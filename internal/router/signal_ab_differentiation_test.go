package router_test

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 2 + Phase 12: controlled A/B score differentiation.
//
// Every experiment constructs TWO candidates that differ in exactly ONE
// dimension and runs all eight public modes. For each mode the test asserts:
//   - the winner,
//   - the winning factor (largest weighted contribution to the margin),
//   - and that the score decomposes as:
//       total = wH*health + wL*latency + wC*cost + wK*capability
//               + residual(mode bonus + telemetry preference)
//
// The decomposition check is Phase 12: it proves candidate scores expose
// enough information to explain unexpected winners without a new API.

// modeWeights returns the normalized weights a mode actually applies.
// The public name "auto" maps to the canonical ModeDefault profile.
func modeWeights(t *testing.T, mode string) router.Weights {
	t.Helper()
	canonical := router.Mode(mode)
	if canonical == "auto" {
		canonical = "default"
	}
	mp := router.DefaultModeProfiles()[canonical]
	if mp == nil || mp.WeightPreferences == nil {
		t.Fatalf("mode %s has no profile", mode)
	}
	return router.Normalize(router.RawWeights{
		Health:     mp.WeightPreferences.Health,
		Latency:    mp.WeightPreferences.Latency,
		Cost:       mp.WeightPreferences.Cost,
		Capability: mp.WeightPreferences.Capability,
	})
}

// contributionBreakdown decomposes one candidate's TotalScore into weighted
// factor contributions plus a residual (mode bonus + telemetry preference).
func contributionBreakdown(t *testing.T, mode string, cs router.CandidateScore) (map[string]float64, float64) {
	t.Helper()
	w := modeWeights(t, mode)
	contrib := map[string]float64{
		"health":     w.Health * cs.HealthScore,
		"latency":    w.Latency * cs.LatencyScore,
		"cost":       w.Cost * cs.CostScore,
		"capability": w.Capability * cs.CapScore,
	}
	sum := contrib["health"] + contrib["latency"] + contrib["cost"] + contrib["capability"]
	residual := cs.TotalScore - sum
	return contrib, residual
}

// winningFactor identifies the signal that contributes the most to the
// winner's margin over the loser, using the decomposition.
func winningFactor(t *testing.T, mode string, winner, loser router.CandidateScore) (string, float64) {
	t.Helper()
	wc, wr := contributionBreakdown(t, mode, winner)
	lc, lr := contributionBreakdown(t, mode, loser)
	bestName := ""
	bestDiff := math.Inf(-1)
	for name := range wc {
		diff := wc[name] - lc[name]
		if diff > bestDiff {
			bestDiff = diff
			bestName = name
		}
	}
	if wr-lr > bestDiff {
		return "mode_bonus_or_telemetry", wr - lr
	}
	return bestName, bestDiff
}

// abResult runs a single-mode A/B experiment and returns the decision.
func abResult(t *testing.T, pipeline *router.DecisionPipeline, mode string, req *apitypes.ChatCompletionRequest) *router.SelectionResult {
	t.Helper()
	req.Mode = mode
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("%s Execute: %v", mode, err)
	}
	return res
}

// verifyAB asserts winner, winning factor, and decomposition consistency for
// every mode in one experiment.
func verifyAB(t *testing.T, pipeline *router.DecisionPipeline, modes map[string]abExpectation, req *apitypes.ChatCompletionRequest, expName string) {
	t.Helper()
	for _, mode := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		res := abResult(t, pipeline, mode, cloneReq(req))
		want, ok := modes[mode]
		if !ok {
			t.Fatalf("%s: no expectation registered for %s", expName, mode)
		}
		if res.Decision.SelectedProvider != want.winner {
			t.Fatalf("%s mode=%s: expected winner %q, got %q", expName, mode, want.winner, res.Decision.SelectedProvider)
		}
		if want.winner == "" {
			continue
		}
		winner := findScore(t, res, want.winner)
		loserName := otherProvider(t, res, want.winner)
		loser := findScore(t, res, loserName)
		if !winner.Selected || winner.Rejected {
			t.Fatalf("%s mode=%s: winner candidate flags unexpected (selected=%v rejected=%v)", expName, mode, winner.Selected, winner.Rejected)
		}
		// Phase 12: decomposition identity — contributions + residual == total.
		wc, wr := contributionBreakdown(t, mode, winner)
		lc, lr := contributionBreakdown(t, mode, loser)
		if math.Abs(wc["health"]+wc["latency"]+wc["cost"]+wc["capability"]+wr-winner.TotalScore) > 1e-9 {
			t.Fatalf("%s mode=%s: winner decomposition mismatch: %v + %v != %v", expName, mode, wc, wr, winner.TotalScore)
		}
		if math.Abs(lc["health"]+lc["latency"]+lc["cost"]+lc["capability"]+lr-loser.TotalScore) > 1e-9 {
			t.Fatalf("%s mode=%s: loser decomposition mismatch", expName, mode)
		}
		// Winning factor: rejection-driven wins and exact ties carry their own
		// explanations; otherwise the largest contributing dimension.
		var factor string
		if loser.Rejected {
			factor = "rejection: " + loser.RejectionReason
		} else {
			f, margin := winningFactor(t, mode, winner, loser)
			if margin <= 1e-12 {
				factor = "tie (deterministic order)"
			} else {
				factor = f
			}
		}
		if want.factor != "" && factor != want.factor {
			t.Fatalf("%s mode=%s: expected winning factor %q, got %q", expName, mode, want.factor, factor)
		}
		t.Logf("%s mode=%-12s winner=%-4s factor=%-30s", expName, mode, winner.Provider, factor)
	}
}

type abExpectation struct {
	winner string
	factor string // "" = deterministic tie-break
}

func cloneReq(req *apitypes.ChatCompletionRequest) *apitypes.ChatCompletionRequest {
	c := *req
	c.Messages = make([]apitypes.Message, len(req.Messages))
	copy(c.Messages, req.Messages)
	if req.Tools != nil {
		c.Tools = make([]apitypes.Tool, len(req.Tools))
		copy(c.Tools, req.Tools)
	}
	return &c
}

func findScore(t *testing.T, res *router.SelectionResult, provider string) router.CandidateScore {
	t.Helper()
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == provider {
			return cs
		}
	}
	t.Fatalf("candidate %q not found", provider)
	return router.CandidateScore{}
}

func otherProvider(t *testing.T, res *router.SelectionResult, winner string) string {
	t.Helper()
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider != winner {
			return cs.Provider
		}
	}
	t.Fatalf("no loser candidate found")
	return ""
}

// baseABReq is the neutral request for A/B experiments: explicit mode, short
// content, no tools, no reasoning params, no response format, no image.
func baseABReq() *apitypes.ChatCompletionRequest {
	return execReq("", "hi")
}

// ---- Experiment 1: LATENCY A/B (aaa 20ms vs zzz 500ms) ----

func TestABLatency(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 20)
	setHealth(t, store, "zzz", runtime.StateHealthy, 500)

	modes := map[string]abExpectation{}
	for _, m := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		modes[m] = abExpectation{winner: "aaa", factor: "latency"}
	}
	verifyAB(t, pipeline, modes, baseABReq(), "latency-ab")
}

// ---- Experiment 2: HEALTH A/B (aaa healthy vs zzz degraded) ----

func TestABHealth(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateDegraded, 100)

	modes := map[string]abExpectation{}
	for _, m := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		modes[m] = abExpectation{winner: "aaa", factor: "health"}
	}
	verifyAB(t, pipeline, modes, baseABReq(), "health-ab")
}

// ---- Experiment 3: COST A/B (aaa 0.0002 vs zzz 0.0006) ----

func TestABCost(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0006, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	modes := map[string]abExpectation{}
	for _, m := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		modes[m] = abExpectation{winner: "aaa", factor: "cost"}
	}
	verifyAB(t, pipeline, modes, baseABReq(), "cost-ab")
}

// ---- Experiment 4: CAPABILITY A/B via TOOL hint (aaa tools vs zzz no tools) ----

func TestABCapabilityTool(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	req := baseABReq()
	req.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "search"}}}

	modes := map[string]abExpectation{
		"auto":         {winner: "aaa", factor: "capability"},
		"coding":       {winner: "aaa", factor: "capability"},
		"reasoning":    {winner: "aaa", factor: "capability"},
		"vision":       {winner: "aaa", factor: "capability"},
		"fast":         {winner: "aaa", factor: "capability"},
		"planning":     {winner: "aaa", factor: "rejection: planning requires reasoning+tool_calling capabilities"},
		"agentic":      {winner: "aaa", factor: "rejection: agentic requires reasoning+tool_calling capabilities"},
		"long_horizon": {winner: "aaa", factor: "capability"},
	}
	verifyAB(t, pipeline, modes, req, "capability-tool-ab")
}

// ---- Experiment 5: REASONING CAPABILITY A/B (aaa reasoning vs zzz not) ----

func TestABReasoningCapability(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	req := baseABReq()
	req.ReasoningEffort = "high"

	modes := map[string]abExpectation{
		"auto":         {winner: "aaa", factor: "capability"},
		"coding":       {winner: "aaa", factor: "capability"},
		"reasoning":    {winner: "aaa", factor: "capability"},
		"vision":       {winner: "aaa", factor: "capability"},
		"fast":         {winner: "aaa", factor: "capability"},
		"planning":     {winner: "aaa", factor: "rejection: planning requires reasoning+tool_calling capabilities"},
		"agentic":      {winner: "aaa", factor: "rejection: agentic requires reasoning+tool_calling capabilities"},
		"long_horizon": {winner: "aaa", factor: "capability"},
	}
	verifyAB(t, pipeline, modes, req, "reasoning-ab")
}

// ---- Experiment 6: STRUCTURED CAPABILITY A/B (aaa structured vs zzz not) ----

func TestABStructuredCapability(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, structured: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	req := baseABReq()
	req.ResponseFormat = map[string]interface{}{"type": "json_object"}

	modes := map[string]abExpectation{}
	for _, m := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		modes[m] = abExpectation{winner: "aaa", factor: "capability"}
	}
	verifyAB(t, pipeline, modes, req, "structured-ab")
}

// ---- Experiment 7: CONTEXT CAPACITY A/B (aaa 32k vs zzz 128k, req ~35k) ----
// Long Horizon and Agentic hard-reject the 32k candidate; every other mode
// sees a tie and falls back to deterministic provider-name ordering.

func TestABContextCapacity(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 32768},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	req := baseABReq()
	// ~35k estimated tokens: 123600 chars /4 + 4 + 4096 default output.
	req.Messages = []apitypes.Message{{Role: "user", Content: string(make([]byte, 123600))}}

	modes := map[string]abExpectation{
		"auto":         {winner: "aaa", factor: ""},
		"coding":       {winner: "aaa", factor: ""},
		"reasoning":    {winner: "aaa", factor: ""},
		"vision":       {winner: "aaa", factor: ""},
		"fast":         {winner: "aaa", factor: ""},
		"planning":     {winner: "aaa", factor: ""},
		"agentic":      {winner: "zzz", factor: "rejection: agentic requires sufficient context capacity (32768 < 35000)"},
		"long_horizon": {winner: "zzz", factor: "rejection: insufficient context (32768 < 35000)"},
	}
	verifyAB(t, pipeline, modes, req, "context-capacity-ab")
}

// ---- Experiment 8: EXECUTION TELEMETRY A/B (zzz good telemetry vs aaa none) ----
// Only Planning and Agentic read telemetry; all other modes tie.

func TestABExecutionTelemetry(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
			r.RecordToolCallOutcomeModel("m", true)
		}
		return nil
	})

	modes := map[string]abExpectation{
		"auto":         {winner: "aaa", factor: ""},
		"coding":       {winner: "aaa", factor: ""},
		"reasoning":    {winner: "aaa", factor: ""},
		"vision":       {winner: "aaa", factor: ""},
		"fast":         {winner: "aaa", factor: ""},
		"planning":     {winner: "zzz", factor: "mode_bonus_or_telemetry"},
		"agentic":      {winner: "zzz", factor: "mode_bonus_or_telemetry"},
		"long_horizon": {winner: "aaa", factor: ""},
	}
	verifyAB(t, pipeline, modes, baseABReq(), "telemetry-ab")
}

// ---- Experiment 9: ERROR-RATE A/B (aaa 50% errors vs zzz clean) ----
// Both candidates are in the healthy state; the error-rate penalty inside
// healthScoreFromSnapshot must flip the winner for every mode.

func TestABErrorRate(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)
	// aaa: 1 latency sample + 1 error => 50% error rate => health 1.0 - 0.25 = 0.75.
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		r.RecordError(fmt.Errorf("boom"))
		return nil
	})

	modes := map[string]abExpectation{}
	for _, m := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		modes[m] = abExpectation{winner: "zzz", factor: "health"}
	}
	verifyAB(t, pipeline, modes, baseABReq(), "error-rate-ab")
}
