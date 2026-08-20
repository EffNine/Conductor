package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 3: mode pair differentiation.
//
// For each pair a realistic candidate configuration is constructed where the
// semantic difference between the modes changes the winner. A pair that cannot
// be separated under ANY realistic configuration would be flagged redundant —
// every pair below separates, which is the acceptance criterion.

func pairRun(t *testing.T, pipeline *router.DecisionPipeline, mode string, req *apitypes.ChatCompletionRequest) string {
	t.Helper()
	c := cloneReq(req)
	c.Mode = mode
	res, err := pipeline.Execute(context.Background(), c, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("%s Execute: %v", mode, err)
	}
	return res.Decision.SelectedProvider
}

// assertPair checks that two modes produce different winners in the scenario
// and that each winner matches its expectation.
func assertPair(t *testing.T, pipeline *router.DecisionPipeline, req *apitypes.ChatCompletionRequest, modeA, wantA, modeB, wantB, scenario string) {
	t.Helper()
	gotA := pairRun(t, pipeline, modeA, req)
	gotB := pairRun(t, pipeline, modeB, req)
	if gotA != wantA {
		t.Fatalf("%s: %s expected %q, got %q", scenario, modeA, wantA, gotA)
	}
	if gotB != wantB {
		t.Fatalf("%s: %s expected %q, got %q", scenario, modeB, wantB, gotB)
	}
	if gotA == gotB {
		t.Fatalf("%s: %s and %s must produce different winners (both %q)", scenario, modeA, modeB, gotA)
	}
}

// TestP313PairAutoVsCoding: with tools present, Coding prefers the tool-capable
// (but degraded) provider; Auto prefers the healthy provider without tools.
func TestP313PairAutoVsCoding(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateDegraded, 100)

	req := execReq("", "write a function")
	req.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "search"}}}
	assertPair(t, pipeline, req, "auto", "aaa", "coding", "zzz", "auto-vs-coding")
}

// TestP313PairAutoVsReasoning: with reasoning params, Reasoning prefers the
// reasoning-capable (but degraded) provider; Auto prefers the healthy provider.
func TestP313PairAutoVsReasoning(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateDegraded, 100)

	req := execReq("", "analyze this")
	req.ReasoningEffort = "high"
	assertPair(t, pipeline, req, "auto", "aaa", "reasoning", "zzz", "auto-vs-reasoning")
}

// TestP313PairAutoVsFast: on the classic latency/health tradeoff, Auto prefers
// the healthy-but-slow provider, Fast the degraded-but-fast provider.
func TestP313PairAutoVsFast(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 4900, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0005, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 4900)
	setHealth(t, store, "zzz", runtime.StateDegraded, 100)

	req := execReq("", "hi")
	assertPair(t, pipeline, req, "auto", "aaa", "fast", "zzz", "auto-vs-fast")
}

// TestP313PairCodingVsReasoning: with tools AND reasoning params present,
// Coding prefers the tool-capable provider, Reasoning the reasoning-capable
// provider — the only difference is which capability is present.
func TestP313PairCodingVsReasoning(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	req := execReq("", "implement a parser")
	req.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "search"}}}
	req.ReasoningEffort = "high"
	assertPair(t, pipeline, req, "coding", "zzz", "reasoning", "aaa", "coding-vs-reasoning")
}

// TestP313PairCodingVsPlanning: Planning hard-rejects a provider without tool
// calling; Coding only soft-scores it and happily selects it.
func TestP313PairCodingVsPlanning(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)

	req := execReq("", "plan a release")
	gotCoding := pairRun(t, pipeline, "coding", req)
	if gotCoding != "aaa" {
		t.Fatalf("coding-vs-planning: coding expected aaa, got %q", gotCoding)
	}
	gotPlanning := pairRun(t, pipeline, "planning", req)
	if gotPlanning != "" {
		t.Fatalf("coding-vs-planning: planning expected no selection (hard tool requirement), got %q", gotPlanning)
	}
}

// TestP313PairPlanningVsLongHorizon: a request requiring >32k rejects the
// 32k candidate under Long Horizon but not under Planning; Planning then wins
// on cost for the 32k provider.
func TestP313PairPlanningVsLongHorizon(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 32768},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0005, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	req := execReq("", "plan and execute")
	req.Messages = []apitypes.Message{{Role: "user", Content: string(make([]byte, 123600))}} // ~35k tokens
	assertPair(t, pipeline, req, "planning", "aaa", "long_horizon", "zzz", "planning-vs-longhorizon")
}

// TestP313PairReasoningVsLongHorizon: the reasoning-capable 32k provider wins
// under Reasoning; Long Horizon hard-rejects it for insufficient context.
func TestP313PairReasoningVsLongHorizon(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 32768},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0005, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	req := execReq("", "analyze this")
	req.Messages = []apitypes.Message{{Role: "user", Content: string(make([]byte, 123600))}}
	req.ReasoningEffort = "high"
	assertPair(t, pipeline, req, "reasoning", "aaa", "long_horizon", "zzz", "reasoning-vs-longhorizon")
}

// TestP313PairAgenticVsLongHorizon: Agentic hard-rejects the tool-less
// provider; Long Horizon (no tool requirement) prefers it on cost.
func TestP313PairAgenticVsLongHorizon(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0005, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	req := execReq("", "plan and execute")
	req.Messages = []apitypes.Message{{Role: "user", Content: string(make([]byte, 123600))}}
	assertPair(t, pipeline, req, "agentic", "zzz", "long_horizon", "aaa", "agentic-vs-longhorizon")
}

// TestP313PairFastVsVision: with a reasoning-params hint, Fast ignores the
// capability difference and picks the fast provider; Vision (which still
// carries a 25% capability weight) picks the reasoning-capable provider.
func TestP313PairFastVsVision(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 3000, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 3000)

	req := execReq("", "explain this diagram")
	req.ReasoningEffort = "high"
	assertPair(t, pipeline, req, "fast", "aaa", "vision", "zzz", "fast-vs-vision")
}
