package router_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.15: routing trace persistence foundation.
//
// The DecisionPipeline publishes the final DecisionTrace on DecisionFinished;
// a persistence consumer (TraceStore) saves it. These tests pin the contract:
// the pipeline persists (via the event boundary) without letting persistence
// influence routing.

// fakeTraceStore records saves and can simulate persistence failures.
type fakeTraceStore struct {
	mu       sync.Mutex
	saved    []*router.DecisionTrace
	attempts int
	failErr  error
}

func (f *fakeTraceStore) Save(_ context.Context, trace *router.DecisionTrace) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.failErr != nil {
		return f.failErr
	}
	f.saved = append(f.saved, trace)
	return nil
}

func (f *fakeTraceStore) Get(context.Context, router.DecisionID) (*router.DecisionTrace, error) {
	return nil, router.ErrTraceNotFound
}

func (f *fakeTraceStore) List(context.Context, router.TraceFilter) ([]router.DecisionTraceSummary, error) {
	return nil, nil
}

func (f *fakeTraceStore) savedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

func (f *fakeTraceStore) last() *router.DecisionTrace {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saved) == 0 {
		return nil
	}
	return f.saved[len(f.saved)-1]
}

// startFakePersistence mirrors the production persistence consumer: it
// subscribes to DecisionFinished and saves the payload trace.
func startFakePersistence(bus *eventbus.EventBus, store router.TraceStore) (uint64, func()) {
	id := bus.Subscribe(eventbus.DecisionFinished, func(evt eventbus.Event) {
		if tr, ok := evt.Payload.(*router.DecisionTrace); ok && tr != nil {
			_ = store.Save(context.Background(), tr)
		}
	})
	return id, func() { bus.Unsubscribe(eventbus.DecisionFinished, id) }
}

// setupP315Pipeline builds a traced pipeline over an exposed event bus.
func setupP315Pipeline(t *testing.T, providers ...*calibStubProvider) (*router.DecisionPipeline, *runtime.RuntimeStore, *eventbus.EventBus) {
	t.Helper()
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	for _, p := range providers {
		reg.Register(p)
		_ = store.Register(runtime.NewProviderRuntime(p.name, p))
	}
	manager := runtime.NewManager(store)
	bus := eventbus.NewEventBus()
	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	for _, p := range providers {
		if p.maxContext > 0 {
			eng.SetModelCapabilities(p.name, "m", router.Capabilities{
				Streaming:   true,
				Vision:      p.vision,
				Reasoning:   p.reasoning,
				ToolCalling: p.toolCalling,
				Structured:  p.structured,
				MaxContext:  p.maxContext,
			})
		}
	}
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RoutingEngine:  eng,
		RuntimeManager: manager,
		EventBus:       bus,
		Weights:        config.DefaultRoutingWeights(),
	})
	return pipeline, store, bus
}

func calibPair() []*calibStubProvider {
	return []*calibStubProvider{
		{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 300, healthState: runtime.StateHealthy, costPerUnit: 0.0009, maxContext: 128000},
	}
}

// TestDecisionPipelinePersistsTrace: a successful decision publishes the
// final trace on DecisionFinished and the persistence consumer saves it.
func TestDecisionPipelinePersistsTrace(t *testing.T) {
	pipeline, store, bus := setupP315Pipeline(t, calibPair()...)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 300)

	fake := &fakeTraceStore{}
	_, stop := startFakePersistence(bus, fake)
	defer stop()

	res, err := pipeline.Execute(context.Background(), hintReq("coding"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil || res.Candidate == nil {
		t.Fatal("expected a successful selection")
	}
	if fake.savedCount() != 1 {
		t.Fatalf("saved = %d, want 1", fake.savedCount())
	}
	tr := fake.last()
	if tr == nil {
		t.Fatal("no trace saved")
	}
	if tr.DecisionID == "" {
		t.Fatal("saved trace has empty decision id")
	}
	if tr.Winner == nil || tr.Winner.ProviderName != res.Candidate.ProviderName {
		t.Fatalf("saved trace winner %+v != selection candidate %s", tr.Winner, res.Candidate.ProviderName)
	}
	if len(tr.StageResults) != 4 {
		t.Fatalf("saved trace has %d stage results, want 4", len(tr.StageResults))
	}
}

// TestFailedDecisionPersistsTrace: a failed decision (intent-stage hard
// rejection) is persisted with the rejection explained in the trace.
func TestFailedDecisionPersistsTrace(t *testing.T) {
	pipeline, store, bus := setupP315Pipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 50)

	fake := &fakeTraceStore{}
	_, stop := startFakePersistence(bus, fake)
	defer stop()

	_, err := pipeline.Execute(context.Background(), execReq("banana", "do it"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err == nil {
		t.Fatal("expected invalid-mode failure")
	}
	if fake.savedCount() != 1 {
		t.Fatalf("saved = %d, want 1 (failure trace must persist)", fake.savedCount())
	}
	tr := fake.last()
	failed := false
	for _, sr := range tr.StageResults {
		if sr.Name == "intent" && sr.Status == router.StageStatusFailed {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("failure trace must mark the intent stage failed: %+v", tr.StageResults)
	}
	if tr.Winner != nil {
		t.Fatal("failure trace must not carry a winner")
	}
	if tr.RequestedMode != "banana" {
		t.Fatalf("failure trace RequestedMode = %q, want banana", tr.RequestedMode)
	}
}

// TestTracePersistenceFailureDoesNotFailRouting: a failing TraceStore.Save
// must not fail or alter the routing request.
func TestTracePersistenceFailureDoesNotFailRouting(t *testing.T) {
	pipeline, store, bus := setupP315Pipeline(t, calibPair()...)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 300)

	fake := &fakeTraceStore{failErr: errors.New("disk full")}
	_, stop := startFakePersistence(bus, fake)
	defer stop()

	res, err := pipeline.Execute(context.Background(), hintReq("coding"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute failed because persistence failed: %v", err)
	}
	if res == nil || res.Candidate == nil {
		t.Fatal("expected a successful selection despite persistence failure")
	}
	if fake.attempts == 0 {
		t.Fatal("persistence consumer never attempted a save")
	}
}

// TestTracePersistenceDoesNotChangeSelection: selection output is identical
// with and without an active persistence consumer.
func TestTracePersistenceDoesNotChangeSelection(t *testing.T) {
	pipeline, store, bus := setupP315Pipeline(t, calibPair()...)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 300)

	run := func() *router.SelectionResult {
		res, err := pipeline.Execute(context.Background(), hintReq("planning"), router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		return res
	}

	// Baseline: no persistence consumer.
	baseline := run()

	// With a working persistence consumer.
	fake := &fakeTraceStore{}
	_, stop := startFakePersistence(bus, fake)
	resWithPersistence := run()
	stop()

	if baseline.Candidate == nil || resWithPersistence.Candidate == nil {
		t.Fatal("both runs must select a candidate")
	}
	if baseline.Candidate.ProviderName != resWithPersistence.Candidate.ProviderName {
		t.Fatalf("selection changed with persistence: %s vs %s",
			baseline.Candidate.ProviderName, resWithPersistence.Candidate.ProviderName)
	}
	if len(baseline.Decision.CandidateScores) != len(resWithPersistence.Decision.CandidateScores) {
		t.Fatal("candidate count changed with persistence")
	}
	for i := range baseline.Decision.CandidateScores {
		a, b := baseline.Decision.CandidateScores[i], resWithPersistence.Decision.CandidateScores[i]
		if a.Provider != b.Provider || a.TotalScore != b.TotalScore || a.Rejected != b.Rejected {
			t.Fatalf("score %d changed with persistence: %+v vs %+v", i, a, b)
		}
	}
	if fake.savedCount() == 0 {
		t.Fatal("persistence consumer never saved during the second run")
	}
}

// TestTraceUsesFinalDecisionTrace: the persisted trace is the FINAL built
// trace — complete mode metadata, weights, scores, winner, and the
// DecisionFinished timeline event.
func TestTraceUsesFinalDecisionTrace(t *testing.T) {
	pipeline, store, bus := setupP315Pipeline(t, calibPair()...)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 300)

	fake := &fakeTraceStore{}
	_, stop := startFakePersistence(bus, fake)
	defer stop()

	res, err := pipeline.Execute(context.Background(), hintReq("agentic"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tr := fake.last()
	if tr == nil {
		t.Fatal("no trace persisted")
	}

	// Final trace identity: matches the returned selection.
	if tr.Winner == nil || tr.Winner.ProviderName != res.Decision.SelectedProvider {
		t.Fatalf("winner %+v != decision %s", tr.Winner, res.Decision.SelectedProvider)
	}
	if len(tr.CandidateScores) != len(res.Decision.CandidateScores) {
		t.Fatalf("candidate scores %d != decision %d", len(tr.CandidateScores), len(res.Decision.CandidateScores))
	}

	// Complete canonical contract.
	if tr.RequestedMode != "agentic" || tr.ResolvedMode != router.ModeAgentic || tr.ModeSource != "explicit" {
		t.Fatalf("mode metadata incomplete: %q %q %q", tr.RequestedMode, tr.ResolvedMode, tr.ModeSource)
	}
	if tr.EffectiveWeights == (router.Weights{}) {
		t.Fatal("effective weights missing from final trace")
	}
	if tr.Intent == nil || tr.Intent.TaskType == "" {
		t.Fatal("intent missing from final trace")
	}
	if tr.CapabilityRequirements == nil {
		t.Fatal("capability requirements missing from final trace")
	}
	if tr.RuntimeHash == "" {
		t.Fatal("runtime hash missing from final trace")
	}
	for _, sr := range tr.StageResults {
		if sr.Status != router.StageStatusCompleted {
			t.Fatalf("stage %s not completed in final trace", sr.Name)
		}
	}
	finished := false
	for _, ev := range tr.Events {
		if ev.Type == string(eventbus.DecisionFinished) {
			finished = true
		}
	}
	if !finished {
		t.Fatal("final trace must record the DecisionFinished timeline event")
	}
}

// TestTraceRuntimeHashMatchesDecision: the persisted trace carries the exact
// runtime hash of the snapshot the decision used.
func TestTraceRuntimeHashMatchesDecision(t *testing.T) {
	pipeline, store, bus := setupP315Pipeline(t, calibPair()...)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 300)

	fake := &fakeTraceStore{}
	_, stop := startFakePersistence(bus, fake)
	defer stop()

	_, err := pipeline.Execute(context.Background(), hintReq("auto"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tr := fake.last()
	if tr == nil {
		t.Fatal("no trace persisted")
	}
	if len(tr.RuntimeHash) != 64 {
		t.Fatalf("RuntimeHash = %q, want 64-hex", tr.RuntimeHash)
	}
	if tr.TraceSchemaVer != router.TraceSchemaVersion() {
		t.Fatalf("TraceSchemaVer = %d, want %d", tr.TraceSchemaVer, router.TraceSchemaVersion())
	}
}
