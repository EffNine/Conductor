package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.12 Planning vs Long Horizon semantics:
//
// Both modes share the same normalized routing weights (health 40, latency 10,
// cost 5, capability 45) and both require Reasoning+ToolCalling providers to
// be competitive, but they differ in three hard-coded ways:
//   - Planning applies the execution-telemetry preference (intensity 1.0);
//     Long Horizon does not.
//   - Long Horizon hard-rejects candidates whose known MaxContext is smaller
//     than the estimated request requirement; Planning has no context filter.
//   - Planning hard-requires Reasoning+ToolCalling; Long Horizon does not.
//
// These tests pin that planning can prefer a more reliable provider even at
// higher cost, while long_horizon prefers cost/context and enforces the
// context budget regardless of other advantages.

// setupPlanHorizonPipeline builds the canonical comparison pair:
//   - aaa: maxContext 32768, cost 0.0005, MEASURED-good model telemetry.
//   - zzz: maxContext 131072, cost 0.0001, MEASURED-poor model telemetry.
func setupPlanHorizonPipeline(t *testing.T) (*router.DecisionPipeline, *runtime.RuntimeStore) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, maxContext: 32768, costPerUnit: 0.0005},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, maxContext: 131072, costPerUnit: 0.0001},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		return nil
	})
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 3; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		for i := 0; i < 7; i++ {
			r.RecordExecutionOutcomeModel("m", false, 0)
		}
		return nil
	})
	return pipeline, store
}

// TestPlanningPrefersExecutionReliableProvider verifies planning uses the
// telemetry preference: aaa's measured-good model history outranks zzz's
// cheaper pricing and larger context.
func TestPlanningPrefersExecutionReliableProvider(t *testing.T) {
	pipeline, _ := setupPlanHorizonPipeline(t)

	res, err := pipeline.Execute(context.Background(), execReq("planning", "hi"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("expected aaa (planning prefers execution-reliable provider via telemetry), got %s", res.Decision.SelectedProvider)
	}
}

// TestLongHorizonPrefersCostAndContext verifies long_horizon ignores the
// telemetry preference: zzz wins on cost (both contexts satisfy the small
// request), despite its poor model telemetry.
func TestLongHorizonPrefersCostAndContext(t *testing.T) {
	pipeline, _ := setupPlanHorizonPipeline(t)

	res, err := pipeline.Execute(context.Background(), execReq("long_horizon", "hi"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("expected zzz (long_horizon ignores telemetry, prefers cost), got %s", res.Decision.SelectedProvider)
	}
}

// TestLongHorizonHardContextRejectsSmallContext verifies the context hard
// filter: a request whose estimated requirement (33005 tokens) exceeds aaa's
// 32768 context hard-rejects aaa even though it is the execution-reliable,
// competitive candidate.
func TestLongHorizonHardContextRejectsSmallContext(t *testing.T) {
	pipeline, _ := setupPlanHorizonPipeline(t)

	req := execReq("long_horizon", "hi")
	maxTokens := 33000
	req.MaxTokens = &maxTokens

	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("expected zzz (aaa hard-rejected by context filter), got %s", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "aaa" && !cs.Rejected {
			t.Fatal("aaa must be rejected for insufficient context in long_horizon")
		}
	}
}

// TestPlanningHasNoContextHardFilter verifies planning does NOT enforce a
// context budget: the same oversized request leaves aaa eligible and aaa wins
// on telemetry.
func TestPlanningHasNoContextHardFilter(t *testing.T) {
	pipeline, _ := setupPlanHorizonPipeline(t)

	req := execReq("planning", "hi")
	maxTokens := 33000
	req.MaxTokens = &maxTokens

	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("expected aaa (planning has no context hard filter), got %s", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "aaa" && cs.Rejected {
			t.Fatalf("aaa must NOT be rejected in planning mode: %s", cs.RejectionReason)
		}
	}
}

// TestPlanningAndAgenticRequireReasoningTooling locks the shared hard
// capability filter: planning and agentic reject a provider lacking either
// reasoning or tool-calling even when it dominates on every other axis.
func TestPlanningAndAgenticRequireReasoningTooling(t *testing.T) {
	for _, mode := range []string{"planning", "agentic"} {
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "plain", supportsAll: true, latencyMs: 10, healthState: runtime.StateHealthy, maxContext: 131072, costPerUnit: 0.0001},
			&calibStubProvider{name: "qualified", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 5000, healthState: runtime.StateDegraded, maxContext: 8192},
		)
		setHealth(t, store, "plain", runtime.StateHealthy, 10)
		setHealth(t, store, "qualified", runtime.StateDegraded, 5000)

		res, err := pipeline.Execute(context.Background(), execReq(mode, "hi"), router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("%s Execute: %v", mode, err)
		}
		if res.Decision.SelectedProvider != "qualified" {
			t.Fatalf("%s: expected qualified (only eligible), got %s", mode, res.Decision.SelectedProvider)
		}
		for _, cs := range res.Decision.CandidateScores {
			if cs.Provider == "plain" && !cs.Rejected {
				t.Fatalf("%s: plain must be rejected (missing reasoning+tool_calling)", mode)
			}
		}
	}
}
