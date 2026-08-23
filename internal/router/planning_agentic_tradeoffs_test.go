package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 4: Planning vs Agentic behavioral separation.
//
// Planning (Health 40, Capability 45, tool +0.20, reason +0.25, telemetry 1.0)
// vs Agentic (Health 55, Capability 30, tool +0.30, reason +0.30, context
// +0.10, telemetry 1.5, context hard filter).
//
// Cases A–C construct candidates where the weight-profile difference flips the
// winner. Cases D–E isolate the telemetry dimension: both modes agree on the
// direction (good history wins) — only the margin scales by the 1.5x Agentic
// intensity. That asymmetry is the honest measured boundary.

func planAgentRun(t *testing.T, pipeline *router.DecisionPipeline, mode string, req *apitypes.ChatCompletionRequest) (string, float64) {
	t.Helper()
	c := cloneReq(req)
	c.Mode = mode
	res, err := pipeline.Execute(context.Background(), c, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("%s Execute: %v", mode, err)
	}
	best := 0.0
	bestName := ""
	for _, cs := range res.Decision.CandidateScores {
		if !cs.Rejected && cs.TotalScore > best {
			best = cs.TotalScore
			bestName = cs.Provider
		}
	}
	return bestName, best
}

// Case A: strong capability, mediocre reliability.
// Planning (capability 45) prefers the structured-capable degraded provider;
// Agentic (health 55) prefers the healthy provider without structured output.
func TestPlanningAgenticCaseAStrongCapabilityWeakReliability(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "cap-strong", supportsAll: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "rel-strong", supportsAll: true, reasoning: true, toolCalling: true, structured: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "cap-strong", runtime.StateDegraded, 100)
	setHealth(t, store, "rel-strong", runtime.StateHealthy, 100)

	req := execReq("", "plan and execute")
	req.ResponseFormat = map[string]interface{}{"type": "json_object"}

	planWin, _ := planAgentRun(t, pipeline, "planning", req)
	agentWin, _ := planAgentRun(t, pipeline, "agentic", req)
	if planWin != "cap-strong" {
		t.Fatalf("case A planning: expected cap-strong (capability 45 dominates), got %q", planWin)
	}
	if agentWin != "rel-strong" {
		t.Fatalf("case A agentic: expected rel-strong (health 55 dominates), got %q", agentWin)
	}
}

// Case B: strong reliability, mediocre capability (mirror of A).
func TestPlanningAgenticCaseBStrongReliabilityWeakCapability(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "rel-strong", supportsAll: true, reasoning: true, toolCalling: true, structured: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "cap-strong", supportsAll: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "rel-strong", runtime.StateHealthy, 100)
	setHealth(t, store, "cap-strong", runtime.StateDegraded, 100)

	req := execReq("", "plan and execute")
	req.ResponseFormat = map[string]interface{}{"type": "json_object"}

	planWin, _ := planAgentRun(t, pipeline, "planning", req)
	agentWin, _ := planAgentRun(t, pipeline, "agentic", req)
	if planWin != "cap-strong" {
		t.Fatalf("case B planning: expected cap-strong, got %q", planWin)
	}
	if agentWin != "rel-strong" {
		t.Fatalf("case B agentic: expected rel-strong, got %q", agentWin)
	}
}

// Case C: large context, weaker capability (health/latency vs capability).
// Planning's 45% capability weight prefers the structured-capable slow
// provider; Agentic's 55% health weight prefers the healthy fast provider.
func TestPlanningAgenticCaseCLargeContextWeakerCapability(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "ctx-strong", supportsAll: true, reasoning: true, toolCalling: true, structured: false, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "cap-strong", supportsAll: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "ctx-strong", runtime.StateHealthy, 20)
	setHealth(t, store, "cap-strong", runtime.StateDegraded, 500)

	req := execReq("", "plan and execute")
	req.ResponseFormat = map[string]interface{}{"type": "json_object"}

	planWin, _ := planAgentRun(t, pipeline, "planning", req)
	agentWin, _ := planAgentRun(t, pipeline, "agentic", req)
	if planWin != "cap-strong" {
		t.Fatalf("case C planning: expected cap-strong, got %q", planWin)
	}
	if agentWin != "ctx-strong" {
		t.Fatalf("case C agentic: expected ctx-strong, got %q", agentWin)
	}
}

// Case D: strong reasoning/tool calling, poor execution history.
// Both modes prefer the candidate with good execution history — the telemetry
// signal decides the winner outright because every other dimension ties.
// Agentic's margin must be 1.5x Planning's (intensity 1.5 vs 1.0).
func TestPlanningAgenticCaseDPoorExecutionHistory(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)
	// aaa: poor execution history (30% success, 10 samples — MEASURED).
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 3; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		for i := 0; i < 7; i++ {
			r.RecordExecutionOutcomeModel("m", false, 0)
		}
		return nil
	})
	// zzz: excellent execution history (100% success + 100% tool success).
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
			r.RecordToolCallOutcomeModel("m", true)
		}
		return nil
	})

	req := execReq("", "build this")
	planWin, planBest := planAgentRun(t, pipeline, "planning", req)
	agentWin, agentBest := planAgentRun(t, pipeline, "agentic", req)
	if planWin != "zzz" {
		t.Fatalf("case D planning: expected zzz (good history), got %q", planWin)
	}
	if agentWin != "zzz" {
		t.Fatalf("case D agentic: expected zzz (good history), got %q", agentWin)
	}
	// Telemetry-only margins: planning 0.06 (0.035 good - 0.025 poor),
	// agentic 0.09 (0.0525 good - 0.0375 poor) — exactly the 1.5x intensity.
	planMargin := planMarginBetween(t, pipeline, "planning", req)
	agentMargin := planMarginBetween(t, pipeline, "agentic", req)
	if planMargin < 0.055 || planMargin > 0.065 {
		t.Fatalf("case D planning margin %f outside [0.055, 0.065]", planMargin)
	}
	if agentMargin < 0.085 || agentMargin > 0.095 {
		t.Fatalf("case D agentic margin %f outside [0.085, 0.095]", agentMargin)
	}
	if mathAbs(agentMargin/planMargin-1.5) > 1e-9 {
		t.Fatalf("case D agentic/planning margin ratio %f != 1.5", agentMargin/planMargin)
	}
	t.Logf("case D planning margin=%.6f agentic margin=%.6f (best %f/%f)", planMargin, agentMargin, planBest, agentBest)
}

// planMarginBetween returns the score gap between the two candidates of the
// experiment for the given mode.
func planMarginBetween(t *testing.T, pipeline *router.DecisionPipeline, mode string, req *apitypes.ChatCompletionRequest) float64 {
	t.Helper()
	c := cloneReq(req)
	c.Mode = mode
	res, err := pipeline.Execute(context.Background(), c, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("%s Execute: %v", mode, err)
	}
	scores := map[string]float64{}
	for _, cs := range res.Decision.CandidateScores {
		scores[cs.Provider] = cs.TotalScore
	}
	if len(scores) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(scores))
	}
	a, b := 0.0, 0.0
	first := true
	for _, v := range scores {
		if first {
			a = v
			first = false
		} else {
			b = v
		}
	}
	if a > b {
		return a - b
	}
	return b - a
}

func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// Case E: excellent execution history, weaker reasoning profile.
// Both modes prefer the excellent-history provider even though it is slower;
// the telemetry edge (+0.035/+0.0525) outweighs the latency loss (~0.0065).
func TestPlanningAgenticCaseEExcellentHistoryWeakerReasoning(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "proven", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "fast", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setHealth(t, store, "proven", runtime.StateHealthy, 500)
	setHealth(t, store, "fast", runtime.StateHealthy, 100)
	_ = store.Update("proven", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
			r.RecordToolCallOutcomeModel("m", true)
		}
		return nil
	})

	req := execReq("", "build this")
	planWin, _ := planAgentRun(t, pipeline, "planning", req)
	agentWin, _ := planAgentRun(t, pipeline, "agentic", req)
	if planWin != "proven" {
		t.Fatalf("case E planning: expected proven (telemetry beats latency), got %q", planWin)
	}
	if agentWin != "proven" {
		t.Fatalf("case E agentic: expected proven (telemetry beats latency), got %q", agentWin)
	}
}
