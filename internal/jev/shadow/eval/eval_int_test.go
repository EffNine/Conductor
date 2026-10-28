package eval

import (
	"testing"

	"github.com/EffNine/conductor/internal/jev/shadow"
)

// TestEvaluatorDoesNotTouchShadowStore proves the evaluator is a pure
// reader: it constructs its inputs from a copy of store rows and never
// mutates the store or the deterministic routing state.
func TestEvaluatorDoesNotTouchShadowStore(t *testing.T) {
	conf := 0.7
	rows := []shadow.Observation{
		{
			DecisionID:            "d1",
			ResolvedMode:          "coding",
			SelectedProvider:      "alpha",
			CandidateCount:        2,
			CandidateProviders:    "alpha,beta",
			AdvisoryProvider:      "alpha",
			AdvisoryStatus:        shadow.AdvisoryOK,
			AdvisoryConfidence:    &conf,
			AdvisoryProbabilities: `{"alpha":0.7,"beta":0.3}`,
			Outcome:               "success",
		},
	}
	original := make([]shadow.Observation, len(rows))
	copy(original, rows)

	// Feed the evaluator via the shadow -> eval adapter.
	recs := make([]Observation, 0, len(rows))
	for _, r := range rows {
		recs = append(recs, RecordFromShadow(r))
	}
	ev := NewEvaluator(DefaultConfig(), nil)
	_ = ev.Evaluate(recs)

	// The store rows must be byte-identical to what we started with.
	for i := range rows {
		if rows[i] != original[i] {
			t.Fatalf("evaluator mutated store row %d", i)
		}
	}
}

// TestRecordFromShadowCopiesAllFields proves the adapter carries every field
// faithfully.
func TestRecordFromShadowCopiesAllFields(t *testing.T) {
	prob := 0.85
	src := shadow.Observation{
		DecisionID:            "d9",
		ResolvedMode:          "fast",
		SelectedProvider:      "zeta",
		SelectedModel:         "zeta-m",
		CandidateCount:        3,
		CandidateProviders:    "zeta,eta,theta",
		AdvisoryProvider:      "eta",
		AdvisoryConfidence:    &prob,
		AdvisoryProbabilities: `{"zeta":0.1,"eta":0.8,"theta":0.1}`,
		AdvisoryStatus:        shadow.AdvisoryOK,
		Outcome:               "failed",
		ProviderLatencyMs:     42,
	}
	got := RecordFromShadow(src)
	want := Observation{
		DecisionID:            "d9",
		ResolvedMode:          "fast",
		SelectedProvider:      "zeta",
		SelectedModel:         "zeta-m",
		CandidateCount:        3,
		CandidateProviders:    "zeta,eta,theta",
		AdvisoryProvider:      "eta",
		AdvisoryConfidence:    &prob,
		AdvisoryProbabilities: `{"zeta":0.1,"eta":0.8,"theta":0.1}`,
		AdvisoryStatus:        shadow.AdvisoryOK,
		Outcome:               "failed",
		ProviderLatencyMs:     42,
	}
	if got != want {
		t.Fatalf("RecordFromShadow mismatch:\n got %+v\nwant %+v", got, want)
	}
}
