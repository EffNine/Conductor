package handler_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/catalog"
	"github.com/novexa/gateway/internal/config"
	"github.com/novexa/gateway/internal/handler"
	"github.com/novexa/gateway/internal/provider"
	"github.com/novexa/gateway/internal/router"
	"go.uber.org/zap"
)

type streamStubProvider struct {
	chunks []apitypes.StreamChunk
}

func (s *streamStubProvider) Name() string { return "nvidia_nim" }

func (s *streamStubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *streamStubProvider) ChatCompletionStream(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	ch := make(chan apitypes.StreamChunk, len(s.chunks))
	for _, chunk := range s.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func (s *streamStubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *streamStubProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *streamStubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *streamStubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.Name(), IsHealthy: true}, nil
}

func (s *streamStubProvider) SupportsModel(string) bool { return true }

func TestStreamFlushesReasoningBeforeFinishReason(t *testing.T) {
	stop := "stop"
	prov := &streamStubProvider{
		chunks: []apitypes.StreamChunk{
			{
				ID: "s1", Object: "chat.completion.chunk", Created: 1, Model: "seed",
				Choices: []apitypes.Choice{{
					Index: 0,
					Delta: &apitypes.Message{Role: "assistant", ReasoningContent: "thinking"},
				}},
			},
			{
				ID: "s1", Object: "chat.completion.chunk", Created: 1, Model: "seed",
				Choices: []apitypes.Choice{{
					Index:        0,
					Delta:        &apitypes.Message{},
					FinishReason: &stop,
				}},
			},
			{Done: true},
		},
	}

	reg := provider.NewRegistry()
	reg.Register(prov)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"seed": {Provider: "nvidia_nim", ModelID: "bytedance/seed-oss-36b-instruct"},
		},
	}
	engine := router.NewEngine(cfg, reg)
	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)

	app := fiber.New()
	h.Register(app)

	body := `{"model":"seed","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	events := parseSSEEvents(string(raw))
	if len(events) < 3 {
		t.Fatalf("events = %d, want at least 3\n%s", len(events), raw)
	}

	contentIdx, finishIdx := -1, -1
	for i, ev := range events {
		if ev == "[DONE]" {
			continue
		}
		var chunk apitypes.StreamChunk
		if err := json.Unmarshal([]byte(ev), &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			if choice.Delta != nil && choice.Delta.Content == "thinking" {
				contentIdx = i
			}
			if choice.FinishReason != nil && *choice.FinishReason == "stop" {
				finishIdx = i
			}
		}
	}
	if contentIdx < 0 {
		t.Fatalf("missing synthetic content chunk in %s", raw)
	}
	if finishIdx < 0 {
		t.Fatalf("missing finish_reason chunk in %s", raw)
	}
	if contentIdx >= finishIdx {
		t.Fatalf("content chunk at %d should precede finish at %d\n%s", contentIdx, finishIdx, raw)
	}
	if events[len(events)-1] != "[DONE]" {
		t.Fatalf("last event = %q, want [DONE]", events[len(events)-1])
	}
}

func TestStreamFlushesReasoningOnAbruptClose(t *testing.T) {
	prov := &streamStubProvider{
		chunks: []apitypes.StreamChunk{
			{
				ID: "s1", Object: "chat.completion.chunk", Created: 1, Model: "seed",
				Choices: []apitypes.Choice{{
					Index: 0,
					Delta: &apitypes.Message{ReasoningContent: "partial"},
				}},
			},
			// No Done chunk — simulates upstream timeout / truncated body.
		},
	}

	reg := provider.NewRegistry()
	reg.Register(prov)
	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"seed": {Provider: "nvidia_nim"},
		},
	}
	engine := router.NewEngine(cfg, reg)
	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)

	app := fiber.New()
	h.Register(app)

	body := `{"model":"seed","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	events := parseSSEEvents(string(raw))
	if events[len(events)-1] != "[DONE]" {
		t.Fatalf("expected trailing [DONE], got %v", events)
	}

	foundContent := false
	for _, ev := range events {
		if ev == "[DONE]" {
			continue
		}
		var chunk apitypes.StreamChunk
		if err := json.Unmarshal([]byte(ev), &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			if choice.Delta != nil && strings.Contains(choice.Delta.Content, "partial") {
				foundContent = true
			}
		}
	}
	if !foundContent {
		t.Fatalf("expected flushed content in %s", raw)
	}
}

func TestStreamSkipsEmptyChunksAndOmitsEmptyRole(t *testing.T) {
	stop := "stop"
	prov := &streamStubProvider{
		chunks: []apitypes.StreamChunk{
			{
				ID: "s1", Object: "chat.completion.chunk", Created: 1, Model: "deepseek-v4-flash-free",
				Choices: []apitypes.Choice{{
					Index: 0,
					Delta: &apitypes.Message{Role: "assistant", ReasoningContent: "plan"},
				}},
			},
			{
				ID: "s1", Object: "chat.completion.chunk", Created: 1, Model: "deepseek-v4-flash-free",
				Choices: []apitypes.Choice{{
					Index: 0,
					// Empty role must not be re-emitted (OpenCode Zod rejects it).
					Delta: &apitypes.Message{Role: "", Content: "Hi"},
				}},
			},
			{}, // upstream data: {} — must not wipe model/content for aggregators
			{
				ID: "s1", Object: "chat.completion.chunk", Created: 1, Model: "deepseek-v4-flash-free",
				Choices: []apitypes.Choice{{
					Index:        0,
					Delta:        &apitypes.Message{},
					FinishReason: &stop,
				}},
			},
			{Done: true},
		},
	}

	reg := provider.NewRegistry()
	reg.Register(prov)
	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"deepseek-v4-flash-free": {Provider: "nvidia_nim", ModelID: "deepseek-v4-flash-free"},
		},
	}
	engine := router.NewEngine(cfg, reg)
	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)

	app := fiber.New()
	h.Register(app)

	body := `{"model":"deepseek-v4-flash-free","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	events := parseSSEEvents(string(raw))
	var sawContent, sawEmptyFrame bool
	for _, ev := range events {
		if ev == "[DONE]" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev), &payload); err != nil {
			t.Fatalf("unmarshal event: %v (%s)", err, ev)
		}
		if payload["id"] == nil && payload["model"] == nil {
			choices, _ := payload["choices"].([]any)
			if len(choices) == 0 && payload["usage"] == nil {
				sawEmptyFrame = true
			}
		}
		choices, _ := payload["choices"].([]any)
		for _, rawChoice := range choices {
			choice, _ := rawChoice.(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if delta == nil {
				continue
			}
			if role, ok := delta["role"]; ok && role == "" {
				t.Fatalf("empty role re-emitted in %s", ev)
			}
			if content, _ := delta["content"].(string); content == "Hi" {
				sawContent = true
			}
		}
	}
	if !sawContent {
		t.Fatalf("missing content chunk in %s", raw)
	}
	if sawEmptyFrame {
		t.Fatalf("empty frame forwarded in %s", raw)
	}
}

func parseSSEEvents(body string) []string {
	var events []string
	scanner := bufio.NewScanner(strings.NewReader(body))
	var data string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data != "" {
				events = append(events, data)
				data = ""
			}
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	if data != "" {
		events = append(events, data)
	}
	return events
}

func TestChatCompletionRequestMarshalsThinkingBudget(t *testing.T) {
	budget := 512
	req := apitypes.ChatCompletionRequest{
		Model:          "bytedance/seed-oss-36b-instruct",
		Messages:       []apitypes.Message{{Role: "user", Content: "hi"}},
		ThinkingBudget: &budget,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["thinking_budget"] != float64(512) {
		t.Fatalf("thinking_budget = %v", got["thinking_budget"])
	}
}
