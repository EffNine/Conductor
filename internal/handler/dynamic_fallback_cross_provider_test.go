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
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// rc3CallRecorder records upstream call order across providers so tests can
// assert exact fallback-chain traversal.
type rc3CallRecorder struct {
	mu    sync.Mutex
	calls []string // "provider/model" in attempt order
}

func (r *rc3CallRecorder) record(providerName, model string) {
	r.mu.Lock()
	r.calls = append(r.calls, providerName+"/"+model)
	r.mu.Unlock()
}

func (r *rc3CallRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// rc3FailingProvider fails every model it serves and records each attempt.
type rc3FailingProvider struct {
	name string
	rec  *rc3CallRecorder
}

func (p *rc3FailingProvider) Name() string                   { return p.name }
func (p *rc3FailingProvider) SupportsModel(string) bool      { return true }
func (p *rc3FailingProvider) GetMetadata() provider.Metadata { return provider.DefaultMetadata(p.name) }

func (p *rc3FailingProvider) ChatCompletion(_ context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	p.rec.record(p.name, req.Model)
	return nil, errors.New("upstream exploded")
}

func (p *rc3FailingProvider) ChatCompletionStream(_ context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	p.rec.record(p.name, req.Model)
	return nil, errors.New("upstream exploded")
}

func (p *rc3FailingProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (p *rc3FailingProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (p *rc3FailingProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (p *rc3FailingProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: p.name, IsHealthy: false}, nil
}

// rc3HealthyProvider serves one model successfully and records the attempt.
type rc3HealthyProvider struct {
	name  string
	model string
	rec   *rc3CallRecorder
}

func (p *rc3HealthyProvider) Name() string                   { return p.name }
func (p *rc3HealthyProvider) SupportsModel(string) bool      { return true }
func (p *rc3HealthyProvider) GetMetadata() provider.Metadata { return provider.DefaultMetadata(p.name) }

func (p *rc3HealthyProvider) ChatCompletion(_ context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	p.rec.record(p.name, req.Model)
	return &apitypes.ChatCompletionResponse{
		ID:    "resp-rc3",
		Model: p.model,
		Choices: []apitypes.Choice{{
			Index: 0,
			Message: &apitypes.Message{
				Role:    "assistant",
				Content: "ok from " + p.name,
			},
		}},
	}, nil
}

func (p *rc3HealthyProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	resp, err := p.ChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan apitypes.StreamChunk, 1)
	ch <- apitypes.StreamChunk{
		ID:    resp.ID,
		Model: resp.Model,
		Choices: []apitypes.Choice{{
			Index: 0,
			Delta: &apitypes.Message{Role: "assistant", Content: resp.Choices[0].Message.Content},
		}},
	}
	close(ch)
	return ch, nil
}

func (p *rc3HealthyProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (p *rc3HealthyProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: p.model, OwnedBy: p.name}}, nil
}
func (p *rc3HealthyProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (p *rc3HealthyProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: p.name, IsHealthy: true}, nil
}

// setupRC3CrossProvider wires the production routing.enabled=true shape:
// DecisionPipeline + RouterEngine + AutoResolver + event-bus trace
// persistence into SQLite. Provider alpha exposes two failing models; beta
// exposes one healthy model. The explicit route targets an alpha model.
func setupRC3CrossProvider(t *testing.T) (*fiber.App, *rc3CallRecorder, *database.SQLiteTraceStore, *eventbus.EventBus) {
	t.Helper()

	rec := &rc3CallRecorder{}
	alpha := &rc3FailingProvider{name: "alpha", rec: rec}
	beta := &rc3HealthyProvider{name: "beta", model: "b-good", rec: rec}

	reg := provider.NewRegistry()
	reg.Register(alpha)
	reg.Register(beta)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"a-bad-1": {Provider: "alpha"}},
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

	bus := eventbus.NewEventBus()
	store := newTraceStore(t)
	persist := database.NewTracePersistence(bus, store, zap.NewNop())
	persist.Start()
	t.Cleanup(persist.Stop)

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		BreakerPool:  engine.BreakerPool(),
		Logger:       zap.NewNop(),
		Weights:      config.DefaultRoutingWeights(),
		Catalog:      cat,
		AutoResolver: autoRes,
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine: routingEngine,
		BreakerPool:   engine.BreakerPool(),
		EventBus:      bus,
		Logger:        zap.NewNop(),
		Weights:       config.DefaultRoutingWeights(),
	})

	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)
	h.SetConfig(cfg)
	h.SetAutoModelResolver(autoRes)
	h.SetRoutingEngine(routingEngine)
	h.SetDecisionPipeline(pipeline)
	h.SetTraceStore(store)

	app := fiber.New()
	h.Register(app)
	return app, rec, store, bus
}

// TestCrossProviderDynamicFallbackThroughDecisionPipeline guards the RC-1
// contract end to end with intelligent routing enabled: a failing primary on
// provider alpha must fail over to provider beta through the dynamic tail,
// candidate ordering must be preserved (primary first, alternates after),
// and the persisted decision trace must record every candidate considered.
func TestCrossProviderDynamicFallbackThroughDecisionPipeline(t *testing.T) {
	app, rec, store, bus := setupRC3CrossProvider(t)

	// Capture the decision ID from the DecisionFinished payload so the
	// assertion below reads exactly the trace this request produced.
	var decisionID router.DecisionID
	var mu sync.Mutex
	sub := bus.Subscribe(eventbus.DecisionFinished, func(evt eventbus.Event) {
		if tr, ok := evt.Payload.(*router.DecisionTrace); ok && tr != nil {
			mu.Lock()
			decisionID = tr.DecisionID
			mu.Unlock()
		}
	})
	defer bus.Unsubscribe(eventbus.DecisionFinished, sub)

	body := `{"model":"a-bad-1","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	respBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, readErr)

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"cross-provider dynamic fallback must serve the request, body=%s", string(respBody))
	assert.Contains(t, string(respBody), "ok from beta")

	calls := rec.snapshot()
	require.NotEmpty(t, calls, "upstream must have been attempted")
	assert.Equal(t, "alpha/a-bad-1", calls[0], "primary route must be attempted first")
	assert.Equal(t, "beta/b-good", calls[len(calls)-1], "healthy provider must serve last")
	for i, c := range calls {
		if i < len(calls)-1 {
			assert.NotEqual(t, "beta/b-good", c,
				"no beta attempt may precede chain order, calls=%v", calls)
		}
	}

	mu.Lock()
	id := decisionID
	mu.Unlock()
	require.NotEmpty(t, id, "decision pipeline must publish a decision trace")

	trace := waitForPersistedTraceRC3(t, store, id)
	require.NotNil(t, trace.Winner, "trace must carry a selection winner")

	// The trace must record every candidate considered: the failing primary
	// from alpha and the healthy alternate from beta at minimum.
	var sawPrimary, sawBeta bool
	for _, cs := range trace.CandidateScores {
		if cs.Provider == "alpha" && cs.ProviderID == "a-bad-1" {
			sawPrimary = true
		}
		if cs.Provider == "beta" && cs.ProviderID == "b-good" {
			sawBeta = true
		}
	}
	assert.True(t, sawPrimary, "trace candidate scores must include primary alpha/a-bad-1, got %+v", trace.CandidateScores)
	assert.True(t, sawBeta, "trace candidate scores must include dynamic alternate beta/b-good, got %+v", trace.CandidateScores)
	assert.GreaterOrEqual(t, len(trace.CandidateScores), 2)
}

func waitForPersistedTraceRC3(t *testing.T, store *database.SQLiteTraceStore, id router.DecisionID) *router.DecisionTrace {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		tr, err := store.Get(context.Background(), id)
		if err == nil {
			return tr
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("trace %s not persisted within 5s: %v", id, lastErr)
	return nil
}
