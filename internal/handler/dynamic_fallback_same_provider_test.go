package handler_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// rc1SelectiveProvider models one upstream serving several models where only
// some fail: bad-model errors, every other model succeeds. This reproduces
// the free-tier reality (e.g. NVIDIA NIM) behind same-provider model-level
// dynamic fallback.
type rc1SelectiveProvider struct {
	mu    sync.Mutex
	calls map[string]int
}

func newRC1SelectiveProvider() *rc1SelectiveProvider {
	return &rc1SelectiveProvider{calls: make(map[string]int)}
}

func (p *rc1SelectiveProvider) Name() string                 { return "openai" }
func (p *rc1SelectiveProvider) SupportsModel(id string) bool { return true }

func (p *rc1SelectiveProvider) ChatCompletion(_ context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls[req.Model]++
	p.mu.Unlock()
	if req.Model == "bad-model" {
		return nil, errors.New("upstream exploded")
	}
	return &apitypes.ChatCompletionResponse{
		ID:    "resp-rc1",
		Model: req.Model,
		Choices: []apitypes.Choice{{
			Index: 0,
			Message: &apitypes.Message{
				Role:    "assistant",
				Content: "ok from good-model",
			},
		}},
	}, nil
}

func (p *rc1SelectiveProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	resp, err := p.ChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan apitypes.StreamChunk, 2)
	ch <- apitypes.StreamChunk{
		ID:    resp.ID,
		Model: resp.Model,
		Choices: []apitypes.Choice{{
			Index: 0,
			Delta: &apitypes.Message{Role: "assistant", Content: "ok from good-model"},
		}},
	}
	close(ch)
	return ch, nil
}

func (p *rc1SelectiveProvider) Embeddings(_ context.Context, _ *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (p *rc1SelectiveProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{
		{ProviderModelID: "bad-model", OwnedBy: "rc1"},
		{ProviderModelID: "good-model", OwnedBy: "rc1"},
	}, nil
}

func (p *rc1SelectiveProvider) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}

func (p *rc1SelectiveProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: p.Name(), IsHealthy: true}, nil
}

func (p *rc1SelectiveProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(p.Name())
}

// setupRC1SameProvider wires the production routing.enabled=true shape for a
// single provider with two models and an explicit route to the failing one.
func setupRC1SameProvider(t *testing.T) (*handler.Handler, *fiber.App, *rc1SelectiveProvider) {
	t.Helper()

	reg := provider.NewRegistry()
	p := newRC1SelectiveProvider()
	reg.Register(p)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"bad-model": {Provider: "openai"}},
		Circuit: config.CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 50,
			RecoveryTimeout:  time.Minute,
			SuccessThreshold: 2,
		},
	}
	cfg.Routing.DynamicFallback.Enabled = true
	cfg.Routing.DynamicFallback.MaxCandidates = 3

	engine, err := router.NewEngine(cfg, reg)
	require.NoError(t, err)

	cat := catalog.New(reg, nil)
	autoRes := router.NewAutoResolver(router.AutoResolverConfig{
		Registry:    reg,
		Catalog:     cat,
		BreakerPool: engine.BreakerPool(),
		Weights:     config.DefaultRoutingWeights(),
	})

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		BreakerPool:  engine.BreakerPool(),
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
		AutoResolver: autoRes,
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine: routingEngine,
		BreakerPool:   engine.BreakerPool(),
		Logger:        zap.NewNop(),
		Weights:       config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
	})

	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)
	h.SetConfig(cfg)
	h.SetAutoModelResolver(autoRes)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)

	app := fiber.New()
	h.Register(app)
	return h, app, p
}

// TestDynamicFallbackSurvivesPipelineSelectionOnSameProvider guards the RC-1
// fix: after DecisionPipeline selection, remaining candidates were filtered
// by provider name only, which erased same-provider model-level alternates.
// A request must still succeed via another model on the SAME provider when
// intelligent routing is enabled.
func TestDynamicFallbackSurvivesPipelineSelectionOnSameProvider(t *testing.T) {
	_, app, p := setupRC1SameProvider(t)

	for _, stream := range []bool{false, true} {
		body := `{"model":"bad-model","messages":[{"role":"user","content":"hi"}]}`
		if stream {
			body = `{"model":"bad-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		require.NoError(t, err)
		respBody, readErr := io.ReadAll(resp.Body)
		require.NoError(t, readErr)
		resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"stream=%v body=%s", stream, string(respBody))
		assert.Contains(t, string(respBody), "ok from good-model",
			"stream=%v", stream)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	assert.GreaterOrEqual(t, p.calls["bad-model"], 1, "primary attempted first")
	assert.GreaterOrEqual(t, p.calls["good-model"], 1, "same-provider alternate served the request")
}
