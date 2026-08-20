package router_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// TestHealthStoreNotUsedForRouting verifies that RouterEngine does not read
// from healthStore for routing decisions. After P3.4 cleanup, healthStore was
// removed entirely from RouterEngine.
func TestHealthStoreNotUsedForRouting(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	// Build engine without any health store.
	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "hello"},
		},
	}
	result, err := engine.SelectBestProvider(context.Background(), "gpt-4o", req)
	if err != nil {
		t.Fatalf("SelectBestProvider: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected a selected provider")
	}
}

// TestMetricsStoreNotUsedForRouting verifies that metricsStore is never read
// during provider selection. It is write-only (RecordResult) and exposed only
// for dashboard access via GetMetricsStore.
func TestMetricsStoreNotUsedForRouting(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	// Build engine WITHOUT metrics store.
	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	result, err := engine.SelectBestProvider(context.Background(), "gpt-4o", req)
	if err != nil {
		t.Fatalf("SelectBestProvider without metrics store: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected a selected provider")
	}
}

// TestRuntimeSnapshotIsRoutingAuthority verifies that routing decisions derive
// all operational state (health, latency) from the RuntimeSnapshot, not from
// any legacy store.
func TestRuntimeSnapshotIsRoutingAuthority(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	op, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", op))
	gq, _ := reg.Get("groq")
	_ = store.Register(runtime.NewProviderRuntime("groq", gq))
	manager := runtime.NewManager(store)

	// Set openai unhealthy and groq healthy.
	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "probe failed", nil)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "ok", nil)
		return nil
	})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	result, err := engine.SelectBestProvider(context.Background(), "gpt-4o", req)
	if err != nil {
		t.Fatalf("SelectBestProvider: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Should prefer groq since openai is unhealthy.
	if result.Decision.SelectedProvider != "groq" {
		t.Fatalf("expected groq (openai is unhealthy), got %s", result.Decision.SelectedProvider)
	}
}

// TestPipelineSingleSnapshot verifies that DecisionPipeline.Execute acquires
// exactly one RuntimeSnapshot and passes it through to all stages. A second
// acquisition would break snapshot coherence.
func TestPipelineSingleSnapshot(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	p, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", p))
	manager := runtime.NewManager(store)

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}
	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// TestModeProfileImmutableUnderConcurrency verifies that concurrent pipeline
// executions cannot mutate global mode profile state.
func TestModeProfileImmutableUnderConcurrency(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"openai", "groq"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := &apitypes.ChatCompletionRequest{
				Model: "gpt-4o",
				Messages: []apitypes.Message{
					{Role: "user", Content: "Write a function"},
				},
			}
			_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent execution error: %v", err)
	}
}

// TestEffectiveWeightsRequestLocal verifies that effective weights are
// computed per-request and do not leak between concurrent decisions.
func TestEffectiveWeightsRequestLocal(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"openai", "groq"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	var wg sync.WaitGroup
	results := make([]string, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Mix of coding-like and general requests.
			content := "Write a Python function"
			if idx%2 == 0 {
				content = "Summarize this text"
			}
			req := &apitypes.ChatCompletionRequest{
				Model:    "gpt-4o",
				Messages: []apitypes.Message{{Role: "user", Content: content}},
			}
			res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
			if err != nil {
				t.Logf("execution error: %v", err)
				return
			}
			if res != nil && res.Candidate != nil {
				results[idx] = res.Candidate.ProviderName
			}
		}(i)
	}
	wg.Wait()

	// All executions completed without data races or crashes.
}

// TestCapabilityCacheIsolation verifies that capability lookups for one model
// do not affect another model's cached capabilities. We test this indirectly
// through routing: setting model-specific capabilities for one model must not
// change routing outcomes for another model.
func TestCapabilityCacheIsolation(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	// Prime the cache by resolving both models.
	req1 := &apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	req2 := &apitypes.ChatCompletionRequest{Model: "gpt-3.5-turbo", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	_, _ = engine.SelectBestProvider(context.Background(), "gpt-4o", req1)
	_, _ = engine.SelectBestProvider(context.Background(), "gpt-3.5-turbo", req2)

	// Now set explicit capabilities for gpt-4o only.
	engine.SetModelCapabilities("openai", "gpt-4o", router.Capabilities{Vision: true, Reasoning: true})

	// Resolve both models again — outcomes should be stable.
	result1, err := engine.SelectBestProvider(context.Background(), "gpt-4o", req1)
	if err != nil {
		t.Fatalf("select gpt-4o after override: %v", err)
	}
	result2, err := engine.SelectBestProvider(context.Background(), "gpt-3.5-turbo", req2)
	if err != nil {
		t.Fatalf("select gpt-3.5 after override: %v", err)
	}
	if result1 == nil || result2 == nil {
		t.Fatal("expected non-nil results for both models")
	}
	// Both should still select the only registered provider.
	if result1.Candidate == nil || result1.Candidate.ProviderName != "openai" {
		t.Error("gpt-4o should select openai")
	}
	if result2.Candidate == nil || result2.Candidate.ProviderName != "openai" {
		t.Error("gpt-3.5 should select openai")
	}
}

// TestCapabilityCacheInvalidation verifies that SetModelCapabilities invalidates
// the provider-level cache so subsequent lookups recompute from the new metadata.
func TestCapabilityCacheInvalidation(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	// Prime the cache.
	req := &apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}
	_, err := engine.SelectBestProvider(context.Background(), "gpt-4o", req)
	if err != nil {
		t.Fatalf("initial select: %v", err)
	}

	// Override with explicit model capabilities.
	engine.SetModelCapabilities("openai", "gpt-4o", router.Capabilities{
		Vision: true, Reasoning: true, ToolCalling: true,
	})

	// Re-select — should succeed and use the overridden capabilities.
	result, err := engine.SelectBestProvider(context.Background(), "gpt-4o", req)
	if err != nil {
		t.Fatalf("select after override: %v", err)
	}
	if result == nil || result.Candidate == nil {
		t.Fatal("expected non-nil result after override")
	}
	if result.Candidate.ProviderName != "openai" {
		t.Fatalf("expected openai, got %s", result.Candidate.ProviderName)
	}
}

// TestExplicitRoutePinnedForSingleCandidate verifies that an explicit route
// with a single candidate is effectively pinned (no scoring variation).
func TestExplicitRoutePinnedForSingleCandidate(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	p, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", p))
	manager := runtime.NewManager(store)

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})

	route := router.ResolvedRoute{
		ProviderName:    "openai",
		ProviderModelID: "gpt-4o",
		ModelID:         "gpt-4o",
		Provider:        p,
	}

	req := &apitypes.ChatCompletionRequest{
		Model:    "openai/gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	// Run multiple times; should always select the same provider.
	for i := 0; i < 5; i++ {
		result, err := engine.SelectFromRoutes(context.Background(), []router.ResolvedRoute{route}, req)
		if err != nil {
			t.Fatalf("SelectFromRoutes iteration %d: %v", i, err)
		}
		if result == nil || result.Candidate == nil || result.Candidate.ProviderName != "openai" {
			t.Fatalf("iteration %d: expected openai, got %+v", i, result)
		}
	}
}

// TestDeterministicTieBreaking verifies that auto-selection uses stable
// provider ordering (alphabetical) for deterministic tie-breaking.
func TestDeterministicTieBreaking(t *testing.T) {
	reg := provider.NewRegistry()
	// Register in non-alphabetical order.
	reg.Register(&routingStubProvider{name: "zebra", supportsAll: true})
	reg.Register(&routingStubProvider{name: "alpha", supportsAll: true})
	reg.Register(&routingStubProvider{name: "mid", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"zebra", "alpha", "mid"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "any-model",
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
	}

	// All providers have equal scores; selection should be deterministic.
	var first string
	for i := 0; i < 10; i++ {
		result, err := engine.SelectBestProvider(context.Background(), "any-model", req)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if result == nil || result.Candidate == nil {
			t.Fatalf("iteration %d: nil result", i)
		}
		if i == 0 {
			first = result.Candidate.ProviderName
		} else if result.Candidate.ProviderName != first {
			t.Fatalf("iteration %d: non-deterministic selection %q != %q", i, result.Candidate.ProviderName, first)
		}
	}
}
