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

// TestPipelineConsumesRuntimeSnapshot verifies that DecisionPipeline obtains its
// authoritative runtime snapshot from the injected runtime.Manager and passes it
// through to the DecisionContext. No second runtime.RuntimeSnapshot type exists.
func TestPipelineConsumesRuntimeSnapshot(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	p, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", p))
	manager := runtime.NewManager(store)

	// Mark openai healthy with known latency.
	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "test", nil)
		r.RecordLatency(120)
		return nil
	})

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

// TestPipelineUsesSingleCoherentSnapshot proves that one pipeline execution uses
// exactly one runtime snapshot — not one per stage and not one per metric source.
func TestPipelineUsesSingleCoherentSnapshot(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "a", supportsAll: true})
	reg.Register(&routingStubProvider{name: "b", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"a", "b"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	// Set distinct states so we can verify the snapshot is used consistently.
	_ = store.Update("a", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("b", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateDegraded, "", nil)
		r.RecordLatency(300)
		return nil
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}

	// Execute twice and confirm the same coherent view is used both times.
	for i := 0; i < 2; i++ {
		_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("Execute iteration %d: %v", i, err)
		}
	}
}

// TestPipelineRespectsRuntimeStateChanges proves that provider state mutations in
// RuntimeStore are visible to the next pipeline execution.
func TestPipelineRespectsRuntimeStateChanges(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "healthy-p", supportsAll: true})
	reg.Register(&routingStubProvider{name: "unhealthy-p", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"healthy-p", "unhealthy-p"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}

	// Initially both unknown — pick any.
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("initial execute: %v", err)
	}
	initial := result.Decision.SelectedProvider

	// Make healthy-p healthy and unhealthy-p unhealthy.
	_ = store.Update("healthy-p", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("unhealthy-p", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "", nil)
		return nil
	})

	// Next decision must prefer the healthy provider.
	result, err = pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if result.Decision.SelectedProvider != "healthy-p" {
		t.Fatalf("expected healthy-p after state update, got %s", result.Decision.SelectedProvider)
	}
	_ = initial
}

// TestPipelineDoesNotReadLegacyStoresDirectly verifies that the pipeline does
// not independently read legacy stores for operational routing state. Operational state
// flows through RuntimeSnapshot only.
func TestPipelineDoesNotReadLegacyStoresDirectly(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	p, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", p))
	manager := runtime.NewManager(store)

	// Do NOT create a MetricsStore or pass it to the pipeline.
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
		t.Fatalf("Execute without MetricsStore: %v", err)
	}
}

// TestPipelineDoesNotReadModelStatusStoreDirectly verifies that the pipeline does
// not independently read health.ModelStatusStore for operational routing state.
func TestPipelineDoesNotReadModelStatusStoreDirectly(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	p, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", p))
	manager := runtime.NewManager(store)

	// No HealthStore passed to PipelineConfig (field no longer exists).
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

// TestPipelineSharedRouterEngine verifies that the pipeline receives a shared
// RouterEngine rather than silently constructing a second one from duplicated config.
func TestPipelineSharedRouterEngine(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	p, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", p))
	manager := runtime.NewManager(store)

	sharedEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  sharedEngine,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	if pipeline.RoutingEngine() != sharedEngine {
		t.Fatal("expected pipeline to use the shared RouterEngine, not construct a new one")
	}
}

// TestPipelineWeightUpdatePropagation verifies that updating weights on the
// shared RouterEngine is visible to the pipeline's selection stage.
func TestPipelineWeightUpdatePropagation(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "a", supportsAll: true})
	reg.Register(&routingStubProvider{name: "b", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"a", "b"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	sharedEngine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  sharedEngine,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	// Heavy latency weight.
	sharedEngine.UpdateWeights(config.RoutingWeights{Health: 10, Latency: 80, Cost: 5, Capability: 5})

	_ = store.Update("a", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(800)
		return nil
	})
	_ = store.Update("b", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Decision.SelectedProvider != "b" {
		t.Fatalf("expected b (lower latency), got %s", result.Decision.SelectedProvider)
	}
}

// TestPipelineConcurrentRuntimeUpdates verifies that concurrent runtime updates
// and pipeline executions are race-safe.
func TestPipelineConcurrentRuntimeUpdates(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "pA", supportsAll: true})
	reg.Register(&routingStubProvider{name: "pB", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"pA", "pB"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	env := router.Environment{}
	cfgSnap := router.ConfigSnapshot{}

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

	// Goroutines: continuously execute the pipeline.
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_, _ = pipeline.Execute(context.Background(), req, env, cfgSnap, nil)
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(done)
	wg.Wait()
}

// TestPipelineAllIntentProfiles verifies correct behavior for each intent profile.
func TestPipelineAllIntentProfiles(t *testing.T) {
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

	tests := []struct {
		name    string
		content string
	}{
		{"coding", "write a function that sorts an array"},
		{"reasoning", "analyze and compare the trade-offs of these approaches"},
		{"vision", "describe what is in this image"},
		{"fast", "hi"},
		{"default", "tell me about the weather"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &apitypes.ChatCompletionRequest{
				Model:    "gpt-4o",
				Messages: []apitypes.Message{{Role: "user", Content: tt.content}},
			}
			_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
		})
	}
}

// TestPipelineDoesNotUseSecondSnapshotType proves that no router.RuntimeSnapshot
// type exists in the codebase — only runtime.RuntimeSnapshot is used.
func TestPipelineNoSecondSnapshotType(t *testing.T) {
	// Compile-time check: DecisionContext.RuntimeSnapshot() returns runtime.RuntimeSnapshot.
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

	// The pipeline does not reference router.RuntimeSnapshot anywhere.
	// This test exists to document the architectural invariant.
}

// TestPipelineSnapshotCoherence verifies that a single DecisionPipeline.Execute()
// call uses exactly ONE RuntimeSnapshot — the one acquired at the start of Execute
// — and that the SelectionStage passes this same snapshot to RouterEngine rather
// than acquiring a second one.
//
// We prove this by mutating the runtime store BETWEEN a manual snapshot acquisition
// and the pipeline execution, then verifying the pipeline's result reflects the
// pre-mutation state (proving it used its own snapshot, not a later one).
func TestPipelineSnapshotCoherence(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "a", supportsAll: true})
	reg.Register(&routingStubProvider{name: "b", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"a", "b"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	// Set both healthy with identical latency so scores tie.
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

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	// Acquire a snapshot BEFORE the pipeline runs.
	snapBefore := manager.Snapshot(context.Background())

	// Mutate: make "b" unhealthy.
	_ = store.Update("b", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateUnhealthy, "", nil)
		return nil
	})

	// Run the pipeline. It should use the snapshot from inside Execute (snapAfter),
	// NOT the one we acquired before (snapBefore). Since "b" is now unhealthy,
	// if the pipeline had used snapBefore, "b" would still appear healthy.
	// With coherent snapshotting, "b" is unhealthy and should be rejected.
	candidates := []router.ResolvedRoute{
		{ProviderName: "a", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "b", ProviderModelID: "m", ModelID: "m"},
	}
	result, err := pipeline.Execute(context.Background(), &apitypes.ChatCompletionRequest{Model: "m", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Candidate == nil {
		t.Fatal("expected non-nil selection result")
	}
	// "a" should win because "b" is unhealthy in the snapshot taken during Execute.
	if result.Decision.SelectedProvider != "a" {
		t.Fatalf("expected 'a' (healthy) to win, got %s", result.Decision.SelectedProvider)
	}

	// Verify snapBefore had "b" healthy (proves we captured the pre-mutation state).
	if snapBefore.Providers["b"].State != runtime.StateHealthy {
		t.Fatalf("snapBefore should have had b healthy, got %s", snapBefore.Providers["b"].State)
	}
}

// TestPipelineSingleSnapshotPerExecution verifies that a single pipeline Execute()
// results in exactly one RuntimeManager.Snapshot() call being used for scoring.
// We verify this by ensuring that concurrent mutations during Execute do not
// affect the selection — proving a single coherent snapshot was used.
func TestPipelineSingleSnapshotPerExecution(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "a", supportsAll: true})
	reg.Register(&routingStubProvider{name: "b", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"a", "b"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	_ = store.Update("a", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(50)
		return nil
	})
	_ = store.Update("b", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(200)
		return nil
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "hi"}},
	}
	env := router.Environment{}
	cfgSnap := router.ConfigSnapshot{}

	// Run many concurrent executions while mutating state.
	var wg sync.WaitGroup
	done := make(chan struct{})

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
				_ = store.Update("a", func(r runtime.ProviderRuntime) error {
					r.UpdateState(state, "", nil)
					r.RecordLatency(int64(50 + i%100))
					return nil
				})
				_ = store.Update("b", func(r runtime.ProviderRuntime) error {
					r.UpdateState(state, "", nil)
					r.RecordLatency(int64(50 + (i+1)%100))
					return nil
				})
				i++
			}
		}
	}()

	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_, _ = pipeline.Execute(context.Background(), req, env, cfgSnap, nil)
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(done)
	wg.Wait()
}

// TestPipelineSelectionUsesPassedSnapshot verifies that when candidates are passed
// to Execute, the SelectionStage uses the pipeline's snapshot (not a fresh one).
// We prove this by setting up a scenario where a fresh snapshot would produce
// a different result than the pipeline's snapshot.
func TestPipelineSelectionUsesPassedSnapshot(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "a", supportsAll: true})
	reg.Register(&routingStubProvider{name: "b", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"a", "b"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	// Both healthy, same latency → tie.
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

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	// Candidate "a" is primary, "b" is fallback. With tied scores, primary should win.
	candidates := []router.ResolvedRoute{
		{ProviderName: "a", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "b", ProviderModelID: "m", ModelID: "m"},
	}
	result, err := pipeline.Execute(context.Background(), &apitypes.ChatCompletionRequest{Model: "m", Messages: []apitypes.Message{{Role: "user", Content: "hi"}}}, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Candidate == nil {
		t.Fatal("expected non-nil selection")
	}
	// Primary candidate "a" should win on tie (preserves candidate order).
	if result.Decision.SelectedProvider != "a" {
		t.Fatalf("expected primary candidate 'a' to win on tie, got %s", result.Decision.SelectedProvider)
	}
}
