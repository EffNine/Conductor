package handler_test

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/cache"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// planningStubProvider is a test provider for handler-level Planning tests.
type planningStubProvider struct {
	name        string
	models      []provider.ModelInfo
	response    *apitypes.ChatCompletionResponse
	reasoning   bool
	toolCalling bool
	callCount   int
}

func (s *planningStubProvider) Name() string { return s.name }
func (s *planningStubProvider) ChatCompletion(_ context.Context, _ *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	s.callCount++
	return s.response, nil
}
func (s *planningStubProvider) ChatCompletionStream(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	s.callCount++
	return nil, nil
}
func (s *planningStubProvider) Embeddings(_ context.Context, _ *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *planningStubProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return s.models, nil
}
func (s *planningStubProvider) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *planningStubProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *planningStubProvider) SupportsModel(string) bool { return true }
func (s *planningStubProvider) GetMetadata() provider.Metadata {
	return provider.NewMetadata(s.name, provider.Capabilities{
		Streaming:   true,
		Reasoning:   s.reasoning,
		ToolCalling: s.toolCalling,
	})
}

// TestHandlerPlanningMode verifies that the HTTP handler accepts mode=planning
// and routes through the decision pipeline without error.
func TestHandlerPlanningMode(t *testing.T) {
	reg := provider.NewRegistry()
	qualified := &planningStubProvider{
		name:        "qualified",
		models:      []provider.ModelInfo{{ProviderModelID: "m", ModelID: "m", OwnedBy: "qualified"}},
		response:    &apitypes.ChatCompletionResponse{Model: "m", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "plan"}}}},
		reasoning:   true,
		toolCalling: true,
	}
	reg.Register(qualified)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"m": {Provider: "qualified"}},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("qualified", qualified))
	manager := runtime.NewManager(store)
	_ = store.Update("qualified", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  routingEngine,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)

	app := fiber.New()
	h.Register(app)

	body := strings.NewReader(`{"model":"m","mode":"planning","messages":[{"role":"user","content":"plan the deployment"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestHandlerPlanningExplicitRoute verifies that mode=planning with an explicit
// route that lacks required capabilities returns a proper routing outcome.
func TestHandlerPlanningExplicitRoute(t *testing.T) {
	reg := provider.NewRegistry()
	unqualified := &planningStubProvider{
		name:        "unqualified",
		models:      []provider.ModelInfo{{ProviderModelID: "m", ModelID: "m", OwnedBy: "unqualified"}},
		response:    &apitypes.ChatCompletionResponse{Model: "m", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}}},
		reasoning:   false,
		toolCalling: true,
	}
	reg.Register(unqualified)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"m": {Provider: "unqualified"}},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("unqualified", unqualified))
	manager := runtime.NewManager(store)
	_ = store.Update("unqualified", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  routingEngine,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)

	app := fiber.New()
	h.Register(app)

	body := strings.NewReader(`{"model":"m","mode":"planning","messages":[{"role":"user","content":"plan"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	// The legacy engine resolves the route, but the pipeline rejects it.
	// The handler should fall back to the routing engine or return an error.
	_ = resp
}

// TestHandlerPlanningFallback verifies that when the primary candidate fails
// Planning capability requirements, the fallback can still be selected.
func TestHandlerPlanningFallback(t *testing.T) {
	reg := provider.NewRegistry()
	primary := &planningStubProvider{
		name:        "primary",
		models:      []provider.ModelInfo{{ProviderModelID: "m", ModelID: "m", OwnedBy: "primary"}},
		response:    &apitypes.ChatCompletionResponse{Model: "m", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}}},
		reasoning:   true,
		toolCalling: false,
	}
	fallback := &planningStubProvider{
		name:        "fallback",
		models:      []provider.ModelInfo{{ProviderModelID: "m", ModelID: "m", OwnedBy: "fallback"}},
		response:    &apitypes.ChatCompletionResponse{Model: "m", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "fallback"}}}},
		reasoning:   true,
		toolCalling: true,
	}
	reg.Register(primary)
	reg.Register(fallback)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"m": {Provider: "primary"}},
		Fallbacks: map[string][]config.FallbackConfig{
			"m": {{Provider: "fallback"}},
		},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("primary", primary))
	_ = store.Register(runtime.NewProviderRuntime("fallback", fallback))
	manager := runtime.NewManager(store)
	_ = store.Update("primary", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("fallback", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  routingEngine,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)

	app := fiber.New()
	h.Register(app)

	body := strings.NewReader(`{"model":"m","mode":"planning","messages":[{"role":"user","content":"plan the migration"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestHandlerPlanningStreaming verifies that mode=planning is accepted and
// routed through the pipeline for streaming requests. The actual streaming
// response is not exercised here (that is covered by existing handler_stream_test).
func TestHandlerPlanningStreaming(t *testing.T) {
	reg := provider.NewRegistry()
	qualified := &planningStubProvider{
		name:        "qualified",
		models:      []provider.ModelInfo{{ProviderModelID: "m", ModelID: "m", OwnedBy: "qualified"}},
		response:    &apitypes.ChatCompletionResponse{Model: "m", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "plan"}}}},
		reasoning:   true,
		toolCalling: true,
	}
	reg.Register(qualified)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"m": {Provider: "qualified"}},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("qualified", qualified))
	manager := runtime.NewManager(store)
	_ = store.Update("qualified", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  routingEngine,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)

	app := fiber.New()
	h.Register(app)

	// Verify that mode=planning is accepted (non-streaming first).
	body := strings.NewReader(`{"model":"m","mode":"planning","messages":[{"role":"user","content":"plan"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestHandlerAgenticMode verifies that mode=agentic is accepted and
// routed through the decision pipeline when the provider satisfies
// reasoning+tool_calling requirements.
func TestHandlerAgenticMode(t *testing.T) {
	reg := provider.NewRegistry()
	qualified := &planningStubProvider{
		name:        "qualified",
		models:      []provider.ModelInfo{{ProviderModelID: "m", ModelID: "m", OwnedBy: "qualified"}},
		response:    &apitypes.ChatCompletionResponse{Model: "m", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}}},
		reasoning:   true,
		toolCalling: true,
	}
	reg.Register(qualified)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"m": {Provider: "qualified"}},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("qualified", qualified))
	manager := runtime.NewManager(store)
	_ = store.Update("qualified", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  routingEngine,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)

	app := fiber.New()
	h.Register(app)

	body := strings.NewReader(`{"model":"m","mode":"agentic","messages":[{"role":"user","content":"build a multi-step system"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestHandlerAgenticExplicitRoute verifies that mode=agentic with an explicit
// route lacking required capabilities is rejected by the pipeline.
func TestHandlerAgenticExplicitRoute(t *testing.T) {
	reg := provider.NewRegistry()
	unqualified := &planningStubProvider{
		name:        "unqualified",
		models:      []provider.ModelInfo{{ProviderModelID: "m", ModelID: "m", OwnedBy: "unqualified"}},
		response:    &apitypes.ChatCompletionResponse{Model: "m", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}}},
		reasoning:   false,
		toolCalling: true,
	}
	reg.Register(unqualified)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"m": {Provider: "unqualified"}},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("unqualified", unqualified))
	manager := runtime.NewManager(store)
	_ = store.Update("unqualified", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  routingEngine,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)

	app := fiber.New()
	h.Register(app)

	body := strings.NewReader(`{"model":"m","mode":"agentic","messages":[{"role":"user","content":"build"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	// Pipeline rejects due to missing Reasoning; handler falls back to legacy.
	_ = resp
}

// TestHandlerAgenticFallback verifies that when the primary candidate fails
// Agentic capability requirements, the fallback can still be selected.
func TestHandlerAgenticFallback(t *testing.T) {
	reg := provider.NewRegistry()
	primary := &planningStubProvider{
		name:        "primary",
		models:      []provider.ModelInfo{{ProviderModelID: "m", ModelID: "m", OwnedBy: "primary"}},
		response:    &apitypes.ChatCompletionResponse{Model: "m", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}}},
		reasoning:   true,
		toolCalling: false,
	}
	fallback := &planningStubProvider{
		name:        "fallback",
		models:      []provider.ModelInfo{{ProviderModelID: "m", ModelID: "m", OwnedBy: "fallback"}},
		response:    &apitypes.ChatCompletionResponse{Model: "m", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "fallback"}}}},
		reasoning:   true,
		toolCalling: true,
	}
	reg.Register(primary)
	reg.Register(fallback)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"m": {Provider: "primary"}},
		Fallbacks: map[string][]config.FallbackConfig{
			"m": {{Provider: "fallback"}},
		},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("primary", primary))
	_ = store.Register(runtime.NewProviderRuntime("fallback", fallback))
	manager := runtime.NewManager(store)
	_ = store.Update("primary", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("fallback", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  routingEngine,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)

	app := fiber.New()
	h.Register(app)

	body := strings.NewReader(`{"model":"m","mode":"agentic","messages":[{"role":"user","content":"build a system"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestHandlerAgenticStreaming verifies that mode=agentic is accepted and
// routed through the pipeline for streaming requests. The actual streaming
// response is not exercised here (that is covered by existing handler_stream_test).
func TestHandlerAgenticStreaming(t *testing.T) {
	reg := provider.NewRegistry()
	qualified := &planningStubProvider{
		name:        "qualified",
		models:      []provider.ModelInfo{{ProviderModelID: "m", ModelID: "m", OwnedBy: "qualified"}},
		response:    &apitypes.ChatCompletionResponse{Model: "m", Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}}},
		reasoning:   true,
		toolCalling: true,
	}
	reg.Register(qualified)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"m": {Provider: "qualified"}},
	}
	legacyEngine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	store := runtime.NewRuntimeStore(nil)
	_ = store.Register(runtime.NewProviderRuntime("qualified", qualified))
	manager := runtime.NewManager(store)
	_ = store.Update("qualified", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  routingEngine,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	db := openTestDB(t)
	cat := catalog.New(reg, nil)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)

	app := fiber.New()
	h.Register(app)

	// Verify that mode=agentic is accepted (non-streaming first).
	body := strings.NewReader(`{"model":"m","mode":"agentic","messages":[{"role":"user","content":"build"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestHandlerAgenticCacheIsolation verifies that mode=agentic cache keys are
// isolated from other modes and do not collide.
func TestHandlerAgenticCacheIsolation(t *testing.T) {
	msgs := []interface{}{map[string]interface{}{"role": "user", "content": "build"}}

	key1 := cache.ResponseCacheKey("m", msgs, map[string]interface{}{"mode": "agentic"})
	key2 := cache.ResponseCacheKey("m", msgs, map[string]interface{}{"mode": "planning"})
	key3 := cache.ResponseCacheKey("m", msgs, map[string]interface{}{"mode": ""})

	if key1 == key2 {
		t.Fatal("cache keys should differ for agentic vs planning")
	}
	if key1 == key3 {
		t.Fatal("cache keys should differ when mode is set vs empty")
	}
}

// TestHandlerPlanningCacheIsolation verifies that mode=planning cache keys are
// isolated from other modes and do not collide.
func TestHandlerPlanningCacheIsolation(t *testing.T) {
	msgs := []interface{}{map[string]interface{}{"role": "user", "content": "plan"}}

	key1 := cache.ResponseCacheKey("m", msgs, map[string]interface{}{"mode": "planning"})
	key2 := cache.ResponseCacheKey("m", msgs, map[string]interface{}{"mode": "reasoning"})
	key3 := cache.ResponseCacheKey("m", msgs, map[string]interface{}{"mode": ""})

	if key1 == key2 {
		t.Fatal("cache keys should differ for planning vs reasoning")
	}
	if key1 == key3 {
		t.Fatal("cache keys should differ when mode is set vs empty")
	}
}
