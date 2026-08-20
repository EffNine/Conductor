package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/cache"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/runtime"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// p312CachedPipelineHandler builds a full handler + DecisionPipeline + cache
// engine for end-to-end cache identity tests.
func p312CachedPipelineHandler(t *testing.T, reg *provider.Registry, cfg *config.Config) (*handler.Handler, *runtime.RuntimeStore) {
	t.Helper()
	h, _, store := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())
	cacheEngine := cache.NewEngine(cfg.Cache, nil, zap.NewNop())
	h.SetCacheEngine(cacheEngine)
	return h, store
}

func p312PostChat(t *testing.T, app *fiber.App, body string) *apitypes.ChatCompletionResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var chatResp apitypes.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return &chatResp
}

// TestP312EndToEndCacheProviderIsolation is the end-to-end regression for the
// cache identity contract: two providers serving the same model slug. When
// routing flips from provider A to provider B (health change), the identical
// request MUST NOT be served from A's cache entry. When routing flips back,
// A's cached response MUST be served without re-executing.
func TestP312EndToEndCacheProviderIsolation(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-openai", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "from-openai"}}},
			Usage:   &apitypes.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		},
	}
	azure := &routingChatStubProvider{
		name:   "azure",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "azure"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-azure", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "from-azure"}}},
			Usage:   &apitypes.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		},
	}
	reg.Register(openai)
	reg.Register(azure)

	cfg := &config.Config{
		Cache: config.CacheConfig{
			Enabled:    true,
			TTL:        5 * time.Minute,
			MaxEntries: 100,
		},
	}
	h, store := p312CachedPipelineHandler(t, reg, cfg)

	setHealth := func(name string, state runtime.ProviderState, latency int64) {
		_ = store.Update(name, func(r runtime.ProviderRuntime) error {
			r.UpdateState(state, "", nil)
			r.RecordLatency(latency)
			return nil
		})
	}
	setHealth("openai", runtime.StateHealthy, 100)
	setHealth("azure", runtime.StateUnhealthy, 100)

	app := fiber.New()
	h.Register(app)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`

	// Request 1: routes to openai (azure unhealthy) -> miss -> execute.
	resp1 := p312PostChat(t, app, body)
	if resp1.Choices[0].Message.Content != "from-openai" {
		t.Fatalf("request 1: expected openai response, got %q", resp1.Choices[0].Message.Content)
	}
	if openai.callCount != 1 {
		t.Fatalf("request 1: expected openai executed once, got %d", openai.callCount)
	}

	// Request 2: routing flips to azure. The identical request must NOT be
	// served from openai's cache entry.
	setHealth("openai", runtime.StateUnhealthy, 100)
	setHealth("azure", runtime.StateHealthy, 100)
	resp2 := p312PostChat(t, app, body)
	if resp2.Choices[0].Message.Content != "from-azure" {
		t.Fatalf("request 2: expected azure response (no cross-provider cache hit), got %q", resp2.Choices[0].Message.Content)
	}
	if azure.callCount != 1 {
		t.Fatalf("request 2: expected azure executed once, got %d", azure.callCount)
	}
	if openai.callCount != 1 {
		t.Fatalf("request 2: openai must not be re-executed, got %d calls", openai.callCount)
	}

	// Request 3: routing flips back to openai. openai's cached response must
	// be served WITHOUT re-executing.
	setHealth("openai", runtime.StateHealthy, 100)
	setHealth("azure", runtime.StateUnhealthy, 100)
	resp3 := p312PostChat(t, app, body)
	if resp3.Choices[0].Message.Content != "from-openai" {
		t.Fatalf("request 3: expected openai cached response, got %q", resp3.Choices[0].Message.Content)
	}
	if openai.callCount != 1 {
		t.Fatalf("request 3: expected openai cache hit (no re-execution), got %d calls", openai.callCount)
	}
	if azure.callCount != 1 {
		t.Fatalf("request 3: azure must not be re-executed, got %d calls", azure.callCount)
	}
}

// TestP312EndToEndProviderFallbackTelemetry is the end-to-end regression for
// the P3.12 telemetry precedence fix: a provider whose MODEL-level telemetry
// is INSUFFICIENT (2 samples) must still receive its MEASURED-good PROVIDER
// telemetry bonus in agentic mode. Under the old all-or-nothing rule the
// model entry blocked the provider signal and the provider lost the
// deterministic tie-break.
func TestP312EndToEndProviderFallbackTelemetry(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-openai", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	anthropic := &routingChatStubProvider{
		name:   "anthropic",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "anthropic"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-anthropic", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)
	reg.Register(anthropic)

	cfg := &config.Config{}
	h, _, store := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		// 2 model-level samples (INSUFFICIENT) + MEASURED-good provider history.
		r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
		r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	_ = store.Update("anthropic", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	app := fiber.New()
	h.Register(app)

	// agentic mode: telemetry preference applies; openai must win on the
	// provider-level fallback bonus (anthropic sorts first, so the OLD
	// behavior would have selected anthropic on the tie-break).
	body := `{"model":"gpt-4o","mode":"agentic","messages":[{"role":"user","content":"build this"}]}`
	p312PostChat(t, app, body)
	if openai.callCount != 1 {
		t.Fatalf("expected openai executed once (provider fallback telemetry), got %d", openai.callCount)
	}
	if anthropic.callCount != 0 {
		t.Fatalf("expected anthropic not executed, got %d", anthropic.callCount)
	}
}
