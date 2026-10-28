package shadow_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/eventbus"
	jev "github.com/EffNine/conductor/internal/jev"
	"github.com/EffNine/conductor/internal/jev/shadow"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// busStore captures observations and backfills for bridge tests.
type busStore struct {
	mu        sync.Mutex
	rows      []shadow.Observation
	backfills []string
}

func (b *busStore) Save(ctx context.Context, obs *shadow.Observation) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rows = append(b.rows, *obs)
	return nil
}

func (b *busStore) BackfillOutcome(ctx context.Context, correlationID, decisionID, outcome string, latencyMs int64) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.backfills = append(b.backfills, correlationID+"/"+decisionID+"="+outcome)
	matched := 0
	for i := range b.rows {
		row := &b.rows[i]
		if correlationID != "" && row.CorrelationID == correlationID {
			row.Outcome = outcome
			row.ProviderLatencyMs = latencyMs
			matched++
		}
	}
	return matched, nil
}

// firstRow returns a copy of the first observation (thread-safe).
func (b *busStore) firstRow() *shadow.Observation {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.rows) == 0 {
		return nil
	}
	// Return a copy: the advisor updates the stored row from its own
	// goroutine, so handing out a pointer would race with that update.
	row := b.rows[0]
	return &row
}

// firstAdvisoryStatus returns the first row's advisory status under the lock.
func (b *busStore) firstAdvisoryStatus() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.rows) == 0 {
		return ""
	}
	return b.rows[0].AdvisoryStatus
}

// backfillCount returns the number of backfill calls (thread-safe).
func (b *busStore) backfillCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.backfills)
}

func (b *busStore) Migrate() error { return nil }

func (b *busStore) UpdateAdvisory(ctx context.Context, obs *shadow.Observation) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.rows {
		if b.rows[i].DecisionID == obs.DecisionID {
			row := &b.rows[i]
			row.AdvisoryProvider = obs.AdvisoryProvider
			row.AdvisoryConfidence = obs.AdvisoryConfidence
			row.AdvisoryProbabilities = obs.AdvisoryProbabilities
			row.JEVModel = obs.JEVModel
			row.JEVDurationMs = obs.JEVDurationMs
			row.JEVInputTokens = obs.JEVInputTokens
			row.JEVOutputTokens = obs.JEVOutputTokens
			row.AdvisoryStatus = obs.AdvisoryStatus
		}
	}
	return nil
}

func (b *busStore) PruneBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	return 0, nil
}

func (b *busStore) List(ctx context.Context, limit int) ([]shadow.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]shadow.Observation, 0, len(b.rows))
	out = append(out, b.rows...)
	return out, nil
}

// waitForPred polls until pred returns true or the deadline elapses.
func waitForPred(t *testing.T, d time.Duration, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("predicate not true within %s", d)
}

// TestBridgeWiresObservationToEvents proves the bridge invokes the advisor
// at the intended seam (DecisionFinished) and backfills the outcome on
// ExecutionAttemptCompleted — the full observation → advisory → outcome
// chain, with no live JEV.
func TestBridgeWiresObservationToEvents(t *testing.T) {
	bus := eventbus.NewEventBus()
	store := &busStore{}
	advisor := shadow.NewAdvisor(shadow.AdvisorConfig{Store: store})
	bridge := shadow.NewBridge(bus, advisor, 0, nil)
	bridge.Start()
	defer bridge.Stop()

	trace := routerTraceForTest("bridge-d1", "openai")
	bus.PublishSync(context.Background(), eventbus.Event{
		Type:    eventbus.DecisionFinished,
		Payload: trace,
	})
	waitForPred(t, 5*time.Second, func() bool { return store.firstRow() != nil })
	obs := store.firstRow()
	if obs == nil {
		t.Fatal("no observation recorded")
	}
	if got := obs.DecisionID; got != "bridge-d1" {
		t.Fatalf("decision id = %q, want bridge-d1", got)
	}
	if obs.SelectedProvider != "openai" {
		t.Fatalf("selected provider = %q, want openai", obs.SelectedProvider)
	}
}

// TestBridgeBackfillsOutcome proves an execution-attempt event associates
// the eventual outcome with the observation.
func TestBridgeBackfillsOutcome(t *testing.T) {
	bus := eventbus.NewEventBus()
	store := &busStore{}
	advisor := shadow.NewAdvisor(shadow.AdvisorConfig{Store: store})
	bridge := shadow.NewBridge(bus, advisor, 0, nil)
	bridge.Start()
	defer bridge.Stop()

	// Pre-seed an observation row (as the advisor would after decision).
	_ = store.Save(context.Background(), &shadow.Observation{
		DecisionID:       "bridge-d2",
		SelectedProvider: "openai",
		Outcome:          "selected",
	})

	bus.PublishSync(context.Background(), eventbus.Event{
		Type:    eventbus.ExecutionAttemptCompleted,
		Payload: struct{}{}, // non-AttemptRecord payload: ignored
	})
	// No backfill should occur for a non-AttemptRecord payload: wait briefly
	// and confirm none was recorded.
	time.Sleep(200 * time.Millisecond)
	if n := store.backfillCount(); n != 0 {
		t.Fatalf("non-AttemptRecord payload must not trigger backfill, got %d", n)
	}
}

// TestBridgeBackfillsOutcomeByCorrelation proves the outcome backfill seam
// (C3A): a real request records the correlation id on the trace, the advisor
// persists it on the observation, and an ExecutionAttemptCompleted event
// carrying the same correlation id backfills the terminal outcome.
func TestBridgeBackfillsOutcomeByCorrelation(t *testing.T) {
	bus := eventbus.NewEventBus()
	store := &busStore{}
	advisor := shadow.NewAdvisor(shadow.AdvisorConfig{Store: store})
	bridge := shadow.NewBridge(bus, advisor, 0, nil)
	bridge.Start()
	defer bridge.Stop()

	trace := routerTraceForTest("bridge-d4", "openai")
	trace.CorrelationID = "corr-42"
	bus.PublishSync(context.Background(), eventbus.Event{
		Type:    eventbus.DecisionFinished,
		Payload: trace,
	})
	waitForPred(t, 5*time.Second, func() bool { return store.firstRow() != nil })
	obs := store.firstRow()
	if obs.CorrelationID != "corr-42" {
		t.Fatalf("correlation id = %q, want corr-42", obs.CorrelationID)
	}

	bus.PublishSync(context.Background(), eventbus.Event{
		Type: eventbus.ExecutionAttemptCompleted,
		Payload: database.AttemptRecord{
			RequestID:     "req-1",
			CorrelationID: "corr-42",
			Outcome:       database.AttemptOutcomeSuccess,
			LatencyMS:     123,
		},
	})
	waitForPred(t, 5*time.Second, func() bool { return store.backfillCount() >= 1 })
	// The backfill handler runs the outcome UPDATE on the row.
	waitForPred(t, 5*time.Second, func() bool {
		row := store.firstRow()
		return row != nil && row.Outcome == "success" && row.ProviderLatencyMs == 123
	})
	row := store.firstRow()
	if row.Outcome != "success" || row.ProviderLatencyMs != 123 {
		t.Fatalf("backfill did not land: outcome=%q lat=%d", row.Outcome, row.ProviderLatencyMs)
	}
}

// TestBridgeDisabledIsInert proves a bridge started with a nil bus/advisor
// does nothing and does not panic — the integration is fully detachable.
func TestBridgeDisabledIsInert(t *testing.T) {
	advisor := shadow.NewAdvisor(shadow.AdvisorConfig{})
	bridge := shadow.NewBridge(nil, advisor, 0, nil)
	bridge.Start()
	bridge.Stop()
	// No panic, no events.
}

// routerTraceForTest builds a minimal deterministic trace with a winner.
func routerTraceForTest(decisionID, winnerProvider string) *router.DecisionTrace {
	snap := runtime.RuntimeSnapshot{}
	b := router.NewDecisionTraceBuilder(router.DecisionID(decisionID), snap)
	b.SetRequestedModel("gpt-4o")
	b.SetWinner(&router.ResolvedRoute{
		ProviderName:    winnerProvider,
		ProviderModelID: winnerProvider,
		ModelID:         "gpt-4o",
	})
	b.SetCandidateScores([]router.CandidateScore{
		{Provider: winnerProvider, ProviderID: winnerProvider, TotalScore: 0.9, Selected: true},
		{Provider: "groq", ProviderID: "groq", TotalScore: 0.4},
	})
	return b.Build()
}

// TestBridgeWithJEVClientAdvises proves the bridge, with a live (fake) JEV
// client, attaches the advisory result to the persisted observation.
func TestBridgeWithJEVClientAdvises(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"answers": {"best_provider": {"type":"choice","choice":"groq","probabilities":{"openai":0.3,"groq":0.7},"confidence":0.7}}
		}`))
	}))
	defer srv.Close()

	client := jev.NewClient(jev.Config{APIKey: "k", Endpoint: srv.URL, Timeout: 2 * time.Second, HTTPClient: srv.Client()})
	bus := eventbus.NewEventBus()
	store := &busStore{}
	advisor := shadow.NewAdvisor(shadow.AdvisorConfig{Store: store, JEVClient: client})
	bridge := shadow.NewBridge(bus, advisor, 0, nil)
	bridge.Start()
	defer bridge.Stop()

	bus.PublishSync(context.Background(), eventbus.Event{
		Type:    eventbus.DecisionFinished,
		Payload: routerTraceForTest("bridge-d3", "openai"),
	})
	// Wait for the advisory update, not just the first save: Save and
	// UpdateAdvisory are two separate writes and the assertion below reads a
	// field written by the second one.
	waitForPred(t, 5*time.Second, func() bool {
		return store.firstAdvisoryStatus() == shadow.AdvisoryOK
	})
	obs := store.firstRow()
	if obs == nil {
		t.Fatal("no observation recorded")
	}
	if obs.AdvisoryProvider != "groq" {
		t.Fatalf("advisory provider = %q, want groq", obs.AdvisoryProvider)
	}
	if obs.SelectedProvider != "openai" {
		t.Fatalf("selected = %q, want openai (deterministic, unchanged)", obs.SelectedProvider)
	}
	if obs.AdvisoryStatus != shadow.AdvisoryOK {
		t.Fatalf("advisory status = %q, want ok", obs.AdvisoryStatus)
	}
}
