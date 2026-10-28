//go:build c2fixture
// +build c2fixture

// C2 evaluation smoke fixture generator (test-only; not part of the build).
// Usage: CGO_ENABLED=1 go run -tags c2fixture ./tests/c2eval /tmp/c2_sample.db
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	jevshadow "github.com/EffNine/conductor/internal/jev/shadow"
	"github.com/EffNine/conductor/internal/jev/shadow/eval"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
	dbPath := os.Args[1]
	_ = os.Remove(dbPath)
	gormDB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	st := jevshadow.NewSQLiteStore(gormDB)
	if err := st.Migrate(); err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c9, c5, c3 := 0.9, 0.5, 0.3
	obs := []jevshadow.Observation{
		{DecisionID: "s1", ResolvedMode: "coding", SelectedProvider: "alpha",
			SelectedModel: "m", CandidateCount: 2, CandidateProviders: "alpha,beta",
			AdvisoryProvider: "alpha", AdvisoryConfidence: &c9,
			AdvisoryProbabilities: `{"alpha":0.9,"beta":0.1}`, AdvisoryStatus: jevshadow.AdvisoryOK,
			Outcome: "success"},
		{DecisionID: "s2", ResolvedMode: "fast", SelectedProvider: "alpha",
			SelectedModel: "m", CandidateCount: 2, CandidateProviders: "alpha,beta",
			AdvisoryProvider: "beta", AdvisoryConfidence: &c5,
			AdvisoryProbabilities: `{"alpha":0.3,"beta":0.7}`, AdvisoryStatus: jevshadow.AdvisoryOK,
			Outcome: "failed"},
		{DecisionID: "s3", ResolvedMode: "coding", SelectedProvider: "alpha",
			SelectedModel: "m", CandidateCount: 2, CandidateProviders: "alpha,beta",
			AdvisoryProvider: "alpha", AdvisoryConfidence: &c3,
			AdvisoryProbabilities: `{"alpha":0.6,"beta":0.4}`, AdvisoryStatus: jevshadow.AdvisoryOK,
			Outcome: "success"},
		{DecisionID: "s4", ResolvedMode: "fast", SelectedProvider: "alpha",
			SelectedModel: "m", CandidateCount: 2, CandidateProviders: "alpha,beta",
			AdvisoryProvider: "beta", AdvisoryConfidence: &c3,
			AdvisoryProbabilities: `{"alpha":0.4,"beta":0.6}`, AdvisoryStatus: jevshadow.AdvisoryOK,
			Outcome: ""},
	}
	for i := range obs {
		if err := st.Save(ctx, &obs[i]); err != nil {
			panic(err)
		}
	}
	// Now load via the eval snapshot round-trip to prove the records are
	// usable. This also demonstrates the snapshot -> eval path.
	got, err := st.List(ctx, 100)
	if err != nil {
		panic(err)
	}
	recs := make([]eval.Observation, 0, len(got))
	for _, r := range got {
		recs = append(recs, eval.RecordFromShadow(r))
	}
	if err := eval.WriteSnapshotJSON(dbPath+".snap.json", dbPath, recs); err != nil {
		panic(err)
	}
	res := eval.NewEvaluator(eval.DefaultConfig(), nil).Evaluate(recs)
	fmt.Printf("fixture db=%s total=%d complete=%d verdict=%s\n", dbPath, res.Dataset.Total, res.Dataset.Complete, res.Verdict)
}
