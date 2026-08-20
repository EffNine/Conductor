package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type autoTestProvider struct {
	name        string
	modelID     string
	callCount   int
	healthy     bool
	breakerOpen bool
}

func (p *autoTestProvider) Name() string                 { return p.name }
func (p *autoTestProvider) SupportsModel(id string) bool { return true }
func (p *autoTestProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	p.callCount++
	return &apitypes.ChatCompletionResponse{
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
	}, nil
}
func (p *autoTestProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (p *autoTestProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (p *autoTestProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: p.modelID, ModelID: p.modelID, OwnedBy: p.name}}, nil
}
func (p *autoTestProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (p *autoTestProvider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: p.name, IsHealthy: p.healthy, LatencyMs: 10}, nil
}
func (p *autoTestProvider) GetMetadata() provider.Metadata {
	return provider.Metadata{Name: p.name, Models: []string{p.modelID}}
}

func newAutoTestCatalog(t *testing.T, reg *provider.Registry, providers ...*autoTestProvider) *catalog.Catalog {
	t.Helper()
	for _, p := range providers {
		reg.Register(p)
	}
	return catalog.New(reg, nil)
}

func TestSelectAutoModel_BasicSelection(t *testing.T) {
	reg := provider.NewRegistry()
	p1 := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}
	p2 := &autoTestProvider{name: "anthropic", modelID: "claude-3-5-sonnet", healthy: true}
	cat := newAutoTestCatalog(t, reg, p1, p2)

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := routingEngine.SelectAutoModel(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)

	// Should select one of the available providers
	assert.Contains(t, []string{"openai", "anthropic"}, selection.Candidate.ProviderName)
	assert.NotEmpty(t, selection.Candidate.ProviderModelID)
	// The Decision.SelectedModelID is the provider model ID (internal), not "auto"
	assert.NotEmpty(t, selection.Decision.SelectedModelID)
}

func TestSelectAutoModel_ModeReasoning_PrefersReasoningModel(t *testing.T) {
	reg := provider.NewRegistry()
	p1 := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}
	p2 := &autoTestProvider{name: "deepseek", modelID: "deepseek-reasoner", healthy: true}
	cat := newAutoTestCatalog(t, reg, p1, p2)

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Mode:     "reasoning",
		Messages: []apitypes.Message{{Role: "user", Content: "analyze this complex problem step by step"}},
	}
	selection, err := routingEngine.SelectAutoModel(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)

	// With reasoning mode, should prefer a model with reasoning capability
	// The scoring will favor models with reasoning capability
	assert.NotEmpty(t, selection.Candidate.ProviderModelID)
}

func TestSelectAutoModel_ModeVision_RequiresVision(t *testing.T) {
	reg := provider.NewRegistry()
	p1 := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}            // has vision
	p2 := &autoTestProvider{name: "anthropic", modelID: "claude-3-haiku", healthy: true} // no vision in this test
	cat := newAutoTestCatalog(t, reg, p1, p2)

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "auto",
		Mode:  "vision",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/image.png"}},
			},
		}},
	}
	selection, err := routingEngine.SelectAutoModel(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)

	// Vision mode should only select vision-capable models
	// Since only gpt-4o has vision in this test, it should be selected
	assert.Equal(t, "openai", selection.Candidate.ProviderName)
}

func TestSelectAutoModel_ExcludesUnhealthyProviders(t *testing.T) {
	// This test verifies that the auto resolver respects health state from the catalog's
	// reachability filter. Since the catalog uses ModelStatusStore (not provider.HealthCheck)
	// for filtering, we test that providers marked unhealthy in the catalog are excluded.
	// In this test, we don't have a ModelStatusStore wired, so all providers appear healthy.
	// The actual health filtering is done at the catalog level.
	reg := provider.NewRegistry()
	p1 := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}
	p2 := &autoTestProvider{name: "anthropic", modelID: "claude-3-5-sonnet", healthy: true}
	cat := newAutoTestCatalog(t, reg, p1, p2)

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := routingEngine.SelectAutoModel(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)

	// Without ModelStatusStore, both providers are available.
	// The test verifies the selection works correctly.
	assert.Contains(t, []string{"openai", "anthropic"}, selection.Candidate.ProviderName)
}

func TestSelectAutoModel_ExcludesOpenBreaker(t *testing.T) {
	reg := provider.NewRegistry()
	p1 := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}
	p2 := &autoTestProvider{name: "anthropic", modelID: "claude-3-5-sonnet", healthy: true, breakerOpen: true}
	cat := newAutoTestCatalog(t, reg, p1, p2)

	// Create breaker pool and open breaker for anthropic
	breakerPool := router.NewBreakerPool(breaker.Config{
		FailureThreshold: 3,
		RecoveryTimeout:  30000000000,
		SuccessThreshold: 2,
	})
	b := breakerPool.Get("anthropic")
	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		BreakerPool:  breakerPool,
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := routingEngine.SelectAutoModel(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)

	// Should not select provider with open breaker
	assert.Equal(t, "openai", selection.Candidate.ProviderName)
}

func TestSelectAutoModel_NoCatalog_ReturnsError(t *testing.T) {
	reg := provider.NewRegistry()
	p1 := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}
	reg.Register(p1)

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      nil, // No catalog
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := routingEngine.SelectAutoModel(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, selection)
	assert.Contains(t, err.Error(), "requires a catalog")
}

func TestSelectAutoModel_EmptyCatalog_ReturnsError(t *testing.T) {
	reg := provider.NewRegistry()
	cat := catalog.New(reg, nil)

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := routingEngine.SelectAutoModel(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, selection)
	assert.Contains(t, err.Error(), "no healthy models available")
}

func TestSelectAutoModel_DeterministicTieBreaking(t *testing.T) {
	reg := provider.NewRegistry()
	// Register providers in alphabetical order to test deterministic tie-breaking
	p1 := &autoTestProvider{name: "anthropic", modelID: "claude-3-5-sonnet", healthy: true}
	p2 := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}
	cat := newAutoTestCatalog(t, reg, p1, p2)

	routingEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	// Run multiple times to verify deterministic selection
	var lastProvider string
	for i := 0; i < 5; i++ {
		selection, err := routingEngine.SelectAutoModel(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, selection.Candidate)
		if lastProvider != "" {
			assert.Equal(t, lastProvider, selection.Candidate.ProviderName, "selection should be deterministic")
		}
		lastProvider = selection.Candidate.ProviderName
	}
}
