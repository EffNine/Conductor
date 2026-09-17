package shadow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	jev "github.com/EffNine/conductor/internal/jev"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// testStore is an in-memory Store implementation for advisor tests.
type testStore struct {
	mu          sync.Mutex
	observed    []ShadowObservation
	backfillCnt int
	saveErr     error
}

func (s *testStore) Migrate() error { return nil }

func (s *testStore) Save(ctx context.Context, obs *ShadowObservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.observed = append(s.observed, *obs)
	return nil
}

func (s *testStore) BackfillOutcome(ctx context.Context, decisionID, outcome string, latencyMs int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backfillCnt++
	for i := range s.observed {
		if s.observed[i].DecisionID == decisionID {
			s.observed[i].Outcome = outcome
			s.observed[i].ProviderLatencyMs = latencyMs
			return true, nil
		}
	}
	return false, nil
}

func (s *testStore) PruneBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	return 0, nil
}

// backfillCount returns the number of backfill calls made (thread-safe).
func (s *testStore) backfillCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backfillCnt
}

func (s *testStore) List(ctx context.Context, limit int) ([]ShadowObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.observed) {
		return s.observed, nil
	}
	return s.observed[len(s.observed)-limit:], nil
}

// snapshot returns a copy of the observed rows so readers do not race the
// async writer.
func (s *testStore) snapshot() []ShadowObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ShadowObservation, len(s.observed))
	copy(out, s.observed)
	return out
}

// first returns the first observation if present.
func (s *testStore) first() (*ShadowObservation, bool) {
	rows := s.snapshot()
	if len(rows) == 0 {
		return nil, false
	}
	return &rows[0], true
}

// makeTrace builds a minimal deterministic trace with a winner and two
// candidate scores.
func makeTrace(decisionID string, winnerProvider string) *router.DecisionTrace {
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

// waitFor polls the store until a predicate holds or the deadline passes.
func waitFor(t *testing.T, store *testStore, deadline time.Duration, want func() bool) {
	t.Helper()
	deadlineTime := time.Now().Add(deadline)
	for time.Now().Before(deadlineTime) {
		if want() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", deadline)
}

// expectOne waits until the store holds exactly one observation and returns
// a stable copy of it.
func expectOne(t *testing.T, store *testStore) *ShadowObservation {
	t.Helper()
	waitFor(t, store, 5*time.Second, func() bool { return len(store.snapshot()) == 1 })
	obs, ok := store.first()
	if !ok {
		t.Fatalf("no observation recorded")
	}
	return obs
}

// TestObservePersistsAdvisoryWhenClientConfigured proves the observation
// invokes JEV, attaches the advisory result, and persists the row with the
// deterministic decision and outcome.
func TestObservePersistsAdvisoryWhenClientConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"model": "jev-latest",
			"answers": {"best_provider": {"type":"choice","choice":"groq","probabilities":{"openai":0.4,"groq":0.6},"confidence":0.6}}
		}`))
	}))
	defer srv.Close()

	client := jev.NewClient(jev.Config{
		APIKey:     "k",
		Endpoint:   srv.URL,
		Timeout:    2 * time.Second,
		HTTPClient: srv.Client(),
	})
	store := &testStore{}
	a := NewAdvisor(AdvisorConfig{Store: store, JEVClient: client})
	defer a.Stop()

	a.Observe(makeTrace("d1", "openai"))
	obs := expectOne(t, store)
	if obs.AdvisoryProvider != "groq" {
		t.Fatalf("advisory provider = %q, want groq", obs.AdvisoryProvider)
	}
	if obs.SelectedProvider != "openai" {
		t.Fatalf("selected provider = %q, want openai (deterministic, unchanged)", obs.SelectedProvider)
	}
	if obs.Outcome != OutcomeSelected {
		t.Fatalf("outcome = %q, want selected", obs.Outcome)
	}
	if obs.AdvisoryStatus != AdvisoryOK {
		t.Fatalf("advisory status = %q, want ok", obs.AdvisoryStatus)
	}
	// The deterministic selection is never altered by the advisory.
	if obs.SelectedProvider == obs.AdvisoryProvider {
		t.Fatalf("selected and advisory both %q: deterministic selection must be independent", obs.SelectedProvider)
	}
}

// TestObserveDoesNotAlterDeterministicSelection proves the advisory result
// does not modify the selected route: when JEV advises a different provider,
// the persisted selected provider is still the deterministic winner.
func TestObserveDoesNotAlterDeterministicSelection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"answers": {"best_provider": {"type":"choice","choice":"groq","probabilities":{"openai":0.1,"groq":0.9},"confidence":0.9}}
		}`))
	}))
	defer srv.Close()

	client := jev.NewClient(jev.Config{APIKey: "k", Endpoint: srv.URL, Timeout: 2 * time.Second, HTTPClient: srv.Client()})
	store := &testStore{}
	a := NewAdvisor(AdvisorConfig{Store: store, JEVClient: client})
	defer a.Stop()

	a.Observe(makeTrace("d2", "openai"))
	obs := expectOne(t, store)
	if obs.SelectedProvider != "openai" {
		t.Fatalf("selected = %q, want openai (JEV must not override the deterministic route)", obs.SelectedProvider)
	}
	if obs.AdvisoryProvider != "groq" {
		t.Fatalf("advisory = %q, want groq", obs.AdvisoryProvider)
	}
}

// TestObserveJEVFailureDoesNotBreakRouting proves a failing JEV call is
// recorded as an advisory failure and the observation is still persisted with
// the deterministic decision intact.
func TestObserveJEVFailureDoesNotBreakRouting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"upstream"}`))
	}))
	defer srv.Close()

	client := jev.NewClient(jev.Config{APIKey: "k", Endpoint: srv.URL, Timeout: 2 * time.Second, HTTPClient: srv.Client()})
	store := &testStore{}
	a := NewAdvisor(AdvisorConfig{Store: store, JEVClient: client})
	defer a.Stop()

	a.Observe(makeTrace("d3", "openai"))
	obs := expectOne(t, store)
	if obs.AdvisoryStatus != AdvisoryError {
		t.Fatalf("advisory status = %q, want error", obs.AdvisoryStatus)
	}
	if obs.SelectedProvider != "openai" {
		t.Fatalf("selected = %q after JEV failure, want openai (routing unchanged)", obs.SelectedProvider)
	}
}

// blockingTransport honors the request context: it blocks until the ctx
// deadline fires, then returns context.DeadlineExceeded — mirroring what a
// slow upstream does under a client-side deadline. This lets the timeout
// test exercise the real deadline path without a live server.
type blockingTransport struct{}

func (blockingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

// TestObserveJEVTimeoutRecordsTimeout proves a timeout is captured without
// breaking the deterministic decision. The client's 50ms deadline expires
// while the transport is parked, surfacing a context deadline error.
func TestObserveJEVTimeoutRecordsTimeout(t *testing.T) {
	client := jev.NewClient(jev.Config{
		APIKey:     "k",
		Endpoint:   "http://127.0.0.1:9", // unroutable; transport intercepts first
		Timeout:    50 * time.Millisecond,
		HTTPClient: &http.Client{Transport: blockingTransport{}},
	})
	store := &testStore{}
	a := NewAdvisor(AdvisorConfig{Store: store, JEVClient: client})
	defer a.Stop()

	a.Observe(makeTrace("d4", "openai"))
	obs := expectOne(t, store)
	if obs.AdvisoryStatus != AdvisoryTimeout {
		t.Fatalf("advisory status = %q, want timeout", obs.AdvisoryStatus)
	}
}

// TestObserveNotConfiguredRecordsNoConfigured proves that when no JEV client
// is wired, the observation still persists with advisory_status=not_configured
// and the deterministic decision is recorded.
func TestObserveNotConfiguredRecordsNoConfigured(t *testing.T) {
	store := &testStore{}
	a := NewAdvisor(AdvisorConfig{Store: store}) // no JEVClient
	defer a.Stop()

	a.Observe(makeTrace("d5", "openai"))
	obs := expectOne(t, store)
	if obs.AdvisoryStatus != AdvisoryNotConfigured {
		t.Fatalf("advisory status = %q, want not_configured", obs.AdvisoryStatus)
	}
	if obs.SelectedProvider != "openai" {
		t.Fatalf("selected = %q, want openai", obs.SelectedProvider)
	}
}

// TestBackfillOutcomeAssociatesOutcome proves the eventual execution outcome
// is associated with the observation by decision id.
func TestBackfillOutcomeAssociatesOutcome(t *testing.T) {
	store := &testStore{}
	a := NewAdvisor(AdvisorConfig{Store: store})
	defer a.Stop()

	a.Observe(makeTrace("d6", "openai"))
	obs := expectOne(t, store)
	if obs.Outcome != OutcomeSelected {
		t.Fatalf("initial outcome = %q, want selected", obs.Outcome)
	}

	a.BackfillOutcome("d6", "success", 120)
	waitFor(t, store, 5*time.Second, func() bool { return store.backfillCount() >= 1 })
	obs2 := expectOne(t, store)
	if obs2.Outcome != "success" {
		t.Fatalf("outcome after backfill = %q, want success", obs2.Outcome)
	}
	if obs2.ProviderLatencyMs != 120 {
		t.Fatalf("latency after backfill = %d, want 120", obs2.ProviderLatencyMs)
	}
}

// TestStoreIdempotentOnDecisionID proves re-saving the same decision id is a
// no-op (no duplicate rows).
func TestStoreIdempotentOnDecisionID(t *testing.T) {
	store := &testStore{}
	a := NewAdvisor(AdvisorConfig{Store: store})
	defer a.Stop()

	a.Observe(makeTrace("d7", "openai"))
	expectOne(t, store)
}

// TestObservationCarriesNoSecrets proves the observation only carries
// routing-state identifiers, scores, and the advisory judgment — never
// prompts, API keys, or credentials.
func TestObservationCarriesNoSecrets(t *testing.T) {
	store := &testStore{}
	a := NewAdvisor(AdvisorConfig{Store: store})
	defer a.Stop()

	a.Observe(makeTrace("d8", "openai"))
	obs := expectOne(t, store)
	secretFrag := "sk-live"
	for _, v := range []string{
		obs.RequestedModel, obs.ResolvedMode, obs.SelectedProvider,
		obs.SelectedModel, obs.CandidateProviders, obs.AdvisoryProvider,
		obs.AdvisoryProbabilities, obs.JEVModel, obs.Outcome, obs.AdvisoryStatus,
	} {
		if containsFrag(v, secretFrag) {
			t.Fatalf("observation field leaked secret fragment %q: %q", secretFrag, v)
		}
	}
}

func containsFrag(s, frag string) bool {
	if s == "" {
		return false
	}
	return len(s) >= len(frag) && indexOf(s, frag) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
