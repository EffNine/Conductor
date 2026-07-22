package health_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/provider"
	"go.uber.org/zap"
)

func TestModelProberMarksUnreachableAndFiltersCatalog(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_ = json.NewEncoder(w).Encode(apitypes.ModelList{
				Object: "list",
				Data: []apitypes.ModelInfo{
					{ID: "good/model", Object: "model", OwnedBy: "nvidia"},
					{ID: "bad/model", Object: "model", OwnedBy: "nvidia"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			var req apitypes.ChatCompletionRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Model == "bad/model" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"model not found","type":"not_found"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
				ID:      "chatcmpl-1",
				Object:  "chat.completion",
				Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	reg := provider.NewRegistry()
	reg.Register(newProbeTestProvider("nvidia_nim", srv.URL+"/v1"))

	store := health.NewModelStatusStore(1, true)
	cat := catalog.New(reg, nil)
	cat.SetReachabilityFilter(store, true)

	prober := health.NewModelProber(cat, reg, store, zap.NewNop(), config.ModelHealthConfig{
		Enabled:            true,
		HideUnreachable:    true,
		Timeout:            2 * time.Second,
		Concurrency:        2,
		UnhealthyThreshold: 1,
		Providers:          []string{"nvidia_nim"},
		UnknownAsReachable: true,
	})

	prober.ProbeAll()

	if hits.Load() < 3 { // 1 list + 2 chat probes
		t.Fatalf("expected list+probe hits, got %d", hits.Load())
	}

	entries, err := cat.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := map[string]bool{}
	for _, e := range entries {
		ids[e.ModelID] = true
	}
	if !ids["nvidia_nim/good/model"] {
		t.Fatalf("good model missing from filtered catalog: %v", ids)
	}
	if ids["nvidia_nim/bad/model"] {
		t.Fatalf("bad model should be hidden: %v", ids)
	}

	all, err := cat.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll len = %d, want 2", len(all))
	}
}

func TestModelProberSkipsNonConfiguredProviders(t *testing.T) {
	var chatHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(apitypes.ModelList{
				Object: "list",
				Data:   []apitypes.ModelInfo{{ID: "gpt-test", Object: "model"}},
			})
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			chatHits.Add(1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"1","choices":[{"message":{"role":"assistant","content":"x"}}]}`))
	}))
	defer srv.Close()

	reg := provider.NewRegistry()
	reg.Register(newProbeTestProvider("openai", srv.URL+"/v1"))
	store := health.NewModelStatusStore(1, true)
	cat := catalog.New(reg, nil)
	prober := health.NewModelProber(cat, reg, store, zap.NewNop(), config.ModelHealthConfig{
		Enabled:     true,
		Timeout:     time.Second,
		Concurrency: 1,
		Providers:   []string{"nvidia_nim"},
	})
	prober.ProbeAll()
	if chatHits.Load() != 0 {
		t.Fatalf("openai should not be probed when providers=[nvidia_nim], hits=%d", chatHits.Load())
	}
}

func TestModelProberEmptyProvidersProbesAll(t *testing.T) {
	var chatHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(apitypes.ModelList{
				Object: "list",
				Data:   []apitypes.ModelInfo{{ID: "m1", Object: "model"}},
			})
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			chatHits.Add(1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"1","choices":[{"message":{"role":"assistant","content":"x"}}]}`))
	}))
	defer srv.Close()

	reg := provider.NewRegistry()
	reg.Register(newProbeTestProvider("openai", srv.URL+"/v1"))
	reg.Register(newProbeTestProvider("nvidia_nim", srv.URL+"/v1"))
	store := health.NewModelStatusStore(1, true)
	cat := catalog.New(reg, nil)
	prober := health.NewModelProber(cat, reg, store, zap.NewNop(), config.ModelHealthConfig{
		Enabled:     true,
		Timeout:     time.Second,
		Concurrency: 2,
		Providers:   nil, // empty = all providers
	})
	prober.ProbeAll()
	if chatHits.Load() != 2 {
		t.Fatalf("expected both providers probed, hits=%d want 2", chatHits.Load())
	}
}

type probeTestProvider struct {
	name    string
	baseURL string
	client  *http.Client
}

func newProbeTestProvider(name, baseURL string) *probeTestProvider {
	return &probeTestProvider{
		name:    name,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 2 * time.Second},
	}
}

func (p *probeTestProvider) Name() string { return p.name }

func (p *probeTestProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, provider.NewProviderError(p.name, resp.StatusCode, provider.ErrorTypeInvalidRequest, string(b), nil)
	}
	var out apitypes.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *probeTestProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}

func (p *probeTestProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (p *probeTestProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var apiResp apitypes.ModelList
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}
	out := make([]provider.ModelInfo, 0, len(apiResp.Data))
	for _, m := range apiResp.Data {
		out = append(out, provider.ModelInfo{ProviderModelID: m.ID, ModelID: m.ID, OwnedBy: m.OwnedBy})
	}
	return out, nil
}

func (p *probeTestProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{}, nil
}

func (p *probeTestProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: p.name, IsHealthy: true}, nil
}

func (p *probeTestProvider) SupportsModel(string) bool { return true }

func TestModelProberUsesThinkingBudgetZero(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_ = json.NewEncoder(w).Encode(apitypes.ModelList{
				Object: "list",
				Data:   []apitypes.ModelInfo{{ID: "bytedance/seed-oss-36b-instruct", Object: "model"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode: %v", err)
			}
			_ = json.NewEncoder(w).Encode(apitypes.ChatCompletionResponse{
				ID:      "chatcmpl-1",
				Object:  "chat.completion",
				Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "pong"}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	reg := provider.NewRegistry()
	reg.Register(newProbeTestProvider("nvidia_nim", srv.URL+"/v1"))
	store := health.NewModelStatusStore(1, true)
	cat := catalog.New(reg, nil)
	prober := health.NewModelProber(cat, reg, store, zap.NewNop(), config.ModelHealthConfig{
		Enabled:     true,
		Timeout:     2 * time.Second,
		Concurrency: 1,
		Providers:   []string{"nvidia_nim"},
	})
	prober.ProbeAll()

	if gotBody["thinking_budget"] != float64(0) {
		t.Fatalf("thinking_budget = %v, want 0", gotBody["thinking_budget"])
	}
	if gotBody["max_tokens"] != float64(16) {
		t.Fatalf("max_tokens = %v, want 16", gotBody["max_tokens"])
	}
}

func TestModelProberIgnoresTimeoutsAndTransientErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(apitypes.ModelList{
				Object: "list",
				Data:   []apitypes.ModelInfo{{ID: "slow/model", Object: "model"}},
			})
			return
		}
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream unavailable"}}`))
	}))
	defer srv.Close()

	reg := provider.NewRegistry()
	reg.Register(newProbeTestProvider("nvidia_nim", srv.URL+"/v1"))
	store := health.NewModelStatusStore(1, true)
	cat := catalog.New(reg, nil)
	cat.SetReachabilityFilter(store, true)
	prober := health.NewModelProber(cat, reg, store, zap.NewNop(), config.ModelHealthConfig{
		Enabled:            true,
		HideUnreachable:    true,
		Timeout:            50 * time.Millisecond,
		Concurrency:        1,
		UnhealthyThreshold: 1,
		Providers:          []string{"nvidia_nim"},
		UnknownAsReachable: true,
	})
	prober.ProbeAll()

	if store.Get("nvidia_nim/slow/model") != nil {
		t.Fatalf("timeout/transient probe should not mark model offline: %+v", store.Get("nvidia_nim/slow/model"))
	}
	entries, err := cat.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].ModelID != "nvidia_nim/slow/model" {
		t.Fatalf("model should stay advertised after inconclusive probe: %+v", entries)
	}
}

func TestRecordLiveResultIgnoresNeutralAndTransientFailures(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	prober := health.NewModelProber(nil, nil, store, zap.NewNop(), config.ModelHealthConfig{
		Enabled:   true,
		Providers: []string{"nvidia_nim"},
	})

	prober.RecordLiveResult(
		"nvidia_nim/seed",
		"nvidia_nim",
		"bytedance/seed-oss-36b-instruct",
		provider.NewProviderError("nvidia_nim", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "rate limit", nil),
		100,
	)
	prober.RecordLiveResult(
		"nvidia_nim/seed",
		"nvidia_nim",
		"bytedance/seed-oss-36b-instruct",
		provider.NewProviderError("nvidia_nim", http.StatusBadGateway, provider.ErrorTypeProviderUnavailable, "upstream unavailable", nil),
		100,
	)
	if store.Get("nvidia_nim/seed") != nil {
		t.Fatal("neutral/transient live failures should not update model status")
	}

	prober.RecordLiveResult(
		"nvidia_nim/seed",
		"nvidia_nim",
		"bytedance/seed-oss-36b-instruct",
		provider.NewProviderError("nvidia_nim", http.StatusNotFound, provider.ErrorTypeInvalidRequest, "model not found", nil),
		100,
	)
	st := store.Get("nvidia_nim/seed")
	if st == nil || st.Reachable {
		t.Fatalf("404 live failure should mark model offline: %+v", st)
	}
}
