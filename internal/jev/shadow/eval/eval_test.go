package eval

import (
	"encoding/json"
	"math"
	"testing"
)

// f64 helpers for fixtures.
func fp(v float64) *float64 { return &v }

// probJSON renders a probability map deterministically as the recorded JSON.
func probJSON(m map[string]float64) string {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// okObs is a complete observation: valid decision, ok advisory with a
// distribution and confidence, known outcome.
func okObs(id, mode, sel string, outcome string) Observation {
	probs := map[string]float64{"alpha": 0.7, "beta": 0.3}
	if sel == "beta" {
		probs = map[string]float64{"alpha": 0.3, "beta": 0.7}
	}
	return Observation{
		DecisionID:            id,
		ResolvedMode:          mode,
		SelectedProvider:      sel,
		SelectedModel:         sel + "-model",
		CandidateCount:        2,
		CandidateProviders:    "alpha,beta",
		AdvisoryProvider:      sel,
		AdvisoryConfidence:    fp(0.7),
		AdvisoryProbabilities: probJSON(probs),
		AdvisoryStatus:        "ok",
		Outcome:               outcome,
	}
}

func TestAgreementExact(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	recs := []Observation{
		okObs("d1", "coding", "alpha", "success"),
		okObs("d2", "coding", "beta", "success"),
	}
	res := ev.Evaluate(recs)
	if res.Agreement.Measurable != 2 {
		t.Fatalf("measurable = %d, want 2 (res=%+v)", res.Agreement.Measurable, res.Agreement)
	}
	if res.Agreement.Agreements != 2 {
		t.Fatalf("agreements = %d, want 2", res.Agreement.Agreements)
	}
	if res.Agreement.Disagreements != 0 {
		t.Fatalf("disagreements = %d, want 0", res.Agreement.Disagreements)
	}
	if math.Abs(res.Agreement.Rate-1.0) > 1e-9 {
		t.Fatalf("rate = %f, want 1.0", res.Agreement.Rate)
	}
}

func TestDisagreement(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	d1 := okObs("d1", "coding", "alpha", "success")
	// JEV recommends beta while deterministic picked alpha.
	d1.AdvisoryProvider = "beta"
	d1.AdvisoryProbabilities = probJSON(map[string]float64{"alpha": 0.3, "beta": 0.7})
	res := ev.Evaluate([]Observation{d1})
	if res.Agreement.Agreements != 0 || res.Agreement.Disagreements != 1 {
		t.Fatalf("got agree=%d disagree=%d measurable=%d, want 0/1/1",
			res.Agreement.Agreements, res.Agreement.Disagreements, res.Agreement.Measurable)
	}
}

func TestMissingOutcomeIsNoOutcomeClass(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	o := okObs("d1", "coding", "alpha", "") // empty outcome
	res := ev.Evaluate([]Observation{o})
	if res.Dataset.NoOutcome != 1 {
		t.Fatalf("no_outcome = %d, want 1", res.Dataset.NoOutcome)
	}
	if res.Dataset.Complete != 0 {
		t.Fatalf("complete = %d, want 0", res.Dataset.Complete)
	}
}

func TestJEVErrorClassification(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	for _, status := range []string{"timeout", "unauthorized", "rate_limited", "malformed", "error", "not_configured"} {
		o := okObs("d1", "coding", "alpha", "success")
		o.AdvisoryStatus = status
		o.AdvisoryProvider = ""
		o.AdvisoryConfidence = nil
		o.AdvisoryProbabilities = ""
		res := ev.Evaluate([]Observation{o})
		if res.Dataset.JEVError != 1 {
			t.Fatalf("status=%s: jev_error = %d, want 1", status, res.Dataset.JEVError)
		}
	}
}

func TestMalformedDistribution(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	o := okObs("d1", "coding", "alpha", "success")
	o.AdvisoryProbabilities = "{not json"
	res := ev.Evaluate([]Observation{o})
	// Malformed probabilities with an ok advisory = internally inconsistent
	// fields -> invalid row.
	if res.Dataset.Invalid != 1 {
		t.Fatalf("invalid = %d, want 1", res.Dataset.Invalid)
	}
	if res.DataQuality.MalformedProbs != 1 {
		t.Fatalf("malformed_probs = %d, want 1", res.DataQuality.MalformedProbs)
	}
}

func TestCandidateMismatch(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	o := okObs("d1", "coding", "alpha", "success")
	o.SelectedProvider = "gamma" // not in candidate set
	res := ev.Evaluate([]Observation{o})
	// selected provider outside the candidate set is a candidate mismatch,
	// reported in DataQuality; the row still participates (its advisory is
	// independently valid) but the mismatch is made visible.
	if res.DataQuality.CandidateMismatches == 0 {
		t.Fatal("candidate_mismatches = 0, want >0")
	}
}

// calFixture builds a set of observations with known calibration inputs.
// All success outcomes, 8 rows, confidence 0.5 -> bucket [0.5,0.6).
// pWin varies: 0.5, 0.5, 0.7, 0.7, 0.9, 0.9, 1.0, 0.0 (last two excluded
// from log loss; all 8 counted for Brier).
func calFixture() []Observation {
	ps := []float64{0.5, 0.5, 0.7, 0.7, 0.9, 0.9, 1.0, 0.0}
	out := make([]Observation, 0, len(ps))
	for i, p := range ps {
		o := okObs(string(rune('a'+i)), "coding", "alpha", "success")
		o.AdvisoryConfidence = fp(0.5)
		o.AdvisoryProbabilities = probJSON(map[string]float64{"alpha": p, "beta": 1 - p})
		out = append(out, o)
	}
	return out
}

func TestCalibrationBuckets(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinBucketSamples = 4 // small so this fixture counts as non-low-sample
	ev := NewEvaluator(cfg, nil)
	res := ev.Evaluate(calFixture())

	var bucket *CalBucket
	for i := range res.Calibration {
		b := res.Calibration[i]
		if b.Low == 0.5 && b.High == 0.6 {
			bucket = &res.Calibration[i]
		}
	}
	if bucket == nil {
		t.Fatal("no bucket [0.5,0.6) found")
	}
	if bucket.Count != 8 {
		t.Fatalf("bucket count = %d, want 8", bucket.Count)
	}
	if math.Abs(bucket.SuccessRate-1.0) > 1e-9 {
		t.Fatalf("success_rate = %f, want 1.0", bucket.SuccessRate)
	}
	if bucket.LowSample {
		t.Fatal("bucket flagged low-sample, want not (min=4, n=8)")
	}
	if bucket.Brier == nil {
		t.Fatal("bucket brier nil, want value")
	}
	// expected brier = mean of (p-1)^2 over all 8 (all successes, y=1).
	want := (0.25 + 0.25 + 0.09 + 0.09 + 0.01 + 0.01 + 0.0 + 1.0) / 8
	if math.Abs(*bucket.Brier-want) > 1e-9 {
		t.Fatalf("bucket brier = %f, want %f", *bucket.Brier, want)
	}
}

func TestGlobalBrier(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	res := ev.Evaluate(calFixture())
	if res.GlobalBrier == nil {
		t.Fatal("global brier nil")
	}
	want := (0.25 + 0.25 + 0.09 + 0.09 + 0.01 + 0.01 + 0.0 + 1.0) / 8
	if math.Abs(*res.GlobalBrier-want) > 1e-9 {
		t.Fatalf("global brier = %f, want %f", *res.GlobalBrier, want)
	}
}

func TestGlobalLogLoss(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	res := ev.Evaluate(calFixture())
	if res.GlobalLogLoss == nil {
		t.Fatal("global log loss nil, want value (6 in-range probs)")
	}
	// expected: -[1*ln(0.5)+1*ln(0.5)+1*ln(0.7)+1*ln(0.7)+1*ln(0.9)+1*ln(0.9)]/6
	want := -(math.Log(0.5) + math.Log(0.5) + math.Log(0.7) + math.Log(0.7) + math.Log(0.9) + math.Log(0.9)) / 6
	if math.Abs(*res.GlobalLogLoss-want) > 1e-9 {
		t.Fatalf("global log loss = %f, want %f", *res.GlobalLogLoss, want)
	}
}

func TestFailedOutcomeInCalibration(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	o := okObs("d1", "coding", "alpha", "failed")
	probs := map[string]float64{"alpha": 0.8, "beta": 0.2}
	o.AdvisoryProbabilities = probJSON(probs)
	o.AdvisoryProvider = "alpha"
	res := ev.Evaluate([]Observation{o})
	if res.Outcomes.Failed != 1 {
		t.Fatalf("failed = %d, want 1", res.Outcomes.Failed)
	}
	if res.GlobalBrier == nil {
		t.Fatal("global brier nil, want value")
	}
	// y=0, p=0.8: brier = (0.8-0)^2 = 0.64.
	if math.Abs(*res.GlobalBrier-0.64) > 1e-9 {
		t.Fatalf("brier = %f, want 0.64", *res.GlobalBrier)
	}
	// log loss: -(1-0)*ln(1-0.8) = -ln(0.2).
	want := -math.Log(0.2)
	if math.Abs(*res.GlobalLogLoss-want) > 1e-9 {
		t.Fatalf("log loss = %f, want %f", *res.GlobalLogLoss, want)
	}
}

func TestSuccessfulObservedProviderMatch(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	// JEV picked beta, deterministic picked alpha, outcome success ->
	// successful observed provider = alpha (no fallback data). Mismatch.
	d1 := okObs("d1", "coding", "alpha", "success")
	d1.AdvisoryProvider = "beta"
	d1.AdvisoryProbabilities = probJSON(map[string]float64{"alpha": 0.3, "beta": 0.7})
	// JEV picked alpha, deterministic alpha, success -> match.
	d2 := okObs("d2", "coding", "alpha", "success")

	res := ev.Evaluate([]Observation{d1, d2})
	if res.Counterfactual.Eligible != 2 {
		t.Fatalf("eligible = %d, want 2", res.Counterfactual.Eligible)
	}
	if res.Counterfactual.Matches != 1 {
		t.Fatalf("matches = %d, want 1", res.Counterfactual.Matches)
	}
	if math.Abs(res.Counterfactual.Rate-0.5) > 1e-9 {
		t.Fatalf("rate = %f, want 0.5", res.Counterfactual.Rate)
	}
}

func TestCounterfactualUnresolvableForFailed(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	// failed outcome has no successful provider to match against.
	d1 := okObs("d1", "coding", "alpha", "failed")
	res := ev.Evaluate([]Observation{d1})
	if res.Counterfactual.Eligible != 0 {
		t.Fatalf("eligible = %d, want 0", res.Counterfactual.Eligible)
	}
	if res.Counterfactual.Unresolvable != 0 {
		t.Fatalf("unresolvable = %d, want 0 (failed rows are not success, so not counted)", res.Counterfactual.Unresolvable)
	}
}

func TestMultipleProviders(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	recs := []Observation{
		okObs("d1", "coding", "alpha", "success"),
		okObs("d2", "fast", "beta", "success"),
		okObs("d3", "fast", "gamma", "failed"),
	}
	res := ev.Evaluate(recs)
	if len(res.ByProvider) != 3 {
		t.Fatalf("by_provider count = %d, want 3", len(res.ByProvider))
	}
	// alpha: obs 1 selected; beta: obs 1; gamma: obs 1.
	var alpha, gamma *ProvStats
	for i := range res.ByProvider {
		switch res.ByProvider[i].Provider {
		case "alpha":
			alpha = res.ByProvider[i]
		case "gamma":
			gamma = res.ByProvider[i]
		}
	}
	if alpha == nil || alpha.Observations != 1 || alpha.SelectedCount != 1 || alpha.OutcomesSuccess != 1 {
		t.Fatalf("alpha stats wrong: %+v", alpha)
	}
	if gamma == nil || gamma.OutcomesSuccess != 0 {
		t.Fatalf("gamma stats wrong: %+v", gamma)
	}
}

func TestEmptyDataset(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	res := ev.Evaluate(nil)
	if res.Dataset.Total != 0 {
		t.Fatalf("total = %d, want 0", res.Dataset.Total)
	}
	if res.Agreement.Rate != 0 {
		t.Fatalf("rate = %f, want 0 on empty", res.Agreement.Rate)
	}
	if res.Verdict != VerdictInsufficientEvidence {
		t.Fatalf("verdict = %s, want INSUFFICIENT_EVIDENCE", res.Verdict)
	}
	if res.GlobalBrier != nil || res.GlobalLogLoss != nil {
		t.Fatal("brier/log-loss should be nil on empty dataset")
	}
}

func TestInsufficientSamplesFlag(t *testing.T) {
	// 5 complete rows, MinBucketSamples default 20 -> buckets flagged low.
	cfg := DefaultConfig()
	ev := NewEvaluator(cfg, nil)
	recs := []Observation{}
	for i := 0; i < 5; i++ {
		recs = append(recs, okObs(string(rune('a'+i)), "coding", "alpha", "success"))
	}
	res := ev.Evaluate(recs)
	if res.Dataset.Complete != 5 {
		t.Fatalf("complete = %d, want 5", res.Dataset.Complete)
	}
	if res.Verdict != VerdictInsufficientEvidence {
		t.Fatalf("verdict = %s, want INSUFFICIENT_EVIDENCE (<10)", res.Verdict)
	}
	// The single occupied bucket should be low-sample.
	found := false
	for _, b := range res.Calibration {
		if b.Count > 0 {
			if !b.LowSample {
				t.Fatalf("bucket n=%d should be low-sample (min=20)", b.Count)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no occupied bucket found")
	}
}

func TestVerdictThresholds(t *testing.T) {
	cfg := DefaultConfig()
	ev := NewEvaluator(cfg, nil)
	mk := func(n int) []Observation {
		out := make([]Observation, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, okObs(string(rune('a'+i)), "coding", "alpha", "success"))
		}
		return out
	}
	if res := ev.Evaluate(mk(9)); res.Verdict != VerdictInsufficientEvidence {
		t.Fatalf("9 complete -> verdict = %s, want INSUFFICIENT", res.Verdict)
	}
	if res := ev.Evaluate(mk(15)); res.Verdict != VerdictLimitedEvidence {
		t.Fatalf("15 complete -> verdict = %s, want LIMITED", res.Verdict)
	}
	if res := ev.Evaluate(mk(40)); res.Verdict != VerdictAdequateEvidence {
		t.Fatalf("40 complete -> verdict = %s, want ADEQUATE", res.Verdict)
	}
}

func TestDeterministicOrdering(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	base := []Observation{
		okObs("d1", "coding", "alpha", "success"),
		okObs("d2", "coding", "beta", "failed"),
		okObs("d3", "fast", "alpha", "success"),
	}
	// shuffled copy
	shuffled := []Observation{base[2], base[0], base[1]}
	a := ev.Evaluate(base)
	b := ev.Evaluate(shuffled)
	ja, _ := a.JSON()
	jb, _ := b.JSON()
	if ja != jb {
		t.Fatal("reordered input changed the JSON output; evaluation is not deterministic")
	}
}

func TestNoMutationOfInputs(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	recs := []Observation{
		okObs("d1", "coding", "alpha", "success"),
		okObs("d2", "coding", "beta", "failed"),
	}
	snapshot := make([]Observation, len(recs))
	copy(snapshot, recs)
	_ = ev.Evaluate(recs)
	for i := range recs {
		if recs[i] != snapshot[i] {
			t.Fatalf("evaluator mutated input row %d: %+v", i, recs[i])
		}
	}
}

func TestOutcomeStratification(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	recs := []Observation{
		okObs("d1", "coding", "alpha", "success"),
		okObs("d2", "coding", "alpha", "failed"),
		okObs("d3", "coding", "alpha", "skipped"),
		okObs("d4", "coding", "alpha", ""),
	}
	res := ev.Evaluate(recs)
	if res.Outcomes.Success != 1 || res.Outcomes.Failed != 1 ||
		res.Outcomes.Skipped != 1 || res.Outcomes.Unknown != 1 {
		t.Fatalf("outcome strata wrong: %+v", res.Outcomes)
	}
	if res.Outcomes.TotalKnown != 3 {
		t.Fatalf("total_known = %d, want 3", res.Outcomes.TotalKnown)
	}
}

func TestDuplicateDecisionIDsReported(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	d1 := okObs("d1", "coding", "alpha", "success")
	d1b := okObs("d1", "coding", "alpha", "success")
	res := ev.Evaluate([]Observation{d1, d1b})
	if res.DataQuality.DuplicateDecisionIDs != 1 {
		t.Fatalf("duplicate_decision_ids = %d, want 1", res.DataQuality.DuplicateDecisionIDs)
	}
}

func TestUnknownModeReported(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	o := okObs("d1", "wizard_mode", "alpha", "success")
	res := ev.Evaluate([]Observation{o})
	if res.DataQuality.UnknownModes != 1 {
		t.Fatalf("unknown_modes = %d, want 1", res.DataQuality.UnknownModes)
	}
}

func TestNonNormalizedDistributionReported(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), nil)
	o := okObs("d1", "coding", "alpha", "success")
	// A distribution that does not sum to 1.0 is still usable but flagged.
	o.AdvisoryProbabilities = probJSON(map[string]float64{"alpha": 0.4, "beta": 0.4})
	res := ev.Evaluate([]Observation{o})
	if res.DataQuality.NonNormalizedProbs != 1 {
		t.Fatalf("non_normalized_probs = %d, want 1", res.DataQuality.NonNormalizedProbs)
	}
	// The row is still complete (not excluded); the flag is diagnostic.
	if res.Dataset.Complete != 1 {
		t.Fatalf("complete = %d, want 1 (flagged, not excluded)", res.Dataset.Complete)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/snap.json"
	recs := []Observation{
		okObs("d1", "coding", "alpha", "success"),
		okObs("d2", "coding", "beta", "failed"),
	}
	if err := WriteSnapshotJSON(path, "test", recs); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadSnapshotJSON(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(recs) {
		t.Fatalf("records = %d, want %d", len(got), len(recs))
	}
	a := NewEvaluator(DefaultConfig(), nil).Evaluate(recs)
	b := NewEvaluator(DefaultConfig(), nil).Evaluate(got)
	ja, _ := a.JSON()
	jb, _ := b.JSON()
	if ja != jb {
		t.Fatal("snapshot round-trip changed the result")
	}
}

func TestProviderWhitelistUnknownProviders(t *testing.T) {
	ev := NewEvaluator(DefaultConfig(), func(p string) bool {
		return p == "alpha" || p == "beta"
	})
	o := okObs("d1", "coding", "gamma", "success") // gamma not in whitelist
	res := ev.Evaluate([]Observation{o})
	if res.DataQuality.UnknownProviders != 1 {
		t.Fatalf("unknown_providers = %d, want 1", res.DataQuality.UnknownProviders)
	}
}
