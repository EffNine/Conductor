package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// setupDecisionPipelineHandler creates a handler wired with a shared RouterEngine
// and DecisionPipeline for production integration testing.
func setupDecisionPipelineHandler(t *testing.T, reg *provider.Registry, cfg *config.Config, weights config.RoutingWeights) (*handler.Handler, *router.DecisionPipeline, *runtime.RuntimeStore) {
	t.Helper()
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	store := runtime.NewRuntimeStore(nil)
	for _, p := range reg.All() {
		_ = store.Register(runtime.NewProviderRuntime(p.Name(), p))
	}
	manager := runtime.NewManager(store)

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  weights,
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  routingEngine,
		RuntimeManager: manager,
		Weights:        weights,
	})

	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)

	return h, pipeline, store
}

func readBody(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// failingChatStubProvider returns an error on ChatCompletion to test execution fallback.
type failingChatStubProvider struct {
	name      string
	err       error
	callCount int
	mu        sync.Mutex
}

func (s *failingChatStubProvider) Name() string { return s.name }
func (s *failingChatStubProvider) ChatCompletion(_ context.Context, _ *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	s.mu.Lock()
	s.callCount++
	s.mu.Unlock()
	return nil, s.err
}
func (s *failingChatStubProvider) ChatCompletionStream(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	s.mu.Lock()
	s.callCount++
	s.mu.Unlock()
	return nil, s.err
}
func (s *failingChatStubProvider) Embeddings(_ context.Context, _ *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *failingChatStubProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (s *failingChatStubProvider) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *failingChatStubProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *failingChatStubProvider) SupportsModel(string) bool { return true }
func (s *failingChatStubProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}

// TestDecisionPipelineProductionFlow verifies that POST /v1/chat/completions
// routes through the DecisionPipeline with a shared RouterEngine.
func TestDecisionPipelineProductionFlow(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name: "openai",
		models: []provider.ModelInfo{
			{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"},
		},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	groq := &routingChatStubProvider{
		name: "groq",
		models: []provider.ModelInfo{
			{ProviderModelID: "llama-3.1-8b-instruct", ModelID: "llama-3.1-8b-instruct", OwnedBy: "groq"},
		},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-2", Model: "llama-3.1-8b-instruct",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "hi"}}},
		},
	}
	reg.Register(openai)
	reg.Register(groq)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"gpt-4o": {Provider: "openai"},
		},
	}
	h, pipeline, store := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateDegraded, "", nil)
		return nil
	})

	app := fiber.New()
	h.Register(app)

	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
	if openai.callCount != 1 {
		t.Fatalf("expected openai called once, got %d", openai.callCount)
	}
	if groq.callCount != 0 {
		t.Fatalf("expected groq not called, got %d", groq.callCount)
	}
	if pipeline.RoutingEngine() == nil {
		t.Fatal("expected DecisionPipeline to have a shared RouterEngine")
	}
}

// TestDecisionPipelineSharedRouterEngine verifies the same RouterEngine instance
// is shared between the DecisionPipeline and the handler.
func TestDecisionPipelineSharedRouterEngine(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	h, pipeline, _ := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	if pipeline.RoutingEngine() == nil {
		t.Fatal("expected shared RouterEngine in DecisionPipeline")
	}

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
}

// TestDecisionPipelineUsesRuntimeSnapshot verifies the pipeline obtains a
// RuntimeSnapshot from the shared RuntimeManager.
func TestDecisionPipelineUsesRuntimeSnapshot(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	h, _, store := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(42)
		return nil
	})

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
}

// TestDecisionPipelineExplicitRouteNoFallback preserves the explicit provider.
func TestDecisionPipelineExplicitRouteNoFallback(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	h, _, _ := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
	if openai.callCount != 1 {
		t.Fatalf("expected openai called once, got %d", openai.callCount)
	}
}

// TestDecisionPipelineExplicitRouteWithFallbackAllowsHealthierWinner verifies
// that when a primary is unhealthy, the RouterEngine selects the healthier fallback.
func TestDecisionPipelineExplicitRouteWithFallbackAllowsHealthierWinner(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	groq := &routingChatStubProvider{
		name:   "groq",
		models: []provider.ModelInfo{{ProviderModelID: "llama-3.1-8b-instruct", ModelID: "llama-3.1-8b-instruct", OwnedBy: "groq"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-2", Model: "llama-3.1-8b-instruct",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "hi"}}},
		},
	}
	reg.Register(openai)
	reg.Register(groq)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
		Fallbacks: map[string][]config.FallbackConfig{
			"gpt-4o": {{Provider: "groq"}},
		},
	}
	h, _, store := setupDecisionPipelineHandler(t, reg, cfg, config.RoutingWeights{Health: 80, Latency: 10, Cost: 5, Capability: 5})

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "", nil)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
	if openai.callCount != 0 {
		t.Fatalf("expected openai (unhealthy) not called, got %d", openai.callCount)
	}
	if groq.callCount != 1 {
		t.Fatalf("expected groq (healthy fallback) called once, got %d", groq.callCount)
	}
}

// TestDecisionPipelineAliasResolution verifies alias normalization is preserved.
func TestDecisionPipelineAliasResolution(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes:  map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
		Aliases: map[string]string{"fast": "gpt-4o"},
	}
	h, _, _ := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "fast", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
	if openai.callCount != 1 {
		t.Fatalf("expected openai called once via alias, got %d", openai.callCount)
	}
}

// TestDecisionPipelineProviderPrefix verifies provider/model prefix resolution.
func TestDecisionPipelineProviderPrefix(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{}
	h, _, _ := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "openai/gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
	if openai.callCount != 1 {
		t.Fatalf("expected openai called once via prefix, got %d", openai.callCount)
	}
}

// TestDecisionPipelineAutoMode verifies intelligent routing when no explicit route exists.
func TestDecisionPipelineAutoMode(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	groq := &routingChatStubProvider{
		name:   "groq",
		models: []provider.ModelInfo{{ProviderModelID: "llama-3.1-8b-instruct", ModelID: "llama-3.1-8b-instruct", OwnedBy: "groq"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-2", Model: "llama-3.1-8b-instruct",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "hi"}}},
		},
	}
	reg.Register(openai)
	reg.Register(groq)

	cfg := &config.Config{}
	h, _, store := setupDecisionPipelineHandler(t, reg, cfg, config.RoutingWeights{Health: 10, Latency: 80, Cost: 5, Capability: 5})

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(200)
		return nil
	})

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
	if openai.callCount != 1 {
		t.Fatalf("expected openai (lower latency) called once, got %d", openai.callCount)
	}
}

// TestDecisionPipelineCodingIntent verifies coding intent classification.
func TestDecisionPipelineCodingIntent(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	h, _, _ := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "write a function that sorts an array"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
}

// TestDecisionPipelineReasoningIntent verifies reasoning intent classification.
func TestDecisionPipelineReasoningIntent(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	h, _, _ := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "analyze and compare the trade-offs of these approaches"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
}

// TestDecisionPipelineVisionCapability verifies vision requests are handled.
func TestDecisionPipelineVisionCapability(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	h, _, _ := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
}

// TestDecisionPipelineFastIntent verifies fast/simple request classification.
func TestDecisionPipelineFastIntent(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	h, _, _ := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
}

// TestDecisionPipelineStreaming verifies streaming requests use the selected route.
func TestDecisionPipelineStreaming(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	groq := &routingChatStubProvider{
		name:   "groq",
		models: []provider.ModelInfo{{ProviderModelID: "llama-3.1-8b-instruct", ModelID: "llama-3.1-8b-instruct", OwnedBy: "groq"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-2", Model: "llama-3.1-8b-instruct",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "hi"}}},
		},
	}
	reg.Register(openai)
	reg.Register(groq)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
	}
	h, _, store := setupDecisionPipelineHandler(t, reg, cfg, config.RoutingWeights{Health: 80, Latency: 10, Cost: 5, Capability: 5})

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "", nil)
		return nil
	})

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}, Stream: true}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
	if openai.callCount != 1 {
		t.Fatalf("expected openai (healthy) called once via streaming, got %d", openai.callCount)
	}
	if groq.callCount != 0 {
		t.Fatalf("expected groq (unhealthy) not called, got %d", groq.callCount)
	}
}

// TestDecisionPipelineExecutionFallbackPreserved verifies execution fallback
// (primary fails -> try fallback provider) still works after pipeline integration.
func TestDecisionPipelineExecutionFallbackPreserved(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &failingChatStubProvider{
		name: "openai",
		err:  provider.ErrNotImplemented,
	}
	groq := &routingChatStubProvider{
		name:   "groq",
		models: []provider.ModelInfo{{ProviderModelID: "llama-3.1-8b-instruct", ModelID: "llama-3.1-8b-instruct", OwnedBy: "groq"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-2", Model: "llama-3.1-8b-instruct",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "hi"}}},
		},
	}
	reg.Register(openai)
	reg.Register(groq)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
		Fallbacks: map[string][]config.FallbackConfig{
			"gpt-4o": {{Provider: "groq"}},
		},
	}
	h, _, store := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 (fallback), got %d: %s", resp.StatusCode, readBody(resp))
	}
	if openai.callCount != 1 {
		t.Fatalf("expected openai (primary) called once, got %d", openai.callCount)
	}
	if groq.callCount != 1 {
		t.Fatalf("expected groq (fallback) called once, got %d", groq.callCount)
	}
}

// TestDecisionPipelineEmbeddingsUnchanged verifies embeddings remain on the legacy path.
func TestDecisionPipelineEmbeddingsUnchanged(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "text-embedding-3-small", ModelID: "text-embedding-3-small", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "text-embedding-3-small",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	reg.Register(openai)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"embedding": {Provider: "openai", ModelID: "text-embedding-3-small"},
		},
	}
	h, _, _ := setupDecisionPipelineHandler(t, reg, cfg, config.DefaultRoutingWeights())

	app := fiber.New()
	h.Register(app)
	embBody := `{"model":"embedding","input":["hello world"]}`
	req := httptest.NewRequest("POST", "/v1/embeddings", strings.NewReader(embBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
	if openai.callCount != 1 {
		t.Fatalf("expected openai called once via legacy embedding route, got %d", openai.callCount)
	}
}

// TestDecisionPipelineNoSecondSelectionPass proves there is only ONE final
// provider-selection authority (RouterEngine via DecisionPipeline).
func TestDecisionPipelineNoSecondSelectionPass(t *testing.T) {
	reg := provider.NewRegistry()
	openai := &routingChatStubProvider{
		name:   "openai",
		models: []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-1", Model: "gpt-4o",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
		},
	}
	groq := &routingChatStubProvider{
		name:   "groq",
		models: []provider.ModelInfo{{ProviderModelID: "llama-3.1-8b-instruct", ModelID: "llama-3.1-8b-instruct", OwnedBy: "groq"}},
		response: &apitypes.ChatCompletionResponse{
			ID: "resp-2", Model: "llama-3.1-8b-instruct",
			Choices: []apitypes.Choice{{Index: 0, Message: &apitypes.Message{Role: "assistant", Content: "hi"}}},
		},
	}
	reg.Register(openai)
	reg.Register(groq)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
		Fallbacks: map[string][]config.FallbackConfig{
			"gpt-4o": {{Provider: "groq"}},
		},
	}
	h, _, store := setupDecisionPipelineHandler(t, reg, cfg, config.RoutingWeights{Health: 80, Latency: 10, Cost: 5, Capability: 5})

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(200)
		return nil
	})

	app := fiber.New()
	h.Register(app)
	reqBody := apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
	if openai.callCount != 1 {
		t.Fatalf("expected openai called once, got %d", openai.callCount)
	}
	if groq.callCount != 0 {
		t.Fatalf("expected groq not called, got %d", groq.callCount)
	}
}
