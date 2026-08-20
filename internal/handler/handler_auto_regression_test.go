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
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// autoCaptureProvider is a chat stub that records the exact model ID it
// receives upstream, so tests can prove "auto" never reaches upstream.
type autoCaptureProvider struct {
	name     string
	models   []provider.ModelInfo
	response string

	mu        sync.Mutex
	lastModel string
	calls     int
}

func (p *autoCaptureProvider) Name() string                 { return p.name }
func (p *autoCaptureProvider) SupportsModel(id string) bool { return true }
func (p *autoCaptureProvider) ChatCompletion(_ context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.lastModel = req.Model
	p.calls++
	p.mu.Unlock()
	return &apitypes.ChatCompletionResponse{
		Object: "chat.completion",
		Model:  req.Model,
		Choices: []apitypes.Choice{{
			Index:   0,
			Message: &apitypes.Message{Role: "assistant", Content: p.response},
		}},
	}, nil
}
func (p *autoCaptureProvider) ChatCompletionStream(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (p *autoCaptureProvider) Embeddings(_ context.Context, _ *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (p *autoCaptureProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return p.models, nil
}
func (p *autoCaptureProvider) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (p *autoCaptureProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: p.name, IsHealthy: true, LatencyMs: 10}, nil
}
func (p *autoCaptureProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(p.name)
}

func (p *autoCaptureProvider) receivedModel() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastModel
}

func (p *autoCaptureProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// autoHarness mirrors the production wiring in cmd/conductor/main.go for the
// parts that matter to model="auto": legacy engine, catalog, runtime manager,
// breaker pool, and the shared VirtualResolver. routingEnabled controls whether
// the RouterEngine + DecisionPipeline are wired (production routing.enabled).
type autoHarness struct {
	app       *fiber.App
	h         *handler.Handler
	reg       *provider.Registry
	cat       *catalog.Catalog
	status    *health.ModelStatusStore
	providers map[string]*autoCaptureProvider
}

func setupAutoHarness(t *testing.T, providers []*autoCaptureProvider, routingEnabled bool) *autoHarness {
	t.Helper()

	reg := provider.NewRegistry()
	pmap := make(map[string]*autoCaptureProvider, len(providers))
	for _, p := range providers {
		reg.Register(p)
		pmap[p.name] = p
	}

	legacyEngine, err := router.NewEngine(&config.Config{}, reg)
	require.NoError(t, err)

	store := runtime.NewRuntimeStore(nil)
	for _, p := range providers {
		require.NoError(t, store.Register(runtime.NewProviderRuntime(p.Name(), p)))
	}
	manager := runtime.NewManager(store)

	breakerPool := router.NewBreakerPool(breaker.Config{
		FailureThreshold: 3,
		RecoveryTimeout:  30000000000,
		SuccessThreshold: 2,
	})

	status := health.NewModelStatusStore(1, true)
	cat := catalog.New(reg, nil)

	db := openTestDB(t)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)

	virtualResolver := router.NewVirtualResolver(router.VirtualModelResolverConfig{
		Registry:    reg,
		Catalog:     cat,
		Runtime:     manager,
		BreakerPool: breakerPool,
		Weights:     config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Logger:      zap.NewNop(),
	})
	h.SetVirtualResolver(virtualResolver)

	// Also create an AutoResolver for the RouterEngine (it expects AutoResolver type).
	autoResolver := router.NewAutoResolver(router.AutoResolverConfig{
		Registry:    reg,
		Catalog:     cat,
		Runtime:     manager,
		BreakerPool: breakerPool,
		Weights:     config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Logger:      zap.NewNop(),
	})

	if routingEnabled {
		routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
			Registry:     reg,
			Runtime:      manager,
			BreakerPool:  breakerPool,
			Logger:       zap.NewNop(),
			Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
			Catalog:      cat,
			AutoResolver: autoResolver,
		})
		pipeline := router.NewDecisionPipeline(router.PipelineConfig{
			RoutingEngine:  routingEngine,
			RuntimeManager: manager,
			BreakerPool:    breakerPool,
			Logger:         zap.NewNop(),
			Weights:        config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		})
		h.SetRoutingEngine(routingEngine)
		h.SetDecisionPipeline(pipeline)
	}

	app := fiber.New()
	h.Register(app)

	return &autoHarness{
		app:       app,
		h:         h,
		reg:       reg,
		cat:       cat,
		status:    status,
		providers: pmap,
	}
}

// postChat posts a chat completion request and returns the parsed response.
func postChat(t *testing.T, app *fiber.App, model, mode string) (*apitypes.ChatCompletionResponse, *http.Response) {
	t.Helper()
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
		},
	}
	if mode != "" {
		body["mode"] = mode
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /v1/chat/completions model=%q mode=%q -> %d: %s", model, mode, resp.StatusCode, string(b))
	}

	var chat apitypes.ChatCompletionResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&chat))
	return &chat, resp
}

// assertAutoContract verifies the required invariants of one auto request:
// 200, concrete model selected, upstream never receives "auto".
func assertAutoContract(t *testing.T, h *autoHarness, chat *apitypes.ChatCompletionResponse) string {
	t.Helper()
	require.NotEmpty(t, chat.Model, "response must carry the concrete provider model")
	require.NotEqual(t, "auto", chat.Model, "response model must be concrete, not 'auto'")
	require.NotEmpty(t, chat.Choices, "must produce a completion")
	require.NotEmpty(t, chat.Choices[0].Message.Content, "must produce content")

	for name, p := range h.providers {
		if p.callCount() > 0 {
			require.NotEqual(t, "auto", p.receivedModel(), "provider %q must never receive model=auto upstream", name)
		}
	}
	return chat.Model
}

func newTestProviders() []*autoCaptureProvider {
	return []*autoCaptureProvider{
		{
			name:     "anthropic",
			models:   []provider.ModelInfo{{ProviderModelID: "claude-3-5-sonnet", ModelID: "claude-3-5-sonnet", OwnedBy: "anthropic"}},
			response: "anthropic-ok",
		},
		{
			name:     "openai",
			models:   []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
			response: "openai-ok",
		},
	}
}

// The regression this milestone locked: model="auto" through the REAL handler
// with routing disabled must succeed (previously fell through to legacy
// resolution and returned "Model 'auto' not found").
func TestAutoModelWorksWithRoutingDisabled(t *testing.T) {
	h := setupAutoHarness(t, newTestProviders(), false)

	chat, _ := postChat(t, h.app, "auto", "")
	assertAutoContract(t, h, chat)

	// The upstream provider that answered received its own concrete model ID.
	var answered bool
	for _, p := range h.providers {
		if p.callCount() > 0 {
			answered = true
			require.Equal(t, chat.Model, p.receivedModel())
		}
	}
	require.True(t, answered, "exactly one provider should have answered")
}

func TestAutoModelWorksWithRoutingEnabled(t *testing.T) {
	h := setupAutoHarness(t, newTestProviders(), true)

	chat, _ := postChat(t, h.app, "auto", "")
	assertAutoContract(t, h, chat)
}

func TestAutoModelReasoningWithRoutingDisabled(t *testing.T) {
	providers := []*autoCaptureProvider{
		{
			name:     "deepseek",
			models:   []provider.ModelInfo{{ProviderModelID: "deepseek-reasoner", ModelID: "deepseek-reasoner", OwnedBy: "deepseek"}},
			response: "deepseek-ok",
		},
		{
			name:     "openai",
			models:   []provider.ModelInfo{{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"}},
			response: "openai-ok",
		},
	}
	h := setupAutoHarness(t, providers, false)

	chat, _ := postChat(t, h.app, "auto", "reasoning")
	assertAutoContract(t, h, chat)
}

func TestAutoModelCodingWithRoutingDisabled(t *testing.T) {
	h := setupAutoHarness(t, newTestProviders(), false)

	chat, _ := postChat(t, h.app, "auto", "coding")
	assertAutoContract(t, h, chat)
}

func TestAutoModelHealthFilteringWithRoutingDisabled(t *testing.T) {
	h := setupAutoHarness(t, newTestProviders(), false)

	// Mark the openai model confirmed-unhealthy (reaches the store's
	// unhealthy threshold immediately). The catalog reachability filter must
	// hide it from auto selection even though routing is disabled.
	h.status.RecordFailure("openai/gpt-4o", "openai", "gpt-4o", "model not found", http.StatusNotFound)
	h.cat.SetReachabilityFilter(h.status, true)

	chat, _ := postChat(t, h.app, "auto", "")
	assertAutoContract(t, h, chat)

	require.Equal(t, "claude-3-5-sonnet", chat.Model, "auto must pick the only healthy model")
	require.Equal(t, 0, h.providers["openai"].callCount(), "unhealthy provider must not be called")
	require.Equal(t, 1, h.providers["anthropic"].callCount())
}

func TestAutoModelBreakerFilteringWithRoutingDisabled(t *testing.T) {
	// Rebuild harness with an open breaker on openai. The breaker pool is
	// wired into the resolver via VirtualResolverConfig.
	reg := provider.NewRegistry()
	providers := newTestProviders()
	for _, p := range providers {
		reg.Register(p)
	}

	legacyEngine, err := router.NewEngine(&config.Config{}, reg)
	require.NoError(t, err)

	store := runtime.NewRuntimeStore(nil)
	for _, p := range providers {
		require.NoError(t, store.Register(runtime.NewProviderRuntime(p.Name(), p)))
	}
	manager := runtime.NewManager(store)

	breakerPool := router.NewBreakerPool(breaker.Config{
		FailureThreshold: 3,
		RecoveryTimeout:  30000000000,
		SuccessThreshold: 2,
	})
	openaiBreaker := breakerPool.Get("openai")
	for i := 0; i < 5; i++ {
		openaiBreaker.RecordFailure()
	}

	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(legacyEngine, reg, nil, zap.NewNop(), cat, db)
	virtualResolver := router.NewVirtualResolver(router.VirtualModelResolverConfig{
		Registry:    reg,
		Catalog:     cat,
		Runtime:     manager,
		BreakerPool: breakerPool,
		Weights:     config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Logger:      zap.NewNop(),
	})
	h.SetVirtualResolver(virtualResolver)

	app := fiber.New()
	h.Register(app)

	pmap := map[string]*autoCaptureProvider{}
	for _, p := range providers {
		pmap[p.name] = p
	}
	harness := &autoHarness{app: app, h: h, reg: reg, cat: cat, providers: pmap}

	chat, _ := postChat(t, app, "auto", "")
	assertAutoContract(t, harness, chat)

	require.Equal(t, "claude-3-5-sonnet", chat.Model, "auto must skip the provider with an open breaker")
	require.Equal(t, 0, pmap["openai"].callCount())
	require.Equal(t, 1, pmap["anthropic"].callCount())
}

// Explicit concrete models must keep working unchanged when routing is
// disabled (legacy resolution path).
func TestConcreteModelWithRoutingDisabledUnchanged(t *testing.T) {
	h := setupAutoHarness(t, newTestProviders(), false)

	chat, _ := postChat(t, h.app, "openai/gpt-4o", "")
	assertAutoContract(t, h, chat)
	require.Equal(t, "gpt-4o", chat.Model)
	require.Equal(t, "gpt-4o", h.providers["openai"].receivedModel())
	require.Equal(t, 1, h.providers["openai"].callCount())
	require.Equal(t, 0, h.providers["anthropic"].callCount())
}

// /v1/models keeps exposing the virtual auto model with routing disabled.
func TestListModelsExposesVirtualAutoWithRoutingDisabled(t *testing.T) {
	h := setupAutoHarness(t, newTestProviders(), false)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	resp, err := h.app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var list apitypes.ModelList
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))

	var found bool
	for _, m := range list.Data {
		if m.ID == "auto" {
			found = true
			assert.Equal(t, "conductor", m.OwnedBy)
		}
	}
	require.True(t, found, "virtual auto model must be advertised")
}
