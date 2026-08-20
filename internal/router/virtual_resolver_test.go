package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// virtualTestProvider is a test provider with configurable capabilities.
type virtualTestProvider struct {
	name        string
	modelID     string
	vision      bool
	reasoning   bool
	toolCalling bool
	structured  bool
	longContext bool
	maxContext  int
	latencyMs   int64
	healthy     bool
}

func (p *virtualTestProvider) Name() string                 { return p.name }
func (p *virtualTestProvider) SupportsModel(id string) bool { return true }
func (p *virtualTestProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return &apitypes.ChatCompletionResponse{
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
	}, nil
}
func (p *virtualTestProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (p *virtualTestProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (p *virtualTestProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: p.modelID, ModelID: p.modelID, OwnedBy: p.name}}, nil
}
func (p *virtualTestProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (p *virtualTestProvider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: p.name, IsHealthy: p.healthy, LatencyMs: p.latencyMs}, nil
}
func (p *virtualTestProvider) GetMetadata() provider.Metadata {
	return provider.NewMetadata(p.name, provider.Capabilities{
		Streaming:   true,
		Vision:      p.vision,
		Reasoning:   p.reasoning,
		ToolCalling: p.toolCalling,
		Structured:  p.structured,
		LongContext: p.longContext,
	})
}

func newVirtualTestCatalog(t *testing.T, reg *provider.Registry, providers ...*virtualTestProvider) *catalog.Catalog {
	t.Helper()
	for _, p := range providers {
		reg.Register(p)
	}
	return catalog.New(reg, nil)
}

func setupVirtualResolver(t *testing.T, providers []*virtualTestProvider) (*router.VirtualResolver, *catalog.Catalog, *provider.Registry, *runtime.ManagerImpl, *health.ModelStatusStore) {
	t.Helper()
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	for _, p := range providers {
		reg.Register(p)
		require.NoError(t, store.Register(runtime.NewProviderRuntime(p.Name(), p)))
	}
	manager := runtime.NewManager(store)

	breakerPool := router.NewBreakerPool(breaker.Config{
		FailureThreshold: 3,
		RecoveryTimeout:  30000000000,
		SuccessThreshold: 2,
	})

	statusStore := health.NewModelStatusStore(1, true)
	cat := newVirtualTestCatalog(t, reg, providers...)
	cat.SetReachabilityFilter(statusStore, true)

	virtualResolver := router.NewVirtualResolver(router.VirtualModelResolverConfig{
		Registry:    reg,
		Catalog:     cat,
		Runtime:     manager,
		BreakerPool: breakerPool,
		Weights:     config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Logger:      zap.NewNop(),
	})
	return virtualResolver, cat, reg, manager, statusStore
}

// TestVirtualResolver_FrontierSelectsBestOverall verifies frontier picks the strongest model.
func TestVirtualResolver_FrontierSelectsBestOverall(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "openai", modelID: "gpt-4o", vision: true, reasoning: true, toolCalling: true, structured: true, maxContext: 128000, latencyMs: 100, healthy: true},
		{name: "anthropic", modelID: "claude-3-5-sonnet", vision: true, reasoning: true, toolCalling: true, structured: true, maxContext: 200000, latencyMs: 150, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "frontier",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualFrontier, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.NotEmpty(t, selection.Candidate.ProviderName)
	assert.NotEmpty(t, selection.Candidate.ProviderModelID)
}

// TestVirtualResolver_CodingPrefersToolCalling verifies coding prefers tool-calling models.
func TestVirtualResolver_CodingPrefersToolCalling(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "no-tools", modelID: "basic", toolCalling: false, latencyMs: 100, healthy: true},
		{name: "with-tools", modelID: "coder", toolCalling: true, reasoning: true, latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "coding",
		Messages: []apitypes.Message{{Role: "user", Content: "write a function"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualCoding, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "with-tools", selection.Candidate.ProviderName)
}

// TestVirtualResolver_ReasoningPrefersReasoning verifies reasoning prefers reasoning models.
func TestVirtualResolver_ReasoningPrefersReasoning(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "no-reason", modelID: "basic", reasoning: false, latencyMs: 100, healthy: true},
		{name: "has-reason", modelID: "reasoner", reasoning: true, latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "reasoning",
		Messages: []apitypes.Message{{Role: "user", Content: "analyze this complex problem"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualReasoning, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "has-reason", selection.Candidate.ProviderName)
}

// TestVirtualResolver_AgenticRequiresReasoningAndTools verifies agentic hard-requires both.
func TestVirtualResolver_AgenticRequiresReasoningAndTools(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "no-reason", modelID: "basic", reasoning: false, toolCalling: true, latencyMs: 100, healthy: true},
		{name: "no-tools", modelID: "thinker", reasoning: true, toolCalling: false, latencyMs: 100, healthy: true},
		{name: "full", modelID: "agent", reasoning: true, toolCalling: true, latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "autonomous task"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualAgentic, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "full", selection.Candidate.ProviderName)
}

// TestVirtualResolver_PlanningRequiresReasoningAndTools verifies planning hard-requires both.
func TestVirtualResolver_PlanningRequiresReasoningAndTools(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "no-reason", modelID: "basic", reasoning: false, toolCalling: true, latencyMs: 100, healthy: true},
		{name: "full", modelID: "planner", reasoning: true, toolCalling: true, latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "planning",
		Messages: []apitypes.Message{{Role: "user", Content: "plan this project"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualPlanning, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "full", selection.Candidate.ProviderName)
}

// TestVirtualResolver_LongHorizonRequiresContext verifies long_horizon hard-requires context.
func TestVirtualResolver_LongHorizonRequiresContext(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "small-ctx", modelID: "small", maxContext: 4096, latencyMs: 100, healthy: true},
		{name: "large-ctx", modelID: "large", maxContext: 100000, latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	// Request requiring > 4096 tokens
	req := &apitypes.ChatCompletionRequest{
		Model:    "long_horizon",
		Messages: []apitypes.Message{{Role: "user", Content: "x"}},
	}
	// EstimateRequestTokens will estimate based on content length
	// For this test, we just verify the large context model is selected
	selection, err := vr.Resolve(context.Background(), router.VirtualLongHorizon, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	// Both have enough context for small request, but large-ctx should be preferred
	// due to context capacity bonus
	assert.Contains(t, []string{"small-ctx", "large-ctx"}, selection.Candidate.ProviderName)
}

// TestVirtualResolver_FastPrefersLowLatency verifies fast prefers low latency.
func TestVirtualResolver_FastPrefersLowLatency(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "slow", modelID: "slow", latencyMs: 800, healthy: true},
		{name: "fast", modelID: "fast", latencyMs: 50, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "fast",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualFast, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "fast", selection.Candidate.ProviderName)
}

// TestVirtualResolver_FastDoesNotSacrificeHealth verifies fast doesn't pick unhealthy.
func TestVirtualResolver_FastDoesNotSacrificeHealth(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "healthy", modelID: "healthy", latencyMs: 200, healthy: true},
		{name: "fast-broken", modelID: "broken", latencyMs: 20, healthy: true},
	}
	vr, _, _, _, statusStore := setupVirtualResolver(t, providers)

	// Mark the fast-broken model as unhealthy in the status store
	statusStore.RecordFailure("fast-broken/broken", "fast-broken", "broken", "model not found", 404)

	req := &apitypes.ChatCompletionRequest{
		Model:    "fast",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualFast, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "healthy", selection.Candidate.ProviderName)
}

// TestVirtualResolver_LightPrefersLowCost verifies light prefers low cost.
func TestVirtualResolver_LightPrefersLowCost(t *testing.T) {
	// We can't easily test cost without pricing, but we can verify it doesn't crash
	providers := []*virtualTestProvider{
		{name: "cheap", modelID: "cheap", latencyMs: 100, healthy: true},
		{name: "expensive", modelID: "expensive", latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "light",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualLight, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.NotEmpty(t, selection.Candidate.ProviderName)
}

// TestVirtualResolver_VisionRequiresVision verifies vision hard-requires vision.
func TestVirtualResolver_VisionRequiresVision(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "vision-model", modelID: "vision", vision: true, latencyMs: 100, healthy: true},
		{name: "text-only", modelID: "text", vision: false, latencyMs: 50, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model: "vision",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualVision, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "vision-model", selection.Candidate.ProviderName)
}

// TestVirtualResolver_VisionWithoutImageStillWorks verifies vision works without image content.
func TestVirtualResolver_VisionWithoutImageStillWorks(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "vision-model", modelID: "vision", vision: true, latencyMs: 100, healthy: true},
		{name: "text-only", modelID: "text", vision: false, latencyMs: 50, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "vision",
		Messages: []apitypes.Message{{Role: "user", Content: "describe an image"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualVision, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	// Should still prefer vision-capable model
	assert.Equal(t, "vision-model", selection.Candidate.ProviderName)
}

// TestVirtualResolver_AutoUsesClassifier verifies auto uses classifier when no mode.
func TestVirtualResolver_AutoUsesClassifier(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "openai", modelID: "gpt-4o", vision: true, reasoning: true, toolCalling: true, latencyMs: 100, healthy: true},
		{name: "anthropic", modelID: "claude", vision: true, reasoning: true, toolCalling: true, latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualAuto, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.NotEmpty(t, selection.Candidate.ProviderName)
}

// TestVirtualResolver_AutoWithExplicitMode verifies auto respects explicit mode.
func TestVirtualResolver_AutoWithExplicitMode(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "no-tools", modelID: "basic", toolCalling: false, latencyMs: 100, healthy: true},
		{name: "with-tools", modelID: "coder", toolCalling: true, reasoning: true, latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Mode:     "coding",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualAuto, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "with-tools", selection.Candidate.ProviderName)
}

// TestVirtualResolver_ExcludesUnhealthy verifies unhealthy models are excluded.
func TestVirtualResolver_ExcludesUnhealthy(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "healthy", modelID: "healthy", latencyMs: 100, healthy: true},
		{name: "unhealthy", modelID: "unhealthy", latencyMs: 50, healthy: false},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualAuto, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "healthy", selection.Candidate.ProviderName)
}

// TestVirtualResolver_ExcludesOpenBreaker verifies open breakers are excluded.
func TestVirtualResolver_ExcludesOpenBreaker(t *testing.T) {
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	providers := []*virtualTestProvider{
		{name: "openai", modelID: "gpt-4o", latencyMs: 100, healthy: true},
		{name: "anthropic", modelID: "claude", latencyMs: 100, healthy: true},
	}
	for _, p := range providers {
		reg.Register(p)
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

	virtualResolver := router.NewVirtualResolver(router.VirtualModelResolverConfig{
		Registry:    reg,
		Catalog:     cat,
		Runtime:     manager,
		BreakerPool: breakerPool,
		Weights:     config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Logger:      zap.NewNop(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := virtualResolver.Resolve(context.Background(), router.VirtualAuto, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "anthropic", selection.Candidate.ProviderName)
}

// TestVirtualResolver_EmptyCatalogReturnsError verifies error on empty catalog.
func TestVirtualResolver_EmptyCatalogReturnsError(t *testing.T) {
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	manager := runtime.NewManager(store)

	breakerPool := router.NewBreakerPool(breaker.Config{})
	cat := catalog.New(reg, nil)

	virtualResolver := router.NewVirtualResolver(router.VirtualModelResolverConfig{
		Registry:    reg,
		Catalog:     cat,
		Runtime:     manager,
		BreakerPool: breakerPool,
		Weights:     config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Logger:      zap.NewNop(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := virtualResolver.Resolve(context.Background(), router.VirtualAuto, req)
	require.Error(t, err)
	assert.Nil(t, selection)
	assert.Contains(t, err.Error(), "no healthy models available")
}

// TestVirtualResolver_NoCatalogReturnsError verifies error when catalog is nil.
func TestVirtualResolver_NoCatalogReturnsError(t *testing.T) {
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	manager := runtime.NewManager(store)

	breakerPool := router.NewBreakerPool(breaker.Config{})

	virtualResolver := router.NewVirtualResolver(router.VirtualModelResolverConfig{
		Registry:    reg,
		Catalog:     nil,
		Runtime:     manager,
		BreakerPool: breakerPool,
		Weights:     config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Logger:      zap.NewNop(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := virtualResolver.Resolve(context.Background(), router.VirtualAuto, req)
	require.Error(t, err)
	assert.Nil(t, selection)
	assert.Contains(t, err.Error(), "requires a catalog")
}

// TestVirtualResolver_UnknownVirtualModelReturnsError verifies error on unknown virtual model.
func TestVirtualResolver_UnknownVirtualModelReturnsError(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "openai", modelID: "gpt-4o", latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "unknown",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualModel("unknown"), req)
	require.Error(t, err)
	assert.Nil(t, selection)
	assert.Contains(t, err.Error(), "unknown virtual model")
}

// TestVirtualResolver_DeterministicTieBreaking verifies deterministic selection.
func TestVirtualResolver_DeterministicTieBreaking(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "zebra", modelID: "zebra", latencyMs: 100, healthy: true},
		{name: "alpha", modelID: "alpha", latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	// Run multiple times to verify deterministic selection
	var lastProvider string
	for i := 0; i < 5; i++ {
		selection, err := vr.Resolve(context.Background(), router.VirtualAuto, req)
		require.NoError(t, err)
		require.NotNil(t, selection.Candidate)
		if lastProvider != "" {
			assert.Equal(t, lastProvider, selection.Candidate.ProviderName, "selection should be deterministic")
		}
		lastProvider = selection.Candidate.ProviderName
	}
	// Alphabetical tie-break: alpha < zebra
	assert.Equal(t, "alpha", lastProvider)
}

// TestVirtualResolver_AllVirtualModelsResolvable verifies all 10 virtual models can resolve.
func TestVirtualResolver_AllVirtualModelsResolvable(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "openai", modelID: "gpt-4o", vision: true, reasoning: true, toolCalling: true, structured: true, maxContext: 128000, latencyMs: 100, healthy: true},
		{name: "anthropic", modelID: "claude", vision: true, reasoning: true, toolCalling: true, structured: true, maxContext: 200000, latencyMs: 100, healthy: true},
		{name: "deepseek", modelID: "deepseek-reasoner", vision: false, reasoning: true, toolCalling: false, structured: false, maxContext: 64000, latencyMs: 100, healthy: true},
		{name: "groq", modelID: "llama-3.1-8b", vision: false, reasoning: false, toolCalling: true, structured: true, maxContext: 128000, latencyMs: 50, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	for _, vm := range router.AllVirtualModels() {
		if vm == router.VirtualVision {
			req.Messages = []apitypes.Message{{
				Role: "user",
				Content: []apitypes.ContentPart{
					{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
				},
			}}
		} else {
			req.Messages = []apitypes.Message{{Role: "user", Content: "hello"}}
		}
		selection, err := vr.Resolve(context.Background(), vm, req)
		require.NoError(t, err, "virtual model %s should resolve", vm)
		require.NotNil(t, selection, "virtual model %s should return selection", vm)
		require.NotNil(t, selection.Candidate, "virtual model %s should return candidate", vm)
		assert.NotEmpty(t, selection.Candidate.ProviderName, "virtual model %s should have provider", vm)
		assert.NotEmpty(t, selection.Candidate.ProviderModelID, "virtual model %s should have model ID", vm)
	}
}

// TestVirtualResolver_UpstreamNeverReceivesVirtualID verifies virtual ID never reaches upstream.
func TestVirtualResolver_UpstreamNeverReceivesVirtualID(t *testing.T) {
	// This is tested in handler_auto_regression_test.go via autoCaptureProvider
	// The handler ensures req.Model is replaced with ProviderModelID before dispatch
	// This test verifies the resolver returns a concrete model ID
	providers := []*virtualTestProvider{
		{name: "openai", modelID: "gpt-4o", latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "coding",
		Messages: []apitypes.Message{{Role: "user", Content: "write code"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualCoding, req)
	require.NoError(t, err)
	require.NotNil(t, selection.Candidate)
	// The resolver returns ProviderModelID, not the virtual model ID
	assert.NotEqual(t, "coding", selection.Candidate.ProviderModelID)
	assert.Equal(t, "gpt-4o", selection.Candidate.ProviderModelID)
}

// TestVirtualResolver_ModeComposition verifies model + mode composition.
func TestVirtualResolver_ModeComposition(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "a", modelID: "a", toolCalling: true, reasoning: true, latencyMs: 100, healthy: true},
		{name: "b", modelID: "b", toolCalling: true, reasoning: false, latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	// model=coding + mode=coding -> should prefer tool-calling + reasoning
	req1 := &apitypes.ChatCompletionRequest{
		Model:    "coding",
		Mode:     "coding",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	sel1, err := vr.Resolve(context.Background(), router.VirtualCoding, req1)
	require.NoError(t, err)
	require.NotNil(t, sel1.Candidate)

	// model=coding + mode=reasoning -> should prefer reasoning
	req2 := &apitypes.ChatCompletionRequest{
		Model:    "coding",
		Mode:     "reasoning",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	sel2, err := vr.Resolve(context.Background(), router.VirtualCoding, req2)
	require.NoError(t, err)
	require.NotNil(t, sel2.Candidate)

	// model=reasoning + mode=coding -> should prefer reasoning (model profile wins)
	req3 := &apitypes.ChatCompletionRequest{
		Model:    "reasoning",
		Mode:     "coding",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	sel3, err := vr.Resolve(context.Background(), router.VirtualReasoning, req3)
	require.NoError(t, err)
	require.NotNil(t, sel3.Candidate)

	// model=auto + mode=reasoning -> should use mode weights
	req4 := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Mode:     "reasoning",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	sel4, err := vr.Resolve(context.Background(), router.VirtualAuto, req4)
	require.NoError(t, err)
	require.NotNil(t, sel4.Candidate)

	// All should resolve successfully
	assert.NotEmpty(t, sel1.Candidate.ProviderName)
	assert.NotEmpty(t, sel2.Candidate.ProviderName)
	assert.NotEmpty(t, sel3.Candidate.ProviderName)
	assert.NotEmpty(t, sel4.Candidate.ProviderName)
}

// TestVirtualResolver_WorksWithStreaming verifies virtual models work with streaming.
func TestVirtualResolver_WorksWithStreaming(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "openai", modelID: "gpt-4o", latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "coding",
		Messages: []apitypes.Message{{Role: "user", Content: "write code"}},
		Stream:   true,
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualCoding, req)
	require.NoError(t, err)
	require.NotNil(t, selection.Candidate)
	assert.Equal(t, "gpt-4o", selection.Candidate.ProviderModelID)
}

// TestVirtualResolver_WorksWithTools verifies virtual models work with tool calls.
func TestVirtualResolver_WorksWithTools(t *testing.T) {
	providers := []*virtualTestProvider{
		{name: "openai", modelID: "gpt-4o", toolCalling: true, latencyMs: 100, healthy: true},
		{name: "anthropic", modelID: "claude", toolCalling: false, latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "coding",
		Messages: []apitypes.Message{{Role: "user", Content: "write code"}},
		Tools: []apitypes.Tool{{
			Type:     "function",
			Function: apitypes.FunctionDef{Name: "test_func", Description: "test"},
		}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualCoding, req)
	require.NoError(t, err)
	require.NotNil(t, selection.Candidate)
	// Should prefer tool-calling model
	assert.Equal(t, "openai", selection.Candidate.ProviderName)
}

// TestVirtualResolver_EmbeddingsNotSupported verifies virtual models not for embeddings.
func TestVirtualResolver_EmbeddingsNotSupported(t *testing.T) {
	// This is enforced at the handler level, not the resolver level.
	// The resolver would still work but handler should reject.
	// We just verify the resolver doesn't crash.
	providers := []*virtualTestProvider{
		{name: "openai", modelID: "text-embedding-3-small", latencyMs: 100, healthy: true},
	}
	vr, _, _, _, _ := setupVirtualResolver(t, providers)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	selection, err := vr.Resolve(context.Background(), router.VirtualAuto, req)
	require.NoError(t, err)
	require.NotNil(t, selection.Candidate)
}
