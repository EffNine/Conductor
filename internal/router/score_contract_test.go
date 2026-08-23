package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 12: white-box score contribution contract.
//
// For every mode profile the composite score must be exactly
//
//	TotalScore = wH*health + wL*latency + wC*cost + wK*capability
//	             + 0.05 * (mode capability bonuses that match)
//
// with factor scores clamped to [0,1]. This test pins the arithmetic
// end-to-end: execute a real decision per mode with known inputs (latency
// >= 100ms so the raw latency score equals the clamped value), then recompute
// each candidate's TotalScore by hand from the trace and compare to 1e-9.

// TestCompositeScoreArithmetic verifies the exact composite formula
// against the trace for all eight active modes.
func TestCompositeScoreArithmetic(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 2000, healthState: runtime.StateDegraded, costPerUnit: 0.0009, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateDegraded, 2000)

	for _, mode := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		req := hintReq(mode)
		res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("%s Execute: %v", mode, err)
		}
		w := modeWeights(t, mode)
		canonical := router.Mode(mode)
		if canonical == "auto" {
			canonical = "default"
		}
		mp := router.DefaultModeProfiles()[canonical]

		for _, name := range []string{"aaa", "zzz"} {
			cs := scoreOf(t, res, name)
			// CapScore is deterministic: aaa matches all hints (1.0), zzz
			// misses reasoning -> matchScore floor 0.3.
			var capScore float64
			// Candidate capabilities are static in this fixture.
			hasReasoning, hasTool, hasStructured, hasContext := false, false, false, true
			if name == "aaa" {
				capScore = 1.0
				hasReasoning, hasTool, hasStructured = true, true, true
			} else {
				capScore = 0.3
			}
			expected := w.Health*cs.HealthScore +
				w.Latency*cs.LatencyScore +
				w.Cost*cs.CostScore +
				w.Capability*capScore
			// Add the 0.05-weighted mode bonus for every bonus the
			// candidate's capabilities satisfy (engine gates bonuses on
			// capabilities, not on CapScore).
			if mp != nil {
				bonus := 0.0
				b := mp.CapabilityBonuses
				if b.Reasoning > 0 && hasReasoning {
					bonus += b.Reasoning
				}
				if b.ToolCalling > 0 && hasTool {
					bonus += b.ToolCalling
				}
				if b.Structured > 0 && hasStructured {
					bonus += b.Structured
				}
				if b.ContextCapacity > 0 && hasContext {
					bonus += b.ContextCapacity
				}
				expected += 0.05 * bonus
			}
			if diff := abs(cs.TotalScore - expected); diff > 1e-9 {
				t.Fatalf("mode=%s %s: TotalScore %f != hand-computed %f (diff %e)", mode, name, cs.TotalScore, expected, diff)
			}
		}
	}
}

// TestModeWeightsSumToOne: every active mode's normalized weights are a
// valid convex combination.
func TestModeWeightsSumToOne(t *testing.T) {
	for _, mode := range []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"} {
		w := modeWeights(t, mode)
		sum := w.Health + w.Latency + w.Cost + w.Capability
		if abs(sum-1.0) > 1e-9 {
			t.Fatalf("mode %s: weights sum %f, want 1.0 (%+v)", mode, sum, w)
		}
	}
}
