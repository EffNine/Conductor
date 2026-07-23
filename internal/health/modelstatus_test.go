package health_test

import (
	"net/http"
	"testing"

	"github.com/EffNine/conductor/internal/health"
)

func TestModelStatusStoreDefaultThresholdIsOne(t *testing.T) {
	store := health.NewModelStatusStore(0, false) // 0 → default threshold 1
	store.MarkFilterReady()

	store.RecordFailure("nvidia_nim/meta/llama", "nvidia_nim", "meta/llama", "model not found", http.StatusNotFound)
	if store.ShouldAdvertise("nvidia_nim/meta/llama", "nvidia_nim") {
		t.Fatal("should hide after 1 definitive failure with default threshold 1")
	}
}

func TestModelStatusStoreHidesAfterThreshold(t *testing.T) {
	store := health.NewModelStatusStore(2, true)
	store.MarkFilterReady()

	if !store.ShouldAdvertise("nvidia_nim/meta/llama", "nvidia_nim") {
		t.Fatal("unprobed model should be advertised when unknown_as_reachable=true")
	}

	store.RecordFailure("nvidia_nim/meta/llama", "nvidia_nim", "meta/llama", "not found", http.StatusNotFound)
	if !store.ShouldAdvertise("nvidia_nim/meta/llama", "nvidia_nim") {
		t.Fatal("should still advertise after 1 failure with threshold 2")
	}

	store.RecordFailure("nvidia_nim/meta/llama", "nvidia_nim", "meta/llama", "not found", http.StatusNotFound)
	if store.ShouldAdvertise("nvidia_nim/meta/llama", "nvidia_nim") {
		t.Fatal("should hide after reaching unhealthy threshold")
	}

	st := store.Get("nvidia_nim/meta/llama")
	if st == nil || st.Reachable {
		t.Fatalf("expected unreachable status, got %+v", st)
	}
	if st.ConsecutiveFails != 2 {
		t.Fatalf("ConsecutiveFails = %d, want 2", st.ConsecutiveFails)
	}
}

func TestModelStatusStoreSuccessResetsFailures(t *testing.T) {
	store := health.NewModelStatusStore(2, true)
	store.RecordFailure("nvidia_nim/a", "nvidia_nim", "a", "down", http.StatusNotFound)
	store.RecordSuccess("nvidia_nim/a", "nvidia_nim", "a", 42)

	st := store.Get("nvidia_nim/a")
	if st == nil || !st.Reachable {
		t.Fatalf("expected reachable, got %+v", st)
	}
	if st.ConsecutiveFails != 0 {
		t.Fatalf("ConsecutiveFails = %d, want 0", st.ConsecutiveFails)
	}
	if st.LatencyMs != 42 {
		t.Fatalf("LatencyMs = %d, want 42", st.LatencyMs)
	}
}

func TestNeutralFailuresDoNotHideModels(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	store.MarkFilterReady()
	store.RecordFailure("nvidia_nim/a", "nvidia_nim", "a", "rate limit exceeded", http.StatusTooManyRequests)
	store.RecordFailure("nvidia_nim/a", "nvidia_nim", "a", "unauthorized", http.StatusUnauthorized)

	if store.Get("nvidia_nim/a") != nil {
		t.Fatal("neutral failures should not create/update status")
	}
	if !store.ShouldAdvertise("nvidia_nim/a", "nvidia_nim") {
		t.Fatal("model should still be advertised after neutral failures")
	}
}

func TestUnknownAsReachableFalseHidesUnprobed(t *testing.T) {
	store := health.NewModelStatusStore(1, false)
	store.MarkFilterReady()
	if store.ShouldAdvertise("nvidia_nim/unseen", "nvidia_nim") {
		t.Fatal("unprobed models should be hidden when unknown_as_reachable=false")
	}

	store.RecordSuccess("nvidia_nim/passed", "nvidia_nim", "passed", 10)
	if !store.ShouldAdvertise("nvidia_nim/passed", "nvidia_nim") {
		t.Fatal("models that passed probe must be advertised")
	}

	store.RecordFailure("nvidia_nim/failed", "nvidia_nim", "failed", "model not found", http.StatusNotFound)
	if store.ShouldAdvertise("nvidia_nim/failed", "nvidia_nim") {
		t.Fatal("failed probe models must not be advertised")
	}
}

func TestShouldAdvertiseBeforeFilterReadyKeepsUnprobedHidesFailed(t *testing.T) {
	store := health.NewModelStatusStore(1, false)
	store.RecordFailure("nvidia_nim/bad", "nvidia_nim", "bad", "model not found", http.StatusNotFound)
	store.RecordSuccess("nvidia_nim/good", "nvidia_nim", "good", 5)

	// Mid-pass: hide confirmed failures, keep unprobed + passed (no empty flicker).
	if store.ShouldAdvertise("nvidia_nim/bad", "nvidia_nim") {
		t.Fatal("confirmed failure must hide even before filter ready")
	}
	if !store.ShouldAdvertise("nvidia_nim/good", "nvidia_nim") || !store.ShouldAdvertise("nvidia_nim/unseen", "nvidia_nim") {
		t.Fatal("passed and unprobed must stay visible before filter ready")
	}

	store.MarkFilterReady()
	if store.ShouldAdvertise("nvidia_nim/bad", "nvidia_nim") || store.ShouldAdvertise("nvidia_nim/unseen", "nvidia_nim") {
		t.Fatal("after filter ready with unknown_as_reachable=false, only passed remain")
	}
	if !store.ShouldAdvertise("nvidia_nim/good", "nvidia_nim") {
		t.Fatal("passed model must remain after filter ready")
	}
}

func TestDefaultAvailableOnlyAfterFilterReady(t *testing.T) {
	// Production defaults: threshold 1, unknown_as_reachable false.
	store := health.NewModelStatusStore(1, false)

	store.RecordSuccess("openai/good", "openai", "good", 5)
	store.RecordFailure("openai/bad", "openai", "bad", "not found", http.StatusNotFound)
	// openai/pending never probed

	// Mid-pass: bad hidden, good+pending visible.
	if store.ShouldAdvertise("openai/bad", "openai") {
		t.Fatal("failed must hide mid-pass")
	}
	if !store.ShouldAdvertise("openai/good", "openai") || !store.ShouldAdvertise("openai/pending", "openai") {
		t.Fatal("good+pending must stay visible mid-pass")
	}

	store.MarkFilterReady()
	var advertised []string
	for _, id := range []string{"openai/good", "openai/bad", "openai/pending"} {
		if store.ShouldAdvertise(id, "openai") {
			advertised = append(advertised, id)
		}
	}
	if len(advertised) != 1 || advertised[0] != "openai/good" {
		t.Fatalf("after filter ready, advertised=%v, want only openai/good", advertised)
	}
}

func TestSubThresholdFailureStaysVisibleMidPass(t *testing.T) {
	store := health.NewModelStatusStore(2, false) // threshold=2, unknown_as_reachable=false
	store.MarkFilterReady()

	store.RecordFailure("nvidia_nim/a", "nvidia_nim", "a", "model not found", http.StatusNotFound)
	if !store.ShouldAdvertise("nvidia_nim/a", "nvidia_nim") {
		t.Fatal("sub-threshold failure should stay visible")
	}

	store.RecordFailure("nvidia_nim/a", "nvidia_nim", "a", "model not found", http.StatusNotFound)
	if store.ShouldAdvertise("nvidia_nim/a", "nvidia_nim") {
		t.Fatal("model should hide after reaching unhealthy threshold")
	}
}

func TestRestoreKeepsAvailableOnlyAcrossRestart(t *testing.T) {
	store := health.NewModelStatusStore(1, false)
	store.Restore([]health.ModelStatus{
		{ModelID: "nvidia_nim/good", Provider: "nvidia_nim", ProviderModelID: "good", Reachable: true},
		{ModelID: "nvidia_nim/bad", Provider: "nvidia_nim", ProviderModelID: "bad", Reachable: false, ConsecutiveFails: 1},
	}, true, []string{"nvidia_nim"})

	if !store.FilterReady() {
		t.Fatal("restored store should be filter-ready")
	}
	if !store.ShouldAdvertise("nvidia_nim/good", "nvidia_nim") {
		t.Fatal("passed model must stay advertised after restore")
	}
	if store.ShouldAdvertise("nvidia_nim/bad", "nvidia_nim") {
		t.Fatal("failed model must stay hidden after restore")
	}
	if store.ShouldAdvertise("nvidia_nim/unseen", "nvidia_nim") {
		t.Fatal("unprobed model must hide after restored filter-ready + unknown_as_reachable=false")
	}
}

type memPersist struct {
	statuses []health.ModelStatus
	allReady bool
	ready    []string
}

func (m *memPersist) UpsertStatus(st health.ModelStatus) error {
	for i := range m.statuses {
		if m.statuses[i].ModelID == st.ModelID {
			m.statuses[i] = st
			return nil
		}
	}
	m.statuses = append(m.statuses, st)
	return nil
}

func (m *memPersist) SaveFilterState(allReady bool, readyProviders []string) error {
	m.allReady = allReady
	m.ready = append([]string(nil), readyProviders...)
	return nil
}

func TestPersistenceWritesOnRecordAndFilterReady(t *testing.T) {
	store := health.NewModelStatusStore(1, false)
	sink := &memPersist{}
	store.SetPersistence(sink)

	store.RecordSuccess("nvidia_nim/good", "nvidia_nim", "good", 12)
	store.RecordFailure("nvidia_nim/bad", "nvidia_nim", "bad", "model not found", http.StatusNotFound)
	store.MarkProviderFilterReady("nvidia_nim")
	store.MarkFilterReady()

	if len(sink.statuses) != 2 {
		t.Fatalf("persisted statuses = %d, want 2", len(sink.statuses))
	}
	if !sink.allReady {
		t.Fatal("expected allReady persisted")
	}
	if len(sink.ready) == 0 {
		t.Fatal("expected ready providers persisted")
	}
}

func TestProviderFilterReadyScopesHiding(t *testing.T) {
	store := health.NewModelStatusStore(1, false)

	store.RecordSuccess("nvidia_nim/good", "nvidia_nim", "good", 5)
	store.RecordFailure("nvidia_nim/bad", "nvidia_nim", "bad", "model not found", http.StatusNotFound)
	// openai/pending is never probed.

	// Before any provider is ready, hide only confirmed failures.
	if store.ShouldAdvertise("nvidia_nim/bad", "nvidia_nim") {
		t.Fatal("confirmed failure must hide before provider ready")
	}
	if !store.ShouldAdvertise("nvidia_nim/good", "nvidia_nim") || !store.ShouldAdvertise("openai/pending", "openai") {
		t.Fatal("passed and unprobed from any provider must stay visible before provider ready")
	}

	store.MarkProviderFilterReady("nvidia_nim")

	// nvidia_nim is now ready: its unprobed models follow unknownAsReachable=false.
	if store.ShouldAdvertise("nvidia_nim/unseen", "nvidia_nim") {
		t.Fatal("unprobed nvidia_nim model should hide after its provider pass")
	}
	if !store.ShouldAdvertise("nvidia_nim/good", "nvidia_nim") {
		t.Fatal("passed nvidia_nim model must remain advertised")
	}
	// openai was never probed, so it should still show unprobed models.
	if !store.ShouldAdvertise("openai/pending", "openai") {
		t.Fatal("unprobed openai model must remain visible because openai was never probed")
	}
	if !store.ProviderFilterReady("nvidia_nim") {
		t.Fatal("nvidia_nim should report filter ready")
	}
	if store.ProviderFilterReady("openai") {
		t.Fatal("openai should not report filter ready")
	}
	if store.FilterReady() {
		t.Fatal("global FilterReady should remain false during scoped probing")
	}
}

func TestIsUnreachableProbeFailure(t *testing.T) {
	cases := []struct {
		code int
		msg  string
		want bool
	}{
		{http.StatusNotFound, "model not found", true},
		{http.StatusGone, "retired", true},
		{http.StatusBadRequest, "does not exist", true},
		{http.StatusBadRequest, "invalid max_tokens", false},
		{0, "timeout", false},
		{http.StatusBadGateway, "upstream error", false},
		{http.StatusServiceUnavailable, "overloaded", false},
		{http.StatusGatewayTimeout, "gateway timeout", false},
		{http.StatusTooManyRequests, "rate limit", false},
		{http.StatusUnauthorized, "bad key", false},
		{http.StatusOK, "", false},
	}
	for _, tc := range cases {
		got := health.IsUnreachableProbeFailure(tc.code, tc.msg)
		if got != tc.want {
			t.Fatalf("code=%d msg=%q: got %v want %v", tc.code, tc.msg, got, tc.want)
		}
	}
}

func TestIsInconclusiveProbeFailure(t *testing.T) {
	cases := []struct {
		code int
		msg  string
		want bool
	}{
		{0, "timeout", true},
		{0, "context deadline exceeded", true},
		{http.StatusBadGateway, "bad gateway", true},
		{http.StatusNotFound, "missing", false},
	}
	for _, tc := range cases {
		got := health.IsInconclusiveProbeFailure(tc.code, tc.msg)
		if got != tc.want {
			t.Fatalf("code=%d msg=%q: got %v want %v", tc.code, tc.msg, got, tc.want)
		}
	}
}
