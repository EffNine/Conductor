package router_test

// Regression tests for C3C: catalog-backed candidate construction must
// include every eligible provider when a reachability filter is wired,
// instead of collapsing to a single provider because non-healthy
// providers get hidden from the catalog before candidate construction.

import (
	"context"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

// fakeReachabilityFilter simulates a health reachability filter. The map
// mirrors a ModelStatusStore's ShouldAdvertise: an absent key means "unprobed
// this provider" (advertise=true, err toward availability); an explicit false
// means "confirmed-unhealthy" (hide); true means "confirmed-healthy".
type fakeReachabilityFilter struct {
	advertise map[string]bool
}

func (f *fakeReachabilityFilter) ShouldAdvertise(_, prov string) bool {
	if f.advertise == nil {
		return true
	}
	hidden, ok := f.advertise[prov]
	if !ok {
		// Unprobed provider: advertise (mirrors unknownAsReachable=true).
		return true
	}
	return hidden
}

// multiProviderCandidateEngine builds a router engine with a catalog-backed
// auto resolver. The catalog is NOT pre-wired with a reachability filter so
// tests can install their own (proving the candidate pool derives from the
// full catalog, not a pre-filtered view).
func multiProviderCandidateEngine(t *testing.T, providers ...provider.Provider) (*router.RouterEngine, *catalog.Catalog) {
	t.Helper()
	reg := provider.NewRegistry()
	for _, p := range providers {
		reg.Register(p)
	}
	cat := catalog.New(reg, nil)
	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
	})
	return engine, cat
}

// TestCandidateConstruction_IncludesAllEligibleProviders is the primary C3C
// regression: two providers, both eligible for default text, candidate set
// must contain BOTH distinct providers.
func TestCandidateConstruction_IncludesAllEligibleProviders(t *testing.T) {
	providerA := &autoTestProvider{name: "provider_a", modelID: "text-model-a", healthy: true}
	providerB := &autoTestProvider{name: "provider_b", modelID: "text-model-b", healthy: true}
	engine, cat := multiProviderCandidateEngine(t, providerA, providerB)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	// Both providers advertised (healthy production state).
	advertiser := &fakeReachabilityFilter{advertise: map[string]bool{"provider_a": true, "provider_b": true}}
	cat.SetReachabilityFilter(advertiser, true)

	resolver := engine.AutoModelResolver()
	if resolver == nil {
		t.Fatal("AutoModelResolver() is nil")
	}
	scored, err := resolver.RankedEligible(context.Background(), req)
	if err != nil {
		t.Fatalf("RankedEligible: %v", err)
	}

	provs := map[string]bool{}
	for _, cs := range scored {
		provs[cs.Provider] = true
	}
	if !provs["provider_a"] || !provs["provider_b"] {
		t.Fatalf("candidate set missing providers: scored=%+v (expected both provider_a and provider_b)", scored)
	}
	t.Logf("multi-provider candidate set: %d candidates, providers=%v", len(scored), provs)
}

// TestCandidateConstruction_ReachesThroughStrictHealthyFilter proves the
// C3C root cause scenario under the ListReachable fix: a provider that is
// UNPROBED (no reachability-filter entry) must still appear in the
// candidate pool even when another provider is confirmed-healthy. The
// strict_healthy pre-gate must not shrink the candidate pool.
//
// Note: a provider that IS confirmed-unhealthy (explicitly hidden by the
// reachability filter) is correctly excluded — that is intended health
// gating, not the C3C defect.
func TestCandidateConstruction_ReachesThroughStrictHealthyFilter(t *testing.T) {
	providerA := &autoTestProvider{name: "provider_a", modelID: "text-model-a", healthy: true}
	providerB := &autoTestProvider{name: "provider_b", modelID: "text-model-b", healthy: true}
	engine, cat := multiProviderCandidateEngine(t, providerA, providerB)

	// Simulate C3C state: provider_a was just probed and marked healthy
	// (advertised), provider_b was never probed (no filter entry = unprobed
	// = must remain a candidate under ListReachable).
	strictFilter := &fakeReachabilityFilter{advertise: map[string]bool{"provider_a": true}}
	// provider_b intentionally absent: unprobed, not confirmed-unhealthy.
	cat.SetReachabilityFilter(strictFilter, true)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	resolver := engine.AutoModelResolver()
	scored, err := resolver.RankedEligible(context.Background(), req)
	if err != nil {
		t.Fatalf("RankedEligible: %v", err)
	}

	provs := map[string]bool{}
	for _, cs := range scored {
		provs[cs.Provider] = true
	}
	if !provs["provider_a"] || !provs["provider_b"] {
		t.Fatalf("unprobed provider_b must remain a candidate under ListReachable: scored=%+v (expected both providers — the reachability filter must not shrink the candidate pool for unprobed providers)", scored)
	}
}

// TestCandidateConstruction_SingleProvider still works when only one
// provider is registered.
func TestCandidateConstruction_SingleProvider(t *testing.T) {
	providerA := &autoTestProvider{name: "provider_a", modelID: "text-model-a", healthy: true}
	engine, cat := multiProviderCandidateEngine(t, providerA)

	advertiser := &fakeReachabilityFilter{advertise: map[string]bool{"provider_a": true}}
	cat.SetReachabilityFilter(advertiser, true)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	resolver := engine.AutoModelResolver()
	scored, err := resolver.RankedEligible(context.Background(), req)
	if err != nil {
		t.Fatalf("RankedEligible: %v", err)
	}
	if len(scored) != 1 {
		t.Fatalf("len(scored) = %d, want 1: %+v", len(scored), scored)
	}
	if scored[0].Provider != "provider_a" {
		t.Fatalf("provider = %s, want provider_a", scored[0].Provider)
	}
}

// TestCandidateConstruction_IneligibleProviderExcluded proves that a
// provider whose circuit breaker is open is still excluded even when the
// reachability filter advertises it — the fix must not bypass breaker
// gating.
func TestCandidateConstruction_IneligibleProviderExcluded(t *testing.T) {
	providerA := &autoTestProvider{name: "provider_a", modelID: "text-model-a", healthy: true}
	providerB := &autoTestProvider{name: "provider_b", modelID: "text-model-b", healthy: true}

	reg := provider.NewRegistry()
	reg.Register(providerA)
	reg.Register(providerB)
	cat := catalog.New(reg, nil)

	breakerPool := router.NewBreakerPool(breaker.Config{
		FailureThreshold: 3,
		RecoveryTimeout:  30 * time.Second,
		SuccessThreshold: 2,
	})
	b := breakerPool.Get("provider_b")
	for i := 0; i < 5; i++ {
		b.RecordFailure()
	}

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: router.NewMetricsStore(),
		BreakerPool:  breakerPool,
		Runtime:      nil,
		Logger:       zap.NewNop(),
		Weights:      config.RoutingWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20},
		Catalog:      cat,
	})

	advertiser := &fakeReachabilityFilter{advertise: map[string]bool{"provider_a": true, "provider_b": true}}
	cat.SetReachabilityFilter(advertiser, true)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	resolver := engine.AutoModelResolver()
	if resolver == nil {
		t.Fatal("AutoModelResolver() is nil")
	}
	scored, err := resolver.RankedEligible(context.Background(), req)
	if err != nil {
		t.Fatalf("RankedEligible: %v", err)
	}
	for _, cs := range scored {
		if cs.Provider == "provider_b" {
			t.Fatalf("provider_b (breaker open) must be excluded, got %+v", cs)
		}
	}
}

// TestCandidateConstruction_MultipleModelsPerProvider confirms that multiple
// models from the same provider are all represented as distinct candidates.
func TestCandidateConstruction_MultipleModelsPerProvider(t *testing.T) {
	providerA := &multiModelTestProvider{name: "provider_a", models: []string{"model-1", "model-2"}, healthy: true}
	engine, cat := multiProviderCandidateEngine(t, providerA)

	advertiser := &fakeReachabilityFilter{advertise: map[string]bool{"provider_a": true}}
	cat.SetReachabilityFilter(advertiser, true)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	resolver := engine.AutoModelResolver()
	scored, err := resolver.RankedEligible(context.Background(), req)
	if err != nil {
		t.Fatalf("RankedEligible: %v", err)
	}
	if len(scored) == 0 {
		t.Fatal("no eligible providers in candidate pool")
	}
	// Both models from the same provider must appear as distinct candidates.
	seen := map[string]bool{}
	for _, cs := range scored {
		seen[cs.Provider+"/"+cs.ProviderID] = true
	}
	if !seen["provider_a/model-1"] || !seen["provider_a/model-2"] {
		t.Fatalf("expected both provider_a models as candidates, got scored=%+v (seen=%v)", scored, seen)
	}
}

// TestCandidateConstruction_ProviderAgnostic verifies the fix is not
// ollama- or agnes-specific by exercising the candidate pool with an
// entirely different provider pair.
func TestCandidateConstruction_ProviderAgnostic(t *testing.T) {
	providerC := &autoTestProvider{name: "deepseek", modelID: "deepseek-chat", healthy: true}
	providerD := &autoTestProvider{name: "groq", modelID: "llama-3.1-8b-instruct", healthy: true}
	engine, cat := multiProviderCandidateEngine(t, providerC, providerD)

	advertiser := &fakeReachabilityFilter{advertise: map[string]bool{"deepseek": true, "groq": true}}
	cat.SetReachabilityFilter(advertiser, true)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	resolver := engine.AutoModelResolver()
	scored, err := resolver.RankedEligible(context.Background(), req)
	if err != nil {
		t.Fatalf("RankedEligible: %v", err)
	}
	provs := map[string]bool{}
	for _, cs := range scored {
		provs[cs.Provider] = true
	}
	if !provs["deepseek"] || !provs["groq"] {
		t.Fatalf("candidate set must include both providers: %+v", scored)
	}
}

// TestCandidateConstruction_CapabilityFilteringIntact confirms the vision
// hard filter still drops non-vision providers even in a multi-provider
// candidate set.
func TestCandidateConstruction_CapabilityFilteringIntact(t *testing.T) {
	providerA := &autoTestProvider{name: "openai", modelID: "gpt-4o", healthy: true}
	providerB := &autoTestProvider{name: "groq", modelID: "llama-3.1-8b-instruct", healthy: true}
	engine, cat := multiProviderCandidateEngine(t, providerA, providerB)

	advertiser := &fakeReachabilityFilter{advertise: map[string]bool{"openai": true, "groq": true}}
	cat.SetReachabilityFilter(advertiser, true)

	req := &apitypes.ChatCompletionRequest{
		Model: "auto",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartText, Text: "describe"},
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}

	resolver := engine.AutoModelResolver()
	scored, err := resolver.RankedEligible(context.Background(), req)
	if err != nil {
		t.Fatalf("RankedEligible: %v", err)
	}
	for _, cs := range scored {
		if cs.Provider == "groq" {
			t.Fatalf("groq (no vision) must be excluded from a vision request, got %+v", cs)
		}
	}
}

// TestCandidateConstruction_NoDuplicateCandidates confirms the candidate
// pool contains no duplicate (provider, model) pairs even when a provider
// lists the same model ID twice (defensive against provider catalog
// quirks).
func TestCandidateConstruction_NoDuplicateCandidates(t *testing.T) {
	// A provider that reports the same model ID twice in ListModels.
	dupProvider := &duplicateModelTestProvider{name: "provider_x", modelID: "model-1"}
	engine, cat := multiProviderCandidateEngine(t, dupProvider)
	advertiser := &fakeReachabilityFilter{advertise: map[string]bool{"provider_x": true}}
	cat.SetReachabilityFilter(advertiser, true)

	req := &apitypes.ChatCompletionRequest{
		Model:    "auto",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	resolver := engine.AutoModelResolver()
	scored, err := resolver.RankedEligible(context.Background(), req)
	if err != nil {
		t.Fatalf("RankedEligible: %v", err)
	}
	seen := map[string]bool{}
	for _, cs := range scored {
		key := cs.Provider + "/" + cs.ProviderID
		if seen[key] {
			t.Fatalf("duplicate candidate %q in pool: %+v", key, scored)
		}
		seen[key] = true
	}
}

// duplicateModelTestProvider reports the same model ID twice.
type duplicateModelTestProvider struct {
	name      string
	modelID   string
	callCount int
}

func (p *duplicateModelTestProvider) Name() string                 { return p.name }
func (p *duplicateModelTestProvider) SupportsModel(id string) bool { return true }
func (p *duplicateModelTestProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	p.callCount++
	return &apitypes.ChatCompletionResponse{
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
	}, nil
}
func (p *duplicateModelTestProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (p *duplicateModelTestProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (p *duplicateModelTestProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	info := provider.ModelInfo{ProviderModelID: p.modelID, ModelID: p.modelID, OwnedBy: p.name}
	return []provider.ModelInfo{info, info}, nil
}
func (p *duplicateModelTestProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (p *duplicateModelTestProvider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: p.name, IsHealthy: true, LatencyMs: 10}, nil
}
func (p *duplicateModelTestProvider) GetMetadata() provider.Metadata {
	return provider.Metadata{Name: p.name, Models: []string{p.modelID}}
}

// multiModelTestProvider is a provider that exposes multiple models.
type multiModelTestProvider struct {
	name    string
	models  []string
	healthy bool
}

func (p *multiModelTestProvider) Name() string                 { return p.name }
func (p *multiModelTestProvider) SupportsModel(id string) bool { return true }
func (p *multiModelTestProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return &apitypes.ChatCompletionResponse{
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "ok"}}},
	}, nil
}
func (p *multiModelTestProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (p *multiModelTestProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (p *multiModelTestProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	out := make([]provider.ModelInfo, 0, len(p.models))
	for _, m := range p.models {
		out = append(out, provider.ModelInfo{ProviderModelID: m, ModelID: m, OwnedBy: p.name})
	}
	return out, nil
}
func (p *multiModelTestProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (p *multiModelTestProvider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: p.name, IsHealthy: p.healthy, LatencyMs: 10}, nil
}
func (p *multiModelTestProvider) GetMetadata() provider.Metadata {
	return provider.Metadata{Name: p.name, Models: p.models}
}
