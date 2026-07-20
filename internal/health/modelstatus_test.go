package health_test

import (
	"net/http"
	"testing"

	"github.com/novexa/gateway/internal/health"
)

func TestModelStatusStoreHidesAfterThreshold(t *testing.T) {
	store := health.NewModelStatusStore(2, true)

	if !store.ShouldAdvertise("nvidia_nim/meta/llama") {
		t.Fatal("unprobed model should be advertised when unknown_as_reachable=true")
	}

	store.RecordFailure("nvidia_nim/meta/llama", "nvidia_nim", "meta/llama", "not found", http.StatusNotFound)
	if !store.ShouldAdvertise("nvidia_nim/meta/llama") {
		t.Fatal("should still advertise after 1 failure with threshold 2")
	}

	store.RecordFailure("nvidia_nim/meta/llama", "nvidia_nim", "meta/llama", "not found", http.StatusNotFound)
	if store.ShouldAdvertise("nvidia_nim/meta/llama") {
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
	store.RecordFailure("nvidia_nim/a", "nvidia_nim", "a", "rate limit exceeded", http.StatusTooManyRequests)
	store.RecordFailure("nvidia_nim/a", "nvidia_nim", "a", "unauthorized", http.StatusUnauthorized)

	if store.Get("nvidia_nim/a") != nil {
		t.Fatal("neutral failures should not create/update status")
	}
	if !store.ShouldAdvertise("nvidia_nim/a") {
		t.Fatal("model should still be advertised after neutral failures")
	}
}

func TestUnknownAsReachableFalseHidesUnprobed(t *testing.T) {
	store := health.NewModelStatusStore(2, false)
	if store.ShouldAdvertise("nvidia_nim/unseen") {
		t.Fatal("unprobed models should be hidden when unknown_as_reachable=false")
	}
}

func TestIsUnreachableProbeFailure(t *testing.T) {
	cases := []struct {
		code int
		msg  string
		want bool
	}{
		{http.StatusNotFound, "model not found", true},
		{http.StatusBadRequest, "does not exist", true},
		{0, "timeout", true},
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
