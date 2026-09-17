// Package shadow_test — end-to-end test: read real SQLite shadow_observations
// through the store, adapt to eval records, evaluate, and assert the
// evaluator is a pure reader (the store is never written by the harness).
package shadow_test

import (
	"context"
	"testing"
	"time"

	jevshadow "github.com/EffNine/conductor/internal/jev/shadow"
	"github.com/EffNine/conductor/internal/jev/shadow/eval"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestSQLiteStoreToEvalEndToEnd runs the full persistence -> evaluation
// pipeline on a temp in-memory SQLite DB with synthetic rows (test-only;
// the harness itself never writes observations).
func TestSQLiteStoreToEvalEndToEnd(t *testing.T) {
	db, openErr := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if openErr != nil {
		t.Fatalf("open in-memory sqlite: %v", openErr)
	}
	store := jevshadow.NewSQLiteStore(db)
	if migErr := store.Migrate(); migErr != nil {
		t.Fatalf("migrate: %v", migErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now().UTC()
	// 4 complete (ok advisory + known outcome), 1 no-outcome, 1 JEV error.
	rows := []jevshadow.ShadowObservation{
		{DecisionID: "e1", ResolvedMode: "coding", SelectedProvider: "alpha",
			SelectedModel: "m", CandidateCount: 2, CandidateProviders: "alpha,beta",
			AdvisoryProvider: "alpha", AdvisoryConfidence: f64e2e(0.9),
			AdvisoryProbabilities: `{"alpha":0.9,"beta":0.1}`, AdvisoryStatus: jevshadow.AdvisoryOK,
			Outcome: "success", CreatedAt: now},
		{DecisionID: "e2", ResolvedMode: "coding", SelectedProvider: "alpha",
			SelectedModel: "m", CandidateCount: 2, CandidateProviders: "alpha,beta",
			AdvisoryProvider: "beta", AdvisoryConfidence: f64e2e(0.6),
			AdvisoryProbabilities: `{"alpha":0.4,"beta":0.6}`, AdvisoryStatus: jevshadow.AdvisoryOK,
			Outcome: "success", CreatedAt: now},
		{DecisionID: "e3", ResolvedMode: "fast", SelectedProvider: "gamma",
			SelectedModel: "g", CandidateCount: 3, CandidateProviders: "gamma,delta,epsilon",
			AdvisoryProvider: "delta", AdvisoryConfidence: f64e2e(0.5),
			AdvisoryProbabilities: `{"gamma":0.2,"delta":0.5,"epsilon":0.3}`, AdvisoryStatus: jevshadow.AdvisoryOK,
			Outcome: "failed", CreatedAt: now},
		{DecisionID: "e4", ResolvedMode: "fast", SelectedProvider: "gamma",
			SelectedModel: "g", CandidateCount: 3, CandidateProviders: "gamma,delta,epsilon",
			AdvisoryProvider: "gamma", AdvisoryConfidence: f64e2e(0.8),
			AdvisoryProbabilities: `{"gamma":0.8,"delta":0.1,"epsilon":0.1}`, AdvisoryStatus: jevshadow.AdvisoryOK,
			Outcome: "success", CreatedAt: now},
		{DecisionID: "e5", ResolvedMode: "coding", SelectedProvider: "alpha",
			SelectedModel: "m", CandidateCount: 2, CandidateProviders: "alpha,beta",
			AdvisoryProvider: "alpha", AdvisoryConfidence: f64e2e(0.7),
			AdvisoryProbabilities: `{"alpha":0.7,"beta":0.3}`, AdvisoryStatus: jevshadow.AdvisoryOK,
			Outcome: "", CreatedAt: now}, // no outcome
		{DecisionID: "e6", ResolvedMode: "coding", SelectedProvider: "alpha",
			SelectedModel: "m", CandidateCount: 2, CandidateProviders: "alpha,beta",
			AdvisoryStatus: jevshadow.AdvisoryTimeout, Outcome: "success", CreatedAt: now}, // JEV error: no advisory fields populated
	}
	for i := range rows {
		if saveErr := store.Save(ctx, &rows[i]); saveErr != nil {
			t.Fatalf("save row %d: %v", i, saveErr)
		}
	}

	// Read back via the same interface the CLI loader uses (List).
	got, listErr := store.List(ctx, 100)
	if listErr != nil {
		t.Fatalf("list: %v", listErr)
	}
	if len(got) != len(rows) {
		t.Fatalf("listed %d rows, want %d", len(got), len(rows))
	}

	// Adapt to eval records and run the evaluator.
	recs := make([]eval.Observation, 0, len(got))
	for _, r := range got {
		recs = append(recs, eval.RecordFromShadow(r))
	}
	ev := eval.NewEvaluator(eval.DefaultConfig(), nil)
	res := ev.Evaluate(recs)

	// Expected: complete=4, no_outcome=1, jev_error=1, total=6.
	if res.Dataset.Total != 6 {
		t.Fatalf("total = %d, want 6", res.Dataset.Total)
	}
	if res.Dataset.Complete != 4 {
		t.Fatalf("complete = %d, want 4", res.Dataset.Complete)
	}
	if res.Dataset.NoOutcome != 1 {
		t.Fatalf("no_outcome = %d, want 1", res.Dataset.NoOutcome)
	}
	if res.Dataset.JEVError != 1 {
		t.Fatalf("jev_error = %d, want 1", res.Dataset.JEVError)
	}
	// Agreement is measurable over all rows with a valid (ok) advisory
	// regardless of outcome. That is e1,e2,e3,e4,e5 = 5 rows: e1 agree,
	// e2 disagree, e3 disagree, e4 agree, e5 agree -> 3 agree / 2 disagree.
	if res.Agreement.Measurable != 5 {
		t.Fatalf("measurable = %d, want 5 (res=%+v)", res.Agreement.Measurable, res.Agreement)
	}
	if res.Agreement.Agreements != 3 || res.Agreement.Disagreements != 2 {
		t.Fatalf("agree/disagree = %d/%d, want 3/2", res.Agreement.Agreements, res.Agreement.Disagreements)
	}
	// Counterfactual: 3 success rows with a resolvable provider (e1,e2,e4).
	if res.Counterfactual.Eligible != 3 {
		t.Fatalf("counterfactual eligible = %d, want 3", res.Counterfactual.Eligible)
	}
	// Outcomes: success=4 (e1,e2,e4,e6), failed=1 (e3), unknown=1 (e5).
	if res.Outcomes.Success != 4 || res.Outcomes.Failed != 1 || res.Outcomes.Unknown != 1 {
		t.Fatalf("outcomes = %+v", res.Outcomes)
	}
	// The store was only read by the evaluator: row count is unchanged.
	again, _ := store.List(ctx, 100)
	if len(again) != len(rows) {
		t.Fatalf("store mutated by evaluator: now %d rows", len(again))
	}
}

func f64e2e(v float64) *float64 { return &v }
