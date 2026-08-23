package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 8: Coding/Reasoning capability composition.
//
// Both modes reward capability hints with soft bonuses (no hard rejection):
//   - coding    .25 tool + .15 reasoning + .10 structured
//   - reasoning .35 reasoning
//
// A request that asks for ALL THREE hints (tools, reasoning controls,
// structured output) composes the bonuses and can flip selection toward the
// capability-strong provider even when it is slower and more expensive.

func hintReq(mode string) *apitypes.ChatCompletionRequest {
	req := execReq(mode, "do it")
	req.Tools = []apitypes.Tool{{Type: "function", Function: apitypes.FunctionDef{Name: "f"}}}
	req.ReasoningEffort = "high"
	req.ResponseFormat = map[string]interface{}{"type": "json_object"}
	return req
}

func winnerAndResidual(t *testing.T, pipeline *router.DecisionPipeline, mode string, req *apitypes.ChatCompletionRequest) (string, map[string]float64) {
	t.Helper()
	c := cloneReq(req)
	c.Mode = mode
	res, err := pipeline.Execute(context.Background(), c, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	residuals := map[string]float64{}
	for _, cs := range res.Decision.CandidateScores {
		_, residual := contributionBreakdown(t, mode, cs)
		residuals[cs.Provider] = residual
	}
	return res.Decision.SelectedProvider, residuals
}

// TestComposedHintsFlipSelection: with all three hints, coding mode
// composes .25+.15+.10 = .50 bonus; the capability-strong provider beats the
// healthy-but-weak provider.
func TestComposedHintsFlipSelection(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0005, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 500)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	winner, residuals := winnerAndResidual(t, pipeline, "coding", hintReq("coding"))
	if winner != "aaa" {
		t.Fatalf("coding with all hints: expected aaa (capability-strong), got %q", winner)
	}
	// zzz is penalized by matchScore (0.3) but NOT rejected.
	if got, want := residuals["zzz"], 0.0; abs(got-want) > 1e-9 {
		t.Fatalf("coding zzz: no hints match -> residual must be %f, got %f", want, got)
	}
	if got, want := residuals["aaa"], 0.05*(0.25+0.15+0.10); abs(got-want) > 1e-9 {
		t.Fatalf("coding aaa bonus: expected %f, got %f", want, got)
	}
}

// TestComposedHintsReasoning: the same all-hints request under reasoning
// mode applies only the .35 reasoning bonus.
func TestComposedHintsReasoning(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0005, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 500)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	winner, residuals := winnerAndResidual(t, pipeline, "reasoning", hintReq("reasoning"))
	if winner != "aaa" {
		t.Fatalf("reasoning with all hints: expected aaa, got %q", winner)
	}
	if got, want := residuals["aaa"], 0.05*0.35; abs(got-want) > 1e-9 {
		t.Fatalf("reasoning aaa bonus: expected %f, got %f", want, got)
	}
}

// TestCodingNoHardReject: coding with tool hint on a tool-less provider is
// a SOFT preference — the provider stays eligible (Rejected=false, CapScore
// floor 0.3). Contrast with planning, which hard-rejects on the same setup
// (covered in the Phase 4 pair tests). Note: capability dominates so hard the
// capability-strong provider still wins even when UNHEALTHY — a finding.
func TestCodingNoHardReject(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0005, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateUnhealthy, 500)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	c := cloneReq(hintReq("coding"))
	res, err := pipeline.Execute(context.Background(), c, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var zzz *router.CandidateScore
	for i := range res.Decision.CandidateScores {
		if res.Decision.CandidateScores[i].Provider == "zzz" {
			zzz = &res.Decision.CandidateScores[i]
		}
	}
	if zzz == nil {
		t.Fatal("zzz must be present in candidate list")
	}
	if zzz.Rejected {
		t.Fatal("coding tool hint must not hard-reject a tool-less provider")
	}
	if got, want := zzz.CapScore, 0.3; abs(got-want) > 1e-9 {
		t.Fatalf("zzz CapScore floor: expected %f, got %f", want, got)
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("coding: expected aaa (capability-strong wins even unhealthy), got %q", res.Decision.SelectedProvider)
	}
}

// TestReasoningNoHardReject: same soft-preference contract for reasoning.
func TestReasoningNoHardReject(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0005, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateUnhealthy, 500)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	c := cloneReq(hintReq("reasoning"))
	res, err := pipeline.Execute(context.Background(), c, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for i := range res.Decision.CandidateScores {
		cs := res.Decision.CandidateScores[i]
		if cs.Provider == "zzz" && cs.Rejected {
			t.Fatal("reasoning hint must not hard-reject a reasoning-less provider")
		}
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("reasoning: expected aaa (capability-strong wins even unhealthy), got %q", res.Decision.SelectedProvider)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
