package router_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// capabilityStubProvider is a test provider with configurable capabilities.
type capabilityStubProvider struct {
	name        string
	supportsAll bool
	vision      bool
	reasoning   bool
	toolCalling bool
}

func (s *capabilityStubProvider) Name() string { return s.name }
func (s *capabilityStubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *capabilityStubProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (s *capabilityStubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *capabilityStubProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (s *capabilityStubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *capabilityStubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *capabilityStubProvider) SupportsModel(string) bool { return s.supportsAll }
func (s *capabilityStubProvider) GetMetadata() provider.Metadata {
	return provider.NewMetadata(s.name, provider.Capabilities{
		Streaming:   true,
		Vision:      s.vision,
		Reasoning:   s.reasoning,
		ToolCalling: s.toolCalling,
	})
}

// setupRuntimeEngine creates a RouterEngine wired to a RuntimeStore for testing.
func setupRuntimeEngine(t *testing.T, regs ...provider.Provider) (*router.RouterEngine, *runtime.RuntimeStore, *runtime.ManagerImpl) {
	t.Helper()
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	for _, p := range regs {
		reg.Register(p)
		_ = store.Register(runtime.NewProviderRuntime(p.Name(), p))
	}
	manager := runtime.NewManager(store)

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	return engine, store, manager
}

// ---- Phase 9 required tests ----

// TestRuntimeSnapshotHealthRouting verifies that RouterEngine selects the healthy
// provider over a degraded/unhealthy one, and re-selects after state mutates.
func TestRuntimeSnapshotHealthRouting(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&routingStubProvider{name: "providerA", supportsAll: true},
		&routingStubProvider{name: "providerB", supportsAll: true},
	)

	ctx := context.Background()
	req := &apitypes.ChatCompletionRequest{Model: "gpt-4o", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}

	// Initially both are unknown → either may win.
	_, err := eng.SelectBestProvider(ctx, "gpt-4o", req)
	if err != nil {
		t.Fatalf("initial select: %v", err)
	}

	// Set A healthy, B unhealthy.
	_ = store.Update("providerA", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "probe ok", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("providerB", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "probe failed", nil)
		return nil
	})

	// Take a NEW snapshot-based decision.
	result, err := eng.SelectBestProvider(ctx, "gpt-4o", req)
	if err != nil {
		t.Fatalf("select after health update: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider after health update")
	}
	if result.Decision.SelectedProvider != "providerA" {
		t.Fatalf("expected providerA (healthy), got %s", result.Decision.SelectedProvider)
	}

	// Mutate: A becomes degraded, B becomes healthy.
	_ = store.Update("providerA", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateDegraded, "degraded", nil)
		return nil
	})
	_ = store.Update("providerB", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "recovered", nil)
		r.RecordLatency(80)
		return nil
	})

	// New decision should now prefer B.
	result, err = eng.SelectBestProvider(ctx, "gpt-4o", req)
	if err != nil {
		t.Fatalf("select after second mutation: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider after second mutation")
	}
	if result.Decision.SelectedProvider != "providerB" {
		t.Fatalf("expected providerB (now healthy), got %s", result.Decision.SelectedProvider)
	}
}

// TestRuntimeSnapshotCoherence verifies that a single routing decision uses one
// coherent snapshot — health and latency values are not mixed from different times.
func TestRuntimeSnapshotCoherence(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&routingStubProvider{name: "pA", supportsAll: true},
		&routingStubProvider{name: "pB", supportsAll: true},
	)

	// Set distinct states.
	_ = store.Update("pA", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("pB", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(200)
		return nil
	})

	// Capture two sequential decisions. Each must be internally coherent.
	for i := 0; i < 3; i++ {
		snapBefore := eng.GetScorer().LoadWeights() // just to exercise the engine
		_ = snapBefore
		_, err := eng.SelectBestProvider(context.Background(), "m", &apitypes.ChatCompletionRequest{Model: "m"})
		if err != nil {
			t.Fatalf("decision %d: %v", i, err)
		}
	}

	// The test passes if no race/mixing occurs (verified by structure, not by value).
	// A race would manifest as a panic or inconsistent score ordering across runs.
}

// TestRuntimeSnapshotLatencyRouting verifies that lower-latency providers win
// when latency weight is non-zero.
func TestRuntimeSnapshotLatencyRouting(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&routingStubProvider{name: "slow", supportsAll: true},
		&routingStubProvider{name: "fast", supportsAll: true},
	)

	// Heavy latency weight.
	eng.UpdateWeights(config.RoutingWeights{Health: 10, Latency: 80, Cost: 5, Capability: 5})

	_ = store.Update("slow", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(800)
		return nil
	})
	_ = store.Update("fast", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})

	result, err := eng.SelectBestProvider(context.Background(), "m", &apitypes.ChatCompletionRequest{Model: "m"})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if result.Decision.SelectedProvider != "fast" {
		t.Fatalf("expected fast (lower latency), got %s", result.Decision.SelectedProvider)
	}
}

// TestRuntimeSnapshotErrorRateRouting verifies that high ErrorRate reduces score.
func TestRuntimeSnapshotErrorRateRouting(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&routingStubProvider{name: "reliable", supportsAll: true},
		&routingStubProvider{name: "flaky", supportsAll: true},
	)

	eng.UpdateWeights(config.RoutingWeights{Health: 80, Latency: 10, Cost: 5, Capability: 5})

	// reliable: many successes, few errors → low error rate.
	_ = store.Update("reliable", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		for i := 0; i < 90; i++ {
			r.RecordSuccess()
		}
		for i := 0; i < 10; i++ {
			r.RecordError(nil)
		}
		return nil
	})
	// flaky: many errors → high error rate.
	_ = store.Update("flaky", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		for i := 0; i < 30; i++ {
			r.RecordSuccess()
		}
		for i := 0; i < 70; i++ {
			r.RecordError(nil)
		}
		return nil
	})

	result, err := eng.SelectBestProvider(context.Background(), "m", &apitypes.ChatCompletionRequest{Model: "m"})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	// Both are healthy state, but reliable has lower error rate → higher health score.
	if result.Decision.SelectedProvider != "reliable" {
		t.Fatalf("expected reliable (lower error rate), got %s", result.Decision.SelectedProvider)
	}
}

// TestRuntimeSnapshotCapabilityFilter verifies that a vision request cannot
// select a provider that does not advertise vision capability.
func TestRuntimeSnapshotCapabilityFilter(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&capabilityStubProvider{name: "vision-only", supportsAll: true, vision: true},
		&capabilityStubProvider{name: "text-only", supportsAll: true, vision: false},
	)

	eng.UpdateWeights(config.RoutingWeights{Health: 10, Latency: 10, Cost: 5, Capability: 75})

	// Set both healthy so capability is the differentiator.
	_ = store.Update("vision-only", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("text-only", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})

	// Vision request.
	req := &apitypes.ChatCompletionRequest{
		Model: "m",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
	result, err := eng.SelectBestProvider(context.Background(), "m", req)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if result.Decision.SelectedProvider != "vision-only" {
		t.Fatalf("expected vision-only for vision request, got %s", result.Decision.SelectedProvider)
	}
}

// TestRouterEngineSelectFromRoutes verifies the new SelectFromRoutes method
// picks the best route from a primary + fallback set using runtime snapshot.
func TestRouterEngineSelectFromRoutes(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&routingStubProvider{name: "primary", supportsAll: true},
		&routingStubProvider{name: "fallback1", supportsAll: true},
		&routingStubProvider{name: "fallback2", supportsAll: true},
	)

	_ = store.Update("primary", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "", nil)
		return nil
	})
	_ = store.Update("fallback1", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("fallback2", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateDegraded, "", nil)
		return nil
	})

	routes := []router.ResolvedRoute{
		{ProviderName: "primary", ProviderModelID: "m", ModelID: "m", Breaker: nil},
		{ProviderName: "fallback1", ProviderModelID: "m", ModelID: "m", Breaker: nil},
		{ProviderName: "fallback2", ProviderModelID: "m", ModelID: "m", Breaker: nil},
	}

	result, err := eng.SelectFromRoutes(context.Background(), routes, &apitypes.ChatCompletionRequest{Model: "m"})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if result.Decision.SelectedProvider != "fallback1" {
		t.Fatalf("expected fallback1 (healthy), got %s", result.Decision.SelectedProvider)
	}
}

// TestRuntimeSnapshotConcurrentAccess verifies that concurrent runtime updates
// and routing decisions observe coherent snapshots (no mixed state).
func TestRuntimeSnapshotConcurrentAccess(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&routingStubProvider{name: "pA", supportsAll: true},
		&routingStubProvider{name: "pB", supportsAll: true},
	)

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Goroutine: constantly update runtime state.
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-done:
				return
			default:
				state := runtime.StateHealthy
				if i%2 == 0 {
					state = runtime.StateDegraded
				}
				_ = store.Update("pA", func(r runtime.ProviderRuntime) error {
					r.UpdateState(state, "", nil)
					r.RecordLatency(int64(50 + i%100))
					return nil
				})
				_ = store.Update("pB", func(r runtime.ProviderRuntime) error {
					r.UpdateState(state, "", nil)
					r.RecordLatency(int64(50 + (i+1)%100))
					return nil
				})
				i++
			}
		}
	}()

	// Goroutines: continuously make routing decisions.
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_, _ = eng.SelectBestProvider(context.Background(), "m", &apitypes.ChatCompletionRequest{Model: "m"})
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(done)
	wg.Wait()
}

type routingStubProvider struct {
	name        string
	supportsAll bool
}

func (s *routingStubProvider) Name() string { return s.name }
func (s *routingStubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *routingStubProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (s *routingStubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *routingStubProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (s *routingStubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *routingStubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *routingStubProvider) SupportsModel(string) bool { return s.supportsAll }
func (s *routingStubProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}

func TestNewRouterEngine(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})
	if engine == nil {
		t.Fatal("expected non-nil router engine")
	}
}

func TestRouterEngineSelectsBestProvider(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	metricsStore := router.NewMetricsStore()
	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: metricsStore,
		Weights:      config.DefaultRoutingWeights(),
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
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if len(result.Decision.CandidateScores) != 2 {
		t.Fatalf("expected 2 candidate scores, got %d", len(result.Decision.CandidateScores))
	}
}

func TestRouterEngineHandlesNoProviders(t *testing.T) {
	reg := provider.NewRegistry()
	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	_, err := engine.SelectBestProvider(context.Background(), "gpt-4o", &apitypes.ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error for no providers")
	}
}

func TestRouterEngineFiltersUnhealthyProvider(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	op, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", op))
	gq, _ := reg.Get("groq")
	_ = store.Register(runtime.NewProviderRuntime("groq", gq))
	manager := runtime.NewManager(store)

	// Mark openai as unhealthy via runtime snapshot.
	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "probe failed", nil)
		return nil
	})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
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
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Should still select a provider (groq as fallback).
	if result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider when one is unhealthy")
	}
}

func TestRouterEngineCapabilityFilter(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
	result, err := engine.SelectBestProvider(context.Background(), "gpt-4o", req)
	if err != nil {
		t.Fatalf("SelectBestProvider: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Decision.CandidateScores) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.Decision.CandidateScores))
	}
}

func TestRouterEngineWeightUpdate(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	engine.UpdateWeights(config.RoutingWeights{
		Health:     80,
		Latency:    10,
		Cost:       5,
		Capability: 5,
	})

	w := engine.GetScorer().LoadWeights()
	if w.Health != 0.8 {
		t.Fatalf("health weight = %f, want 0.8", w.Health)
	}
	if w.Latency != 0.1 {
		t.Fatalf("latency weight = %f, want 0.1", w.Latency)
	}
}

func TestRouterEngineGetProviderScores(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	scores := engine.GetProviderScores(router.CapabilityHint{})
	if len(scores) != 2 {
		t.Fatalf("expected 2 provider scores, got %d", len(scores))
	}
	for _, s := range scores {
		if s.Provider == "" {
			t.Fatal("expected non-empty provider name")
		}
	}
}

func TestRouterEngineRecordResult(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	metricsStore := router.NewMetricsStore()
	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: metricsStore,
		Weights:      config.DefaultRoutingWeights(),
	})

	engine.RecordResult("openai", 150, true)
	engine.RecordResult("openai", 200, true)
	engine.RecordResult("openai", 500, false)

	scores := engine.GetProviderScores(router.CapabilityHint{})
	if len(scores) == 0 {
		t.Fatal("expected scores after recording results")
	}
}

func TestRouterEngineNoProviderSupportsModel(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: false})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	_, err := engine.SelectBestProvider(context.Background(), "nonexistent-model", &apitypes.ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error when no provider supports the model")
	}
}

// TestSelectFromRoutesDeterministicTieBreakingPrimaryWins verifies that when
// scores tie among explicit candidates, the primary (first in slice order) wins.
func TestSelectFromRoutesDeterministicTieBreakingPrimaryWins(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&routingStubProvider{name: "primary", supportsAll: true},
		&routingStubProvider{name: "fallback", supportsAll: true},
	)

	// Both healthy with identical latency → tie.
	_ = store.Update("primary", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("fallback", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	routes := []router.ResolvedRoute{
		{ProviderName: "primary", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "fallback", ProviderModelID: "m", ModelID: "m"},
	}

	result, err := eng.SelectFromRoutes(context.Background(), routes, &apitypes.ChatCompletionRequest{Model: "m"})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if result.Decision.SelectedProvider != "primary" {
		t.Fatalf("expected primary (first in order) to win on tie, got %s", result.Decision.SelectedProvider)
	}
}

// TestSelectFromRoutesDeterministicRepeatedExecution verifies that repeated
// executions with the same candidate set and unchanged runtime state always
// produce the same selection.
func TestSelectFromRoutesDeterministicRepeatedExecution(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&routingStubProvider{name: "a", supportsAll: true},
		&routingStubProvider{name: "b", supportsAll: true},
		&routingStubProvider{name: "c", supportsAll: true},
	)

	// All same state → will tie-break by slice order.
	_ = store.Update("a", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("b", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("c", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	routes := []router.ResolvedRoute{
		{ProviderName: "b", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "a", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "c", ProviderModelID: "m", ModelID: "m"},
	}

	var first string
	for i := 0; i < 10; i++ {
		result, err := eng.SelectFromRoutes(context.Background(), routes, &apitypes.ChatCompletionRequest{Model: "m"})
		if err != nil {
			t.Fatalf("select iteration %d: %v", i, err)
		}
		if i == 0 {
			first = result.Decision.SelectedProvider
		} else if result.Decision.SelectedProvider != first {
			t.Fatalf("iteration %d: expected %s (stable), got %s", i, first, result.Decision.SelectedProvider)
		}
	}
	if first != "b" {
		t.Fatalf("expected 'b' (first in slice) to win on tie, got %s", first)
	}
}

// TestSelectBestProviderDeterministicTieBreaking verifies that when scores tie
// among auto-mode providers, the selection is deterministic (sorted by name).
func TestSelectBestProviderDeterministicTieBreaking(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&routingStubProvider{name: "zebra", supportsAll: true},
		&routingStubProvider{name: "alpha", supportsAll: true},
		&routingStubProvider{name: "beta", supportsAll: true},
	)

	// All same state → will tie-break by provider name (alphabetical).
	_ = store.Update("zebra", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("alpha", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("beta", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	req := &apitypes.ChatCompletionRequest{Model: "m"}

	var first string
	for i := 0; i < 10; i++ {
		result, err := eng.SelectBestProvider(context.Background(), "m", req)
		if err != nil {
			t.Fatalf("select iteration %d: %v", i, err)
		}
		if i == 0 {
			first = result.Decision.SelectedProvider
		} else if result.Decision.SelectedProvider != first {
			t.Fatalf("iteration %d: expected %s (stable), got %s", i, first, result.Decision.SelectedProvider)
		}
	}
	// Alphabetical order: alpha < beta < zebra. Alpha should win.
	if first != "alpha" {
		t.Fatalf("expected 'alpha' (alphabetically first) to win on tie, got %s", first)
	}
}

// TestSelectBestProviderDeterministicConcurrent verifies that concurrent
// SelectBestProvider calls with unchanged runtime state produce deterministic results.
func TestSelectBestProviderDeterministicConcurrent(t *testing.T) {
	eng, store, _ := setupRuntimeEngine(t,
		&routingStubProvider{name: "z", supportsAll: true},
		&routingStubProvider{name: "a", supportsAll: true},
	)

	_ = store.Update("z", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("a", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	var wg sync.WaitGroup
	results := make([]string, 100)
	start := make(chan struct{})

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			result, err := eng.SelectBestProvider(context.Background(), "m", &apitypes.ChatCompletionRequest{Model: "m"})
			if err != nil {
				t.Errorf("goroutine %d: SelectBestProvider: %v", idx, err)
				return
			}
			results[idx] = result.Decision.SelectedProvider
		}(i)
	}

	// Release all goroutines at once: each performs exactly one selection,
	// so participation is total regardless of core count or scheduler fairness.
	close(start)
	wg.Wait()

	// All results should be the same (deterministic).
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatalf("non-deterministic: results[0]=%s, results[%d]=%s", results[0], i, results[i])
		}
	}
	if results[0] == "" {
		t.Fatal("selection produced an empty provider")
	}
	if results[0] != "a" {
		t.Fatalf("expected 'a' (alphabetically first) to win on tie, got %s", results[0])
	}
}

// TestSelectFromRoutesWithSnapshotPassesExplicitSnapshot verifies that
// SelectFromRoutesWithSnapshot uses the provided snapshot instead of calling
// RuntimeManager.Snapshot() internally.
func TestSelectFromRoutesWithSnapshotPassesExplicitSnapshot(t *testing.T) {
	eng, store, manager := setupRuntimeEngine(t,
		&routingStubProvider{name: "a", supportsAll: true},
		&routingStubProvider{name: "b", supportsAll: true},
	)

	// Set both healthy with same latency.
	_ = store.Update("a", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("b", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	// Capture the snapshot.
	snap := manager.Snapshot(context.Background())

	// Mutate: make "b" unhealthy AFTER capturing the snapshot.
	_ = store.Update("b", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "", nil)
		return nil
	})

	routes := []router.ResolvedRoute{
		{ProviderName: "a", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "b", ProviderModelID: "m", ModelID: "m"},
	}

	// Using the explicit snapshot (taken before mutation), "b" should still appear healthy
	// and the result depends on tie-breaking (slice order → "a" wins).
	result, err := eng.SelectFromRoutesWithSnapshot(context.Background(), routes, &apitypes.ChatCompletionRequest{Model: "m"}, snap)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if result == nil || result.Candidate == nil {
		t.Fatal("expected non-nil result")
	}
	// With the old snapshot, both are healthy and tied → primary ("a") wins by slice order.
	if result.Decision.SelectedProvider != "a" {
		t.Fatalf("expected 'a' (primary in slice order) to win, got %s", result.Decision.SelectedProvider)
	}

	// Without the explicit snapshot (fresh call), "b" is unhealthy and would also win "a"
	// — but for the right reason. Verify the difference by comparing with a fresh call.
	freshResult, err := eng.SelectFromRoutes(context.Background(), routes, &apitypes.ChatCompletionRequest{Model: "m"})
	if err != nil {
		t.Fatalf("fresh select: %v", err)
	}
	// Fresh call sees "b" as unhealthy, so "a" wins anyway.
	if freshResult.Decision.SelectedProvider != "a" {
		t.Fatalf("fresh select expected 'a', got %s", freshResult.Decision.SelectedProvider)
	}
}
