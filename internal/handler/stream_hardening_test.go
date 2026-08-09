package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp/fasthttputil"
	"go.uber.org/zap"
)

const streamTestBody = `{"model":"seed","messages":[{"role":"user","content":"hi"}],"stream":true}`

// scriptedProvider delegates ChatCompletionStream to a per-test function so
// each hardening scenario can control chunk ordering and lifecycle.
type scriptedProvider struct {
	name     string
	streamFn func(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error)
}

func (s *scriptedProvider) Name() string { return s.name }

func (s *scriptedProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *scriptedProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return s.streamFn(ctx, req)
}

func (s *scriptedProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *scriptedProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *scriptedProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *scriptedProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}

func (s *scriptedProvider) SupportsModel(string) bool { return true }
func (s *scriptedProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}

func newStreamTestApp(t testing.TB, p provider.Provider, opts ...func(*handler.Handler)) (*fiber.App, *handler.Handler) {
	t.Helper()
	reg := provider.NewRegistry()
	reg.Register(p)
	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"seed": {Provider: p.Name(), ModelID: "seed-model"},
		},
	}
	engine := router.NewEngine(cfg, reg)
	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)
	for _, opt := range opts {
		opt(h)
	}
	app := fiber.New()
	h.Register(app)
	return app, h
}

func postStream(app *fiber.App, timeout int) (string, int) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(streamTestBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, timeout)
	if err != nil {
		return "", 0
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return buf.String(), resp.StatusCode
}

// waitFor polls cond until it returns true or the deadline passes.
func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

// ── Successful streaming ──────────────────────────────────────────────────

func TestStreamHardeningSuccessRecordsMetrics(t *testing.T) {
	stop := "stop"
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk)
			go func() {
				ch <- apitypes.StreamChunk{ID: "s1", Object: "chat.completion.chunk", Created: 1, Model: "seed-model", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Role: "assistant", Content: "hello"}}}}
				ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{}, FinishReason: &stop}}}
				ch <- apitypes.StreamChunk{Done: true}
				close(ch)
			}()
			return ch, nil
		},
	}
	app, h := newStreamTestApp(t, p)

	body, _ := postStream(app, 5000)
	events := parseSSEEvents(body)
	if len(events) < 2 || events[len(events)-1] != "[DONE]" {
		t.Fatalf("expected [DONE]-terminated stream, got events: %v\n%s", events, body)
	}

	waitFor(t, func() bool { return h.Metrics().Snapshot().ActiveStreams == 0 }, 3*time.Second, "active streams did not drain")

	snap := h.Metrics().Snapshot()
	if snap.StreamStarted < 1 {
		t.Fatalf("StreamStarted = %d, want >= 1", snap.StreamStarted)
	}
	if snap.StreamCompleted < 1 {
		t.Fatalf("StreamCompleted = %d, want >= 1", snap.StreamCompleted)
	}
	if snap.StreamChunksTotal < 2 {
		t.Fatalf("StreamChunksTotal = %d, want >= 2", snap.StreamChunksTotal)
	}
	if snap.StreamBytesTotal <= 0 {
		t.Fatalf("StreamBytesTotal = %d, want > 0", snap.StreamBytesTotal)
	}
	if snap.StreamDurationMs.Count < 1 {
		t.Fatalf("StreamDurationMs.Count = %d, want >= 1", snap.StreamDurationMs.Count)
	}
	ps, ok := snap.StreamStatsByProvider["nvidia_nim"]
	if !ok {
		t.Fatalf("missing per-provider stats for nvidia_nim: %v", snap.StreamStatsByProvider)
	}
	if ps.Started < 1 || ps.Completed < 1 {
		t.Fatalf("per-provider stats = %+v, want started+completed >= 1", ps)
	}
}

// ── Provider disconnect (channel closed without [DONE]) ───────────────────

func TestStreamProviderDisconnectFlushesAndRecordsError(t *testing.T) {
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk)
			go func() {
				ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Content: "partial"}}}}
				// No [DONE] — simulates a truncated provider body.
				close(ch)
			}()
			return ch, nil
		},
	}
	app, h := newStreamTestApp(t, p)

	body, _ := postStream(app, 5000)
	events := parseSSEEvents(body)
	if len(events) < 1 || events[len(events)-1] != "[DONE]" {
		t.Fatalf("expected flushed content + trailing [DONE], got: %v", events)
	}
	waitFor(t, func() bool { return h.Metrics().Snapshot().ActiveStreams == 0 }, 3*time.Second, "active streams did not drain")
	if got := h.Metrics().Snapshot().StreamErrorsTotal; got < 1 {
		t.Fatalf("StreamErrorsTotal = %d, want >= 1 (provider disconnect)", got)
	}
}

// ── Partial stream failure (error chunk mid-stream) ───────────────────────

func TestStreamPartialFailureRecordsError(t *testing.T) {
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk)
			go func() {
				ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Content: "before"}}}}
				ch <- apitypes.StreamChunk{Error: fmt.Errorf("upstream stream error")}
				close(ch)
			}()
			return ch, nil
		},
	}
	app, h := newStreamTestApp(t, p)

	body, _ := postStream(app, 5000)
	events := parseSSEEvents(body)
	if len(events) < 1 || events[len(events)-1] != "[DONE]" {
		t.Fatalf("expected partial content + trailing [DONE], got: %v", events)
	}
	waitFor(t, func() bool { return h.Metrics().Snapshot().ActiveStreams == 0 }, 3*time.Second, "active streams did not drain")
	if got := h.Metrics().Snapshot().StreamErrorsTotal; got < 1 {
		t.Fatalf("StreamErrorsTotal = %d, want >= 1 (partial failure)", got)
	}
}

// ── Provider timeout (idle stream) ────────────────────────────────────────

func TestStreamProviderTimeoutEndsStreamAndCancelsProvider(t *testing.T) {
	cancelled := make(chan struct{})
	var once sync.Once
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(ctx context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk)
			go func() {
				defer close(ch)
				<-ctx.Done()
				once.Do(func() { close(cancelled) })
			}()
			return ch, nil
		},
	}
	app, h := newStreamTestApp(t, p, func(h *handler.Handler) { h.SetStreamIdleTimeout(150 * time.Millisecond) })

	body, _ := postStream(app, 5000)
	events := parseSSEEvents(body)
	if len(events) < 1 || events[len(events)-1] != "[DONE]" {
		t.Fatalf("expected graceful [DONE] after timeout, got: %v", events)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("provider context was not cancelled on idle timeout")
	}
	waitFor(t, func() bool { return h.Metrics().Snapshot().ActiveStreams == 0 }, 3*time.Second, "active streams did not drain")
	if got := h.Metrics().Snapshot().StreamTimeout; got < 1 {
		t.Fatalf("StreamTimeout = %d, want >= 1", got)
	}
}

// ── Client disconnect mid-stream ──────────────────────────────────────────

func TestStreamClientDisconnectCancelsProviderAndRecordsCancelled(t *testing.T) {
	cancelled := make(chan struct{})
	var cancelledOnce sync.Once
	firstChunk := make(chan struct{})
	var firstOnce sync.Once
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(ctx context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk)
			go func() {
				defer close(ch)
				for i := 0; ; i++ {
					select {
					case <-ctx.Done():
						cancelledOnce.Do(func() { close(cancelled) })
						return
					case ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Content: fmt.Sprintf("chunk-%d ", i)}}}}:
						if i == 0 {
							firstOnce.Do(func() { close(firstChunk) })
						}
					}
				}
			}()
			return ch, nil
		},
	}
	app, h := newStreamTestApp(t, p)

	ln := fasthttputil.NewInmemoryListener()
	go func() { _ = app.Listener(ln) }()
	defer ln.Close()

	conn, err := ln.Dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	reqLine := "POST /v1/chat/completions HTTP/1.1\r\n" +
		"Host: 127.0.0.1\r\n" +
		"Content-Type: application/json\r\n" +
		"Content-Length: " + strconv.Itoa(len(streamTestBody)) + "\r\n\r\n" +
		streamTestBody
	if _, err := conn.Write([]byte(reqLine)); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Read until at least one SSE frame has been flushed to the client.
	buf := make([]byte, 8192)
	read := 0
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		n, err := conn.Read(buf[read:])
		read += n
		if err != nil {
			break
		}
		if bytes.Contains(buf[:read], []byte("data: ")) {
			break
		}
	}

	// Client disconnects mid-stream.
	_ = conn.Close()

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("provider stream was not cancelled after client disconnect")
	}

	waitFor(t, func() bool { return h.Metrics().Snapshot().ActiveStreams == 0 }, 5*time.Second, "active streams did not drain after disconnect")
	snap := h.Metrics().Snapshot()
	if snap.StreamCancelled < 1 {
		t.Fatalf("StreamCancelled = %d, want >= 1 (client disconnect)", snap.StreamCancelled)
	}
}

// ── Context cancellation via graceful shutdown ────────────────────────────

func TestStreamContextCancellationOnShutdown(t *testing.T) {
	cancelled := make(chan struct{})
	var once sync.Once
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(ctx context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk)
			go func() {
				defer close(ch)
				ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Content: "hi"}}}}
				<-ctx.Done()
				once.Do(func() { close(cancelled) })
			}()
			return ch, nil
		},
	}
	app, h := newStreamTestApp(t, p)

	ln := fasthttputil.NewInmemoryListener()
	go func() { _ = app.Listener(ln) }()
	defer ln.Close()

	conn, err := ln.Dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	reqLine := "POST /v1/chat/completions HTTP/1.1\r\n" +
		"Host: 127.0.0.1\r\n" +
		"Content-Type: application/json\r\n" +
		"Content-Length: " + strconv.Itoa(len(streamTestBody)) + "\r\n\r\n" +
		streamTestBody
	if _, err := conn.Write([]byte(reqLine)); err != nil {
		t.Fatalf("write request: %v", err)
	}

	buf := make([]byte, 8192)
	read := 0
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		n, err := conn.Read(buf[read:])
		read += n
		if err != nil {
			break
		}
		if bytes.Contains(buf[:read], []byte("data: ")) {
			break
		}
	}

	// Graceful shutdown cancels in-flight request contexts.
	shutdownDone := make(chan struct{})
	go func() {
		_ = app.Shutdown()
		close(shutdownDone)
	}()

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("provider context was not cancelled on server shutdown")
	}
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete")
	}

	waitFor(t, func() bool { return h.Metrics().Snapshot().ActiveStreams == 0 }, 5*time.Second, "active streams did not drain after shutdown")
}

// ── Concurrent streaming ──────────────────────────────────────────────────

func TestStreamConcurrentStreamsCompleteAndSettle(t *testing.T) {
	stop := "stop"
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk)
			go func() {
				defer close(ch)
				ch <- apitypes.StreamChunk{ID: "s1", Object: "chat.completion.chunk", Created: 1, Model: "seed-model", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Role: "assistant", Content: "x"}}}}
				ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{}, FinishReason: &stop}}}
				ch <- apitypes.StreamChunk{Done: true}
			}()
			return ch, nil
		},
	}
	app, h := newStreamTestApp(t, p)

	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _ := postStream(app, 10000)
			events := parseSSEEvents(body)
			if len(events) == 0 || events[len(events)-1] != "[DONE]" {
				t.Errorf("stream missing [DONE]: %v", events)
			}
		}()
	}
	wg.Wait()

	waitFor(t, func() bool { return h.Metrics().Snapshot().ActiveStreams == 0 }, 5*time.Second, "active streams did not drain")
	snap := h.Metrics().Snapshot()
	if snap.StreamStarted < workers {
		t.Fatalf("StreamStarted = %d, want >= %d", snap.StreamStarted, workers)
	}
	if snap.StreamCompleted < workers {
		t.Fatalf("StreamCompleted = %d, want >= %d", snap.StreamCompleted, workers)
	}
}

// ── Goroutine leak detection ──────────────────────────────────────────────

func TestStreamNoGoroutineLeaks(t *testing.T) {
	stop := "stop"
	mkProvider := func() *scriptedProvider {
		return &scriptedProvider{
			name: "nvidia_nim",
			streamFn: func(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
				ch := make(chan apitypes.StreamChunk)
				go func() {
					defer close(ch)
					ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Content: "a"}}}}
					ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{}, FinishReason: &stop}}}
					ch <- apitypes.StreamChunk{Done: true}
				}()
				return ch, nil
			},
		}
	}
	app, h := newStreamTestApp(t, mkProvider())
	for i := 0; i < 40; i++ {
		postStream(app, 10000)
	}
	waitFor(t, func() bool { return h.Metrics().Snapshot().ActiveStreams == 0 }, 5*time.Second, "active streams did not drain")

	// Let leaked goroutines surface before sampling the baseline.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	baseline := runtime.NumGoroutine()

	for i := 0; i < 60; i++ {
		postStream(app, 10000)
	}
	waitFor(t, func() bool { return h.Metrics().Snapshot().ActiveStreams == 0 }, 5*time.Second, "active streams did not drain")
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	const slack = 8
	waitFor(t, func() bool { return runtime.NumGoroutine() <= baseline+slack }, 5*time.Second,
		fmt.Sprintf("goroutine leak detected: baseline=%d now=%d", baseline, runtime.NumGoroutine()))
}

// ── Dashboard endpoint ────────────────────────────────────────────────────

func TestStreamDashboardEndpoint(t *testing.T) {
	stop := "stop"
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk)
			go func() {
				defer close(ch)
				ch <- apitypes.StreamChunk{ID: "s1", Object: "chat.completion.chunk", Created: 1, Model: "seed-model", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Role: "assistant", Content: "hello"}}}}
				ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{}, FinishReason: &stop}}}
				ch <- apitypes.StreamChunk{Done: true}
			}()
			return ch, nil
		},
	}
	app, h := newStreamTestApp(t, p)
	postStream(app, 5000)
	waitFor(t, func() bool { return h.Metrics().Snapshot().StreamCompleted >= 1 }, 3*time.Second, "stream did not complete")

	req, _ := http.NewRequest(http.MethodGet, "/api/streams", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("dashboard request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["active_streams"] != float64(0) {
		t.Fatalf("active_streams = %v, want 0", payload["active_streams"])
	}
	if payload["streams_completed"].(float64) < 1 {
		t.Fatalf("streams_completed = %v, want >= 1", payload["streams_completed"])
	}
	if payload["average_duration_ms"].(float64) < 0 {
		t.Fatalf("average_duration_ms = %v", payload["average_duration_ms"])
	}
	providers, ok := payload["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers missing or wrong type: %v", payload["providers"])
	}
	if _, ok := providers["nvidia_nim"]; !ok {
		t.Fatalf("missing nvidia_nim in providers: %v", providers)
	}
}

// ── Write / flush failure paths via an early-dropping client ─────────────
//
// Small SSE frames are buffered by the response writer, so a disconnect
// surfaces on w.Flush(). Frames larger than the writer's 4 KiB buffer are
// written straight to the response pipe, so a disconnect surfaces on
// w.Write(). Both are detected identically in writeChunk.

func streamWithDisconnectingClient(t *testing.T, chunkContent func(i int) string) *handler.Handler {
	t.Helper()
	cancelled := make(chan struct{})
	var cancelledOnce sync.Once
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(ctx context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk)
			go func() {
				defer close(ch)
				for i := 0; ; i++ {
					select {
					case <-ctx.Done():
						cancelledOnce.Do(func() { close(cancelled) })
						return
					case ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Content: chunkContent(i)}}}}:
					}
				}
			}()
			return ch, nil
		},
	}
	app, h := newStreamTestApp(t, p)

	ln := fasthttputil.NewInmemoryListener()
	go func() { _ = app.Listener(ln) }()
	defer ln.Close()

	conn, err := ln.Dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	reqLine := "POST /v1/chat/completions HTTP/1.1\r\n" +
		"Host: 127.0.0.1\r\n" +
		"Content-Type: application/json\r\n" +
		"Content-Length: " + strconv.Itoa(len(streamTestBody)) + "\r\n\r\n" +
		streamTestBody
	if _, err := conn.Write([]byte(reqLine)); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Read one frame, then drop the connection without consuming the body.
	buf := make([]byte, 8192)
	read := 0
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		n, err := conn.Read(buf[read:])
		read += n
		if err != nil {
			break
		}
		if bytes.Contains(buf[:read], []byte("data: ")) {
			break
		}
	}
	_ = conn.Close()

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("provider stream was not cancelled after client disconnect")
	}
	waitFor(t, func() bool { return h.Metrics().Snapshot().ActiveStreams == 0 }, 5*time.Second, "active streams did not drain after disconnect")
	return h
}

func TestStreamFlushFailureTerminatesCleanly(t *testing.T) {
	h := streamWithDisconnectingClient(t, func(i int) string { return fmt.Sprintf("small-%d ", i) })
	if got := h.Metrics().Snapshot().StreamCancelled; got < 1 {
		t.Fatalf("StreamCancelled = %d, want >= 1 (flush failure)", got)
	}
}

func TestStreamWriteFailureTerminatesCleanly(t *testing.T) {
	h := streamWithDisconnectingClient(t, func(i int) string { return fmt.Sprintf("big-%d-%s", i, strings.Repeat("x", 8192)) })
	if got := h.Metrics().Snapshot().StreamCancelled; got < 1 {
		t.Fatalf("StreamCancelled = %d, want >= 1 (write failure)", got)
	}
}

// ── Benchmarks ──────────────────────────────────────────────────────────────

func BenchmarkStreamSuccessful(b *testing.B) {
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk, 100)
			go func() {
				defer close(ch)
				for i := 0; i < 100; i++ {
					ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Content: "x"}}}}
				}
				ch <- apitypes.StreamChunk{Done: true}
			}()
			return ch, nil
		},
	}
	app, _ := newStreamTestApp(b, p)
	body := streamTestBody

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			b.Fatalf("test: %v", err)
		}
		_ = resp.Body.Close()
	}
}

func BenchmarkStreamConcurrent(b *testing.B) {
	p := &scriptedProvider{
		name: "nvidia_nim",
		streamFn: func(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
			ch := make(chan apitypes.StreamChunk, 100)
			go func() {
				defer close(ch)
				for i := 0; i < 100; i++ {
					ch <- apitypes.StreamChunk{ID: "s1", Choices: []apitypes.Choice{{Index: 0, Delta: &apitypes.Message{Content: "x"}}}}
				}
				ch <- apitypes.StreamChunk{Done: true}
			}()
			return ch, nil
		},
	}
	app, _ := newStreamTestApp(b, p)
	body := streamTestBody

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		errs := make(chan error, 10)
		var wg sync.WaitGroup
		for j := 0; j < 10; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				resp, err := app.Test(req)
				if err != nil {
					errs <- err
					return
				}
				_ = resp.Body.Close()
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			b.Fatal(err)
		}
	}
}
