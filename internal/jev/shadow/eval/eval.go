// Package eval implements the Phase C2 offline evaluation harness for JEV
// shadow observations. It is a PURE READER: it loads persisted
// shadow_observations rows and computes deterministic agreement, calibration,
// and counterfactual metrics. It never writes to the observation store,
// never influences routing, and never calls the JEV service.
//
// The harness is offline and evidence-only: it answers "is there measurable
// signal in the existing shadow observations?" and must not be read as
// "should Conductor route with JEV?" — that belongs to a later controlled
// experiment, if the evidence is sufficient.
package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/EffNine/conductor/internal/jev/shadow"
)

// Classification categories for an observation row. Every input row lands in
// exactly one category; the evaluator reports all of them explicitly.
const (
	// ClassComplete: valid deterministic decision + valid JEV advisory +
	// identifiable candidate set + known execution outcome.
	ClassComplete = "complete"
	// ClassNoOutcome: routing/advisory information is usable but the
	// execution outcome was not recorded. NOT a JEV failure.
	ClassNoOutcome = "no_outcome"
	// ClassJEVError: the JEV advisory call failed or produced no usable
	// result. Reported, but not treated as a JEV quality failure of the
	// advisory signal itself.
	ClassJEVError = "jev_error"
	// ClassInvalid: required fields are missing or inconsistent, so the
	// row cannot safely participate in any metric.
	ClassInvalid = "invalid"
)

// Execution outcome values persisted by the C1 bridge/attempt backfill.
const (
	OutcomeSuccess = "success"
	OutcomeFailed  = "failed"
	OutcomeSkipped = "skipped"
)

// Outcome strata used for reporting. A "terminal" outcome is one of
// success/failed/skipped; everything else (empty or "unknown") is
// unknown/unavailable, which is a distinct category, never collapsed into
// failure.
const (
	StratumSuccess = "success"
	StratumFailed  = "failed"
	StratumSkipped = "skipped"
	StratumUnknown = "unknown_or_unavailable"
)

// advisory statuses (mirror shadow.Advisory* constants, re-listed here so
// eval stays decoupled from production names).
const (
	advOK            = "ok"
	advTimeout       = "timeout"
	advUnauthorized  = "unauthorized"
	advRateLimited   = "rate_limited"
	advMalformed     = "malformed"
	advError         = "error"
	advNotConfigured = "not_configured"
)

// Observation is the eval's read-only view of one shadow_observations row.
// It is a plain value type so the evaluator can never mutate store state.
type Observation struct {
	DecisionID       string
	ResolvedMode     string
	SelectedProvider string
	SelectedModel    string
	CandidateCount   int
	// CandidateProviders is the recorded CSV candidate set (parse order is
	// the recorded order; the evaluator sorts it for deterministic use).
	CandidateProviders string
	AdvisoryProvider   string
	// AdvisoryConfidence is nil when no confidence was recorded.
	AdvisoryConfidence    *float64
	AdvisoryProbabilities string
	AdvisoryStatus        string
	Outcome               string
	ProviderLatencyMs     int64
}

// RecordFromShadow copies a persisted observation into the eval view.
func RecordFromShadow(o shadow.ShadowObservation) Observation {
	return Observation{
		DecisionID:            o.DecisionID,
		ResolvedMode:          o.ResolvedMode,
		SelectedProvider:      o.SelectedProvider,
		SelectedModel:         o.SelectedModel,
		CandidateCount:        o.CandidateCount,
		CandidateProviders:    o.CandidateProviders,
		AdvisoryProvider:      o.AdvisoryProvider,
		AdvisoryConfidence:    o.AdvisoryConfidence,
		AdvisoryProbabilities: o.AdvisoryProbabilities,
		AdvisoryStatus:        o.AdvisoryStatus,
		Outcome:               o.Outcome,
		ProviderLatencyMs:     o.ProviderLatencyMs,
	}
}

// Config controls a deterministic evaluation run.
type Config struct {
	// BucketEdges are the confidence-bucket boundaries in [0,1). A bucket
	// [edges[i], edges[i+1]) holds values with edges[i] <= c < edges[i+1].
	// The final implicit bucket is [lastEdge, 1.0]. Defaults to the
	// standard deciles when empty.
	BucketEdges []float64
	// MinBucketSamples is the minimum sample count per confidence bucket
	// before the bucket's empirical frequency is reported as meaningful
	// (smaller buckets are still reported but flagged as low-sample).
	// Default 20.
	MinBucketSamples int
}

// DefaultConfig returns the standard deterministic configuration.
func DefaultConfig() Config {
	return Config{
		MinBucketSamples: 20,
	}
}

// classify returns the category and the data-quality reason codes that
// apply to one observation. The category is exactly one of the four class
// constants. Reasons is empty for a clean ClassComplete row.
func classify(o Observation, knownProviders func(string) bool) (string, []string) {
	reasons := make([]string, 0, 4)
	// A usable deterministic decision requires a selected provider AND a
	// non-empty candidate set.
	hasDecision := o.SelectedProvider != "" && len(parseCandidates(o.CandidateProviders)) > 0

	// Consistency: when both candidate count and provider list exist, they
	// must agree.
	if o.CandidateCount > 0 && len(parseCandidates(o.CandidateProviders)) != o.CandidateCount {
		reasons = append(reasons, "candidate_count_mismatch")
	}
	// Unknown providers: selected or advisory provider not in the candidate
	// set is a candidate mismatch, not a JEV failure.
	if o.SelectedProvider != "" {
		cands := parseCandidates(o.CandidateProviders)
		if !contains(cands, o.SelectedProvider) {
			reasons = append(reasons, "selected_provider_not_in_candidates")
		}
	}
	if o.AdvisoryStatus == "" {
		reasons = append(reasons, "missing_advisory_status")
	}
	if !hasDecision {
		reasons = append(reasons, "missing_decision")
	}

	// Invalid: cannot safely participate. Missing decision fields, or the
	// advisory is claimed ok but its fields are empty.
	if !hasDecision ||
		(o.AdvisoryStatus == advOK && o.AdvisoryStatusFieldBroken()) {
		return ClassInvalid, reasons
	}

	// JEV error class: any non-ok advisory status.
	switch o.AdvisoryStatus {
	case advOK:
		// usable advisory
	case advTimeout, advUnauthorized, advRateLimited, advMalformed, advError:
		return ClassJEVError, reasons
	case advNotConfigured:
		return ClassJEVError, reasons
	default:
		return ClassInvalid, reasons
	}

	// ok advisory: complete iff a known execution outcome exists.
	if isKnownOutcome(o.Outcome) {
		return ClassComplete, reasons
	}
	return ClassNoOutcome, reasons
}

// AdvisoryStatusFieldBroken reports whether an "ok" advisory row has
// internally inconsistent fields (status ok but missing provider or
// probabilities not parseable when claimed present).
func (o Observation) AdvisoryStatusFieldBroken() bool {
	if o.AdvisoryProvider == "" {
		return true
	}
	if o.AdvisoryProbabilities != "" {
		if _, ok := parseProbabilities(o.AdvisoryProbabilities); !ok {
			return true
		}
	}
	return false
}

// isKnownOutcome reports whether the outcome field carries a terminal
// execution result (as opposed to empty/unknown).
func isKnownOutcome(outcome string) bool {
	switch outcome {
	case OutcomeSuccess, OutcomeFailed, OutcomeSkipped:
		return true
	default:
		return false
	}
}

// outcomeStratum maps a raw outcome string to its reporting stratum.
func outcomeStratum(outcome string) string {
	switch outcome {
	case OutcomeSuccess:
		return StratumSuccess
	case OutcomeFailed:
		return StratumFailed
	case OutcomeSkipped:
		return StratumSkipped
	default:
		return StratumUnknown
	}
}

// parseCandidates splits a recorded CSV candidate set, trimming empty
// entries. Returns a sorted copy for deterministic use.
func parseCandidates(csv string) []string {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// parseProbabilities parses the recorded JSON probability map. It returns ok
// only when the JSON is valid and every value is a finite number in [0,1].
// An empty map (no JSON recorded) returns a nil map with ok=true, which the
// caller treats as "no distribution recorded" — not an error.
func parseProbabilities(raw string) (map[string]float64, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, true
	}
	var m map[string]float64
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, false
	}
	for k, v := range m {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return nil, false
		}
		_ = k
	}
	return m, true
}

// sanityReport returns a human-readable consistency check of a
// distribution: the sum and whether it is close to 1.0. Distributions
// that do not sum to ~1 are flagged so the report makes it visible.
func sanityReport(m map[string]float64) (sum float64, normalized bool) {
	if len(m) == 0 {
		return -1, true
	}
	sum = 0.0
	for _, v := range m {
		sum += v
	}
	return sum, math.Abs(sum-1.0) < 1e-6
}

// Result is the complete output of one evaluation run. It is deterministic
// for a given input snapshot and configuration: identical inputs produce
// byte-identical JSON.
type Result struct {
	Config         Config         `json:"config"`
	Dataset        DatasetStats   `json:"dataset"`
	Agreement      Agreement      `json:"agreement"`
	Calibration    []CalBucket    `json:"calibration"`
	GlobalBrier    *float64       `json:"global_brier"`
	GlobalLogLoss  *float64       `json:"global_log_loss"`
	ByMode         []*ModeStats   `json:"by_mode"`
	ByProvider     []*ProvStats   `json:"by_provider"`
	Counterfactual Counterfactual `json:"counterfactual"`
	Outcomes       OutcomeStats   `json:"outcomes"`
	DataQuality    DataQuality    `json:"data_quality"`
	// Verdict is a fixed categorical label for the overall evidence
	// strength. It is NOT a ranking of JEV vs deterministic.
	Verdict Verdict `json:"verdict"`
}

// Verdict is a fixed categorical label for the overall evidence strength.
// It is NOT a ranking of JEV vs deterministic.
type Verdict string

const (
	VerdictInsufficientEvidence = Verdict("INSUFFICIENT_EVIDENCE")
	VerdictLimitedEvidence      = Verdict("LIMITED_EVIDENCE")
	VerdictAdequateEvidence     = Verdict("ADEQUATE_EVIDENCE")
)

// minAdequateSamples is the smallest complete-observation count the harness
// treats as enough to move past "insufficient". This is an evaluation-
// sufficiency threshold, not a quality judgment on JEV.
const minAdequateSamples = 30

// minLimitedSamples is the count below which evidence is "limited".
const minLimitedSamples = 10

// DatasetStats counts every input row by category.
type DatasetStats struct {
	Total     int `json:"total"`
	Complete  int `json:"complete"`
	NoOutcome int `json:"no_outcome"`
	JEVError  int `json:"jev_error"`
	Invalid   int `json:"invalid"`
	// OutcomeCoverage is the fraction of rows with a known execution
	// outcome (0.0 when total is 0).
	OutcomeCoverage float64 `json:"outcome_coverage"`
}

// Agreement measures deterministic-vs-JEV top-choice match. It is a
// descriptive statistic, not a correctness claim.
type Agreement struct {
	// Measurable is the count of rows with a usable JEV top recommendation
	// (advisory status ok and advisory provider present) regardless of
	// outcome.
	Measurable    int `json:"measurable"`
	Agreements    int `json:"agreements"`
	Disagreements int `json:"disagreements"`
	// Rate is agreements/measurable, or 0 when measurable is 0.
	Rate float64 `json:"rate"`
}

// CalBucket is one confidence bucket with its empirical outcome rate.
type CalBucket struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
	// Count is the number of measurable rows whose confidence falls in the
	// bucket.
	Count int `json:"count"`
	// SuccessRate is the fraction of Count rows with a successful
	// execution outcome, or 0 when Count is 0.
	SuccessRate float64 `json:"success_rate"`
	// MeanProb is the mean JEV probability assigned to the
	// successful-observed-provider within the bucket, or 0 when there is no
	// distribution data.
	MeanProb  float64  `json:"mean_prob"`
	LowSample bool     `json:"low_sample"`
	Brier     *float64 `json:"brier"`
	LogLoss   *float64 `json:"log_loss"`
}

// ModeStats reports per-mode counts and agreement.
type ModeStats struct {
	Mode          string  `json:"mode"`
	Observations  int     `json:"observations"`
	Measurable    int     `json:"measurable"`
	Agreements    int     `json:"agreements"`
	Disagreements int     `json:"disagreements"`
	OutcomesKnown int     `json:"outcomes_known"`
	AgreementRate float64 `json:"agreement_rate"`
}

// ProvStats reports per-provider counts.
type ProvStats struct {
	Provider        string `json:"provider"`
	Observations    int    `json:"observations"`
	SelectedCount   int    `json:"selected_count"`
	AdvisedCount    int    `json:"advised_count"`
	Agreements      int    `json:"agreements"`
	Disagreements   int    `json:"disagreements"`
	OutcomesKnown   int    `json:"outcomes_known"`
	OutcomesSuccess int    `json:"outcomes_success"`
}

// Counterfactual reports whether the JEV top recommendation matched the
// provider associated with the observed successful execution outcome.
// Named deliberately as a provider match (not "picked the winner"): the
// shadow schema has no fallback-provider field, so a success outcome's
// provider is the deterministic selected provider only, and a success
// that required a fallback cannot be attributed to a unique winner.
type Counterfactual struct {
	// Eligible is the count of complete rows where the recorded outcome is
	// success AND the successful provider is determinable from the recorded
	// fields (the selected provider).
	Eligible int `json:"eligible"`
	// Matches is the count of eligible rows where the JEV top
	// recommendation equals the successful observed provider.
	Matches int `json:"matches"`
	// Rate is matches/eligible, or 0 when eligible is 0.
	Rate float64 `json:"rate"`
	// Unresolvable is the count of success-outcome rows where a unique
	// successful provider cannot be established from the data, so the
	// counterfactual is deliberately not computed for them.
	Unresolvable int `json:"unresolvable"`
}

// OutcomeStats is the stratified outcome breakdown.
type OutcomeStats struct {
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Unknown int `json:"unknown_or_unavailable"`
	// TotalKnown is success+failed+skipped.
	TotalKnown int `json:"total_known"`
}

// DataQuality makes every exclusion and data problem visible.
type DataQuality struct {
	DuplicateDecisionIDs int `json:"duplicate_decision_ids"`
	MalformedProbs       int `json:"malformed_probabilities"`
	// NonNormalizedProbs counts valid-JSON distributions whose probabilities
	// do not sum to ~1.0. They are still usable (the harness normalizes
	// nothing; it only reports) but flagged so the reader knows the JEV
	// output was not a proper distribution.
	NonNormalizedProbs  int `json:"non_normalized_probabilities"`
	CandidateMismatches int `json:"candidate_mismatches"`
	UnknownProviders    int `json:"unknown_providers"`
	UnknownModes        int `json:"unknown_modes"`
	// Exclusions lists the exact per-category exclusion counts.
	Exclusions []Exclusion `json:"exclusions"`
}

// Exclusion is one exact exclusion reason with its count.
type Exclusion struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// Evaluator computes a deterministic Result from a slice of observations.
type Evaluator struct {
	cfg Config
	// knownProviders optionally restricts which provider ids are "known".
	// Nil = all providers observed are treated as known.
	knownProviders func(string) bool
}

// NewEvaluator builds an evaluator. The provider whitelist may be nil.
func NewEvaluator(cfg Config, knownProviders func(string) bool) *Evaluator {
	if cfg.BucketEdges == nil {
		cfg.BucketEdges = []float64{0.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9}
	}
	if cfg.MinBucketSamples <= 0 {
		cfg.MinBucketSamples = 20
	}
	return &Evaluator{cfg: cfg, knownProviders: knownProviders}
}

// Evaluate computes the full Result. It is a pure function of its inputs.
func (e *Evaluator) Evaluate(obs []Observation) Result {
	cfg := e.cfg
	res := Result{Config: cfg}

	// Deterministic ordering: sort by decision id (then provider, mode) so
	// identical input sets always produce identical metrics regardless of
	// input order.
	sorted := make([]Observation, len(obs))
	copy(sorted, obs)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.DecisionID != b.DecisionID {
			return a.DecisionID < b.DecisionID
		}
		if a.SelectedProvider != b.SelectedProvider {
			return a.SelectedProvider < b.SelectedProvider
		}
		if a.ResolvedMode != b.ResolvedMode {
			return a.ResolvedMode < b.ResolvedMode
		}
		return a.AdvisoryProvider < b.AdvisoryProvider
	})

	// Data-quality pass.
	dq := DataQuality{}
	seenIDs := make(map[string]int)
	provCount := make(map[string]int)
	modeCount := make(map[string]int)
	knownModes := knownModeSet()

	for _, o := range sorted {
		if o.DecisionID != "" {
			seenIDs[o.DecisionID]++
		}
		for _, p := range parseCandidates(o.CandidateProviders) {
			provCount[p]++
		}
		if o.SelectedProvider != "" {
			provCount[o.SelectedProvider]++
		}
		if o.AdvisoryProvider != "" {
			provCount[o.AdvisoryProvider]++
		}
		if o.ResolvedMode != "" {
			modeCount[o.ResolvedMode]++
		}
	}
	for id := range seenIDs {
		if seenIDs[id] > 1 {
			dq.DuplicateDecisionIDs += seenIDs[id] - 1
		}
	}

	// Known-provider resolver: a provider is "known" when it appears in at
	// least one row's candidate set or as selected/advised, unless an
	// external whitelist is set.
	knownProvider := e.knownProviders
	if knownProvider == nil {
		knownProvider = func(string) bool { return true }
	}

	for _, o := range sorted {
		if !knownProvider(o.SelectedProvider) || (!knownProvider(o.AdvisoryProvider) && o.AdvisoryProvider != "") {
			dq.UnknownProviders++
		}
		if o.ResolvedMode != "" && !knownModes[o.ResolvedMode] {
			dq.UnknownModes++
		}
		// Malformed probability JSON (only when non-empty).
		if o.AdvisoryProbabilities != "" {
			if probs, ok := parseProbabilities(o.AdvisoryProbabilities); !ok {
				dq.MalformedProbs++
			} else {
				if _, norm := sanityReport(probs); !norm {
					dq.NonNormalizedProbs++
				}
			}
		}
		// Candidate mismatch flags.
		cands := parseCandidates(o.CandidateProviders)
		if o.SelectedProvider != "" && !contains(cands, o.SelectedProvider) {
			dq.CandidateMismatches++
		}
		if o.AdvisoryProvider != "" && !contains(cands, o.AdvisoryProvider) {
			dq.CandidateMismatches++
		}
	}

	// Classification pass + all metric accumulation.
	ag := Agreement{}
	modeMap := make(map[string]*ModeStats)
	provMap := make(map[string]*ProvStats)
	ou := OutcomeStats{}
	cf := Counterfactual{}
	excl := map[string]int{}

	for _, o := range sorted {
		class, reasons := classify(o, knownProvider)
		for _, r := range reasons {
			excl[r]++
		}
		switch class {
		case ClassComplete:
			res.Dataset.Complete++
		case ClassNoOutcome:
			res.Dataset.NoOutcome++
		case ClassJEVError:
			res.Dataset.JEVError++
		case ClassInvalid:
			res.Dataset.Invalid++
		}
		res.Dataset.Total++

		// Outcome stratum (all rows, regardless of class).
		switch outcomeStratum(o.Outcome) {
		case StratumSuccess:
			ou.Success++
		case StratumFailed:
			ou.Failed++
		case StratumSkipped:
			ou.Skipped++
		default:
			ou.Unknown++
		}
		ou.TotalKnown = ou.Success + ou.Failed + ou.Skipped

		// Measurable advisory = ok status, provider present, AND a usable
		// row. Computed after the classification above so invalid rows
		// never enter agreement.
		measurable := class != ClassInvalid && o.AdvisoryStatus == advOK && o.AdvisoryProvider != ""

		// Mode stats.
		modeKey := o.ResolvedMode
		if modeKey == "" {
			modeKey = "(none)"
		}
		ms := modeMap[modeKey]
		if ms == nil {
			ms = &ModeStats{Mode: modeKey}
			modeMap[modeKey] = ms
		}
		ms.Observations++
		if isKnownOutcome(o.Outcome) {
			ms.OutcomesKnown++
		}

		// Provider stats (selected provider).
		if o.SelectedProvider != "" {
			ps := provMap[o.SelectedProvider]
			if ps == nil {
				ps = &ProvStats{Provider: o.SelectedProvider}
				provMap[o.SelectedProvider] = ps
			}
			ps.Observations++
			ps.SelectedCount++
			if isKnownOutcome(o.Outcome) {
				ps.OutcomesKnown++
			}
			if o.Outcome == OutcomeSuccess {
				ps.OutcomesSuccess++
			}
		}

		// Agreement + counterfactual are only measurable when both sides
		// have a top choice AND the row is usable (not invalid).
		if measurable {
			ag.Measurable++
			ms.Measurable++
			if provMap[o.AdvisoryProvider] == nil {
				provMap[o.AdvisoryProvider] = &ProvStats{Provider: o.AdvisoryProvider}
			}
			provMap[o.AdvisoryProvider].AdvisedCount++
			if o.AdvisoryProvider == o.SelectedProvider {
				ag.Agreements++
				ms.Agreements++
				if provMap[o.SelectedProvider] != nil {
					provMap[o.SelectedProvider].Agreements++
				}
			} else {
				ag.Disagreements++
				ms.Disagreements++
				if provMap[o.SelectedProvider] != nil {
					provMap[o.SelectedProvider].Disagreements++
				}
			}

			// Counterfactual: only for complete rows with a success outcome
			// and a resolvable successful provider.
			if class == ClassComplete && o.Outcome == OutcomeSuccess {
				succProv := successfulProvider(o)
				if succProv == "" {
					cf.Unresolvable++
				} else {
					cf.Eligible++
					if o.AdvisoryProvider == succProv {
						cf.Matches++
					}
				}
			}
		}
	}

	res.Outcomes = ou

	// Rates.
	if ag.Measurable > 0 {
		ag.Rate = float64(ag.Agreements) / float64(ag.Measurable)
	}
	if cf.Eligible > 0 {
		cf.Rate = float64(cf.Matches) / float64(cf.Eligible)
	}
	res.Agreement = ag
	res.Counterfactual = cf

	// Mode/provider rate fills.
	res.ByMode = sliceOf(modeMap)
	res.ByProvider = sliceOf(provMap)
	for _, m := range res.ByMode {
		if m.Measurable > 0 {
			m.AgreementRate = float64(m.Agreements) / float64(m.Measurable)
		}
	}

	// Calibration + global losses over measurable rows with a valid
	// distribution and a known outcome.
	e.computeCalibration(sorted, &res)

	// Exclusion list, sorted deterministically.
	for r, c := range excl {
		dq.Exclusions = append(dq.Exclusions, Exclusion{Reason: r, Count: c})
	}
	sort.Slice(dq.Exclusions, func(i, j int) bool {
		if dq.Exclusions[i].Reason != dq.Exclusions[j].Reason {
			return dq.Exclusions[i].Reason < dq.Exclusions[j].Reason
		}
		return false
	})
	res.DataQuality = dq

	// Outcome coverage.
	if res.Dataset.Total > 0 {
		res.Dataset.OutcomeCoverage = float64(res.Dataset.Complete) / float64(res.Dataset.Total)
	}

	// Verdict: evidence-sufficiency label, not a quality judgment.
	res.Verdict = verdict(res.Dataset.Complete)

	return res
}

// successfulProvider returns the provider associated with the observed
// successful outcome, or "" when it cannot be established from the data.
//
// The shadow observation records only the deterministic selected provider.
// When a request succeeds on the selected provider, the selected provider
// IS the observed-successful provider. The data cannot currently
// distinguish "succeeded on selected provider" from "selected provider
// failed and a fallback provider succeeded" — there is no
// fallback-provider field. This is precisely why the counterfactual is
// named successful_observed_provider_match rather than "picked the
// winner": it measures whether JEV's top recommendation equals the
// provider on which the request was observed to succeed, and only for
// success outcomes. Failed/skipped outcomes have no successful provider
// to match, so they contribute nothing (and are not counted as
// unresolvable — unresolvable is reserved for success rows where the
// winner cannot be determined).
func successfulProvider(o Observation) string {
	if o.Outcome != OutcomeSuccess {
		return ""
	}
	// Success: the observed winner is the selected provider. This is the
	// only provider the data supports; a fallback (if any) is not recorded.
	return o.SelectedProvider
}

// computeCalibration fills res.Calibration, res.GlobalBrier, and
// res.GlobalLogLoss. It operates over measurable rows (advisory ok) that
// have a valid probability distribution and a known execution outcome.
func (e *Evaluator) computeCalibration(sorted []Observation, res *Result) {
	cfg := e.cfg
	edges := cfg.BucketEdges
	// Build bucket list from edges.
	type acc struct {
		count      int
		success    int
		probSum    float64
		probN      int
		brierNum   float64
		brierN     int
		loglossNum float64
		loglossN   int
	}
	buckets := make([]*acc, len(edges))
	for i := range buckets {
		buckets[i] = &acc{}
	}

	var globalBrierNum, globalBrierDen float64
	var globalLogNum, globalLogDen float64

	for _, o := range sorted {
		if o.AdvisoryStatus != advOK || o.AdvisoryProvider == "" {
			continue
		}
		if !isKnownOutcome(o.Outcome) {
			continue
		}
		probs, ok := parseProbabilities(o.AdvisoryProbabilities)
		if !ok {
			continue
		}
		// For calibration we need the probability JEV assigned to the
		// provider the deterministic router selected (the observed
		// winner). For success outcomes the selected provider IS the
		// winner; for failed/skipped the selected provider is the one
		// that was tried (the positive-class event is "selected provider
		// succeeds", so y=1 iff outcome is success and pWin is the
		// probability assigned to the selected provider).
		succProv := o.SelectedProvider
		var pWin float64
		hasP := false
		if len(probs) > 0 && succProv != "" {
			pWin, hasP = probs[succProv]
		}
		// y = 1 if the observed outcome is success (the positive class).
		y := 0.0
		if o.Outcome == OutcomeSuccess {
			y = 1.0
		}

		// Assign to confidence bucket when confidence is present; a row
		// with no confidence cannot be bucketed by confidence, so it is
		// excluded from the confidence-bucket table but still counted
		// toward global losses if it has a probability.
		var bi int
		if o.AdvisoryConfidence != nil {
			c := *o.AdvisoryConfidence
			bi = bucketIndex(c, edges)
			b := buckets[bi]
			b.count++
			if o.Outcome == OutcomeSuccess {
				b.success++
			}
			if hasP {
				b.probSum += pWin
				b.probN++
			}
			// Brier: (p - y)^2
			if hasP {
				d := pWin - y
				b.brierNum += d * d
				b.brierN++
			}
			// Log loss: -[y ln p + (1-y) ln (1-p)], guarding p in (0,1).
			if hasP && pWin > 0 && pWin < 1 {
				if y == 1 {
					b.loglossNum += -math.Log(pWin)
				} else {
					b.loglossNum += -math.Log(1 - pWin)
				}
				b.loglossN++
			}
		}

		// Global losses when a probability is available.
		if hasP {
			d := pWin - y
			globalBrierNum += d * d
			globalBrierDen++
			if pWin > 0 && pWin < 1 {
				if y == 1 {
					globalLogNum += -math.Log(pWin)
				} else {
					globalLogNum += -math.Log(1 - pWin)
				}
				globalLogDen++
			}
		}
	}

	// Materialize buckets deterministically.
	res.Calibration = make([]CalBucket, 0, len(buckets))
	for i, b := range buckets {
		low, high := bucketBounds(i, edges)
		cb := CalBucket{Low: low, High: high, Count: b.count}
		if b.count > 0 {
			cb.SuccessRate = float64(b.success) / float64(b.count)
		}
		if b.probN > 0 {
			cb.MeanProb = b.probSum / float64(b.probN)
		}
		if b.count < cfg.MinBucketSamples {
			cb.LowSample = true
		}
		if b.brierN > 0 {
			v := b.brierNum / float64(b.brierN)
			cb.Brier = &v
		}
		if b.loglossN > 0 {
			v := b.loglossNum / float64(b.loglossN)
			cb.LogLoss = &v
		}
		res.Calibration = append(res.Calibration, cb)
	}

	if globalBrierDen > 0 {
		v := globalBrierNum / float64(globalBrierDen)
		res.GlobalBrier = &v
	}
	if globalLogDen > 0 {
		v := globalLogNum / float64(globalLogDen)
		res.GlobalLogLoss = &v
	}
}

// bucketIndex maps a confidence value into the bucket list derived from
// edges. The final bucket is the last edge through 1.0.
func bucketIndex(c float64, edges []float64) int {
	if c < 0 {
		c = 0
	}
	if c > 1 {
		c = 1
	}
	for i, e := range edges {
		if c >= e && (i == len(edges)-1 || c < edges[i+1]) {
			return i
		}
	}
	return len(edges) - 1
}

// bucketBounds returns the [low, high) interval for bucket index i.
func bucketBounds(i int, edges []float64) (float64, float64) {
	if i < 0 || i >= len(edges) {
		return 0, 1
	}
	low := edges[i]
	high := 1.0
	if i+1 < len(edges) {
		high = edges[i+1]
	}
	return low, high
}

// verdict maps the complete-sample count to an evidence-sufficiency label.
func verdict(complete int) Verdict {
	switch {
	case complete >= minAdequateSamples:
		return VerdictAdequateEvidence
	case complete >= minLimitedSamples:
		return VerdictLimitedEvidence
	default:
		return VerdictInsufficientEvidence
	}
}

// knownModeSet returns the set of routing modes the router recognizes.
func knownModeSet() map[string]bool {
	m := make(map[string]bool)
	for _, s := range []string{
		"elite", "coding", "reasoning", "vision", "fast",
		"default", "planning", "agentic", "long_horizon",
	} {
		m[s] = true
	}
	return m
}

// sliceOf converts a stats map into a deterministic slice of pointers.
func sliceOf[V any](m map[string]*V) []*V {
	out := make([]*V, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	// deterministic: sort by the first string field we can key on.
	sort.Slice(out, func(i, j int) bool {
		return statPtrKey(out[i]) < statPtrKey(out[j])
	})
	return out
}

// statPtrKey returns a sort key for a stats pointer.
func statPtrKey(v any) string {
	switch s := v.(type) {
	case *ModeStats:
		return s.Mode
	case *ProvStats:
		return s.Provider
	default:
		return ""
	}
}

// JSON renders the result deterministically (stable field order, stable
// slice order).
func (r Result) JSON() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("eval: marshal result: %w", err)
	}
	return string(b), nil
}

// Text renders a compact human-readable report.
func (r Result) Text() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "JEV Shadow Offline Evaluation\n")
	fmt.Fprintf(&sb, "Verdict: %s\n", r.Verdict)
	fmt.Fprintf(&sb, "\nDataset: total=%d complete=%d no_outcome=%d jev_error=%d invalid=%d outcome_coverage=%.3f\n",
		r.Dataset.Total, r.Dataset.Complete, r.Dataset.NoOutcome,
		r.Dataset.JEVError, r.Dataset.Invalid, r.Dataset.OutcomeCoverage)
	fmt.Fprintf(&sb, "\nAgreement (measurable=%d): rate=%.3f agreements=%d disagreements=%d\n",
		r.Agreement.Measurable, r.Agreement.Rate,
		r.Agreement.Agreements, r.Agreement.Disagreements)
	if r.GlobalBrier != nil {
		fmt.Fprintf(&sb, "Global Brier: %.4f\n", *r.GlobalBrier)
	} else {
		fmt.Fprintf(&sb, "Global Brier: n/a (no probability data)\n")
	}
	if r.GlobalLogLoss != nil {
		fmt.Fprintf(&sb, "Global Log-Loss: %.4f\n", *r.GlobalLogLoss)
	} else {
		fmt.Fprintf(&sb, "Global Log-Loss: n/a (no probability data)\n")
	}
	fmt.Fprintf(&sb, "\nOutcomes: success=%d failed=%d skipped=%d unknown=%d\n",
		r.Outcomes.Success, r.Outcomes.Failed, r.Outcomes.Skipped, r.Outcomes.Unknown)
	fmt.Fprintf(&sb, "Counterfactual (successful_observed_provider_match): eligible=%d matches=%d unresolvable=%d",
		r.Counterfactual.Eligible, r.Counterfactual.Matches, r.Counterfactual.Unresolvable)
	if r.Counterfactual.Eligible > 0 {
		fmt.Fprintf(&sb, " rate=%.3f", r.Counterfactual.Rate)
	}
	fmt.Fprintf(&sb, "\n\nBy mode:\n")
	for _, m := range r.ByMode {
		fmt.Fprintf(&sb, "  %-16s obs=%d measurable=%d agreement=%.3f outcomes_known=%d\n",
			m.Mode, m.Observations, m.Measurable, m.AgreementRate, m.OutcomesKnown)
	}
	fmt.Fprintf(&sb, "\nBy provider:\n")
	for _, p := range r.ByProvider {
		fmt.Fprintf(&sb, "  %-16s obs=%d selected=%d advised=%d agree=%d disagree=%d outcomes_success=%d\n",
			p.Provider, p.Observations, p.SelectedCount, p.AdvisedCount,
			p.Agreements, p.Disagreements, p.OutcomesSuccess)
	}
	fmt.Fprintf(&sb, "\nConfidence buckets (min_samples=%d):\n", r.Config.MinBucketSamples)
	for _, b := range r.Calibration {
		fmt.Fprintf(&sb, "  [%.1f, %.1f) n=%d success_rate=%.3f mean_prob=%.3f",
			b.Low, b.High, b.Count, b.SuccessRate, b.MeanProb)
		if b.LowSample {
			fmt.Fprintf(&sb, " (low sample)")
		}
		if b.Brier != nil {
			fmt.Fprintf(&sb, " brier=%.4f", *b.Brier)
		}
		if b.LogLoss != nil {
			fmt.Fprintf(&sb, " logloss=%.4f", *b.LogLoss)
		}
		fmt.Fprintf(&sb, "\n")
	}
	fmt.Fprintf(&sb, "\nData quality: dup_ids=%d malformed_probs=%d non_normalized_probs=%d candidate_mismatches=%d unknown_providers=%d unknown_modes=%d\n",
		r.DataQuality.DuplicateDecisionIDs, r.DataQuality.MalformedProbs, r.DataQuality.NonNormalizedProbs,
		r.DataQuality.CandidateMismatches, r.DataQuality.UnknownProviders, r.DataQuality.UnknownModes)
	for _, ex := range r.DataQuality.Exclusions {
		fmt.Fprintf(&sb, "  excluded[%s]=%d\n", ex.Reason, ex.Count)
	}
	return sb.String()
}
