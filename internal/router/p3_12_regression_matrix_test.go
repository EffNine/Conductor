package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.12 regression matrix: MODE x TELEMETRY.
//
// Two fixed providers differing ONLY in cost (zzz cheaper) and model
// telemetry (aaa scripted per profile):
//   - aaa: latency 100, cost 0.0005, maxContext 32768.
//   - zzz: latency 100, cost 0.0001, maxContext 131072.
//
// Deterministic expectations (verified by construction):
//   - Non-planning/agentic modes never read telemetry: zzz wins on cost.
//   - planning/agentic with no telemetry: zzz wins on cost.
//   - planning/agentic with MEASURED-good aaa telemetry (exec 10/10 + tool
//     10/10 -> +0.035 planning / +0.0525 agentic): aaa wins.
//   - planning/agentic with MEASURED-poor aaa telemetry: zzz wins (no
//     fallback to good provider data — aaa has none here).
//   - planning with INSUFFICIENT aaa model telemetry + MEASURED-good provider
//     telemetry: provider signal applies (+0.02), ties zzz's cost advantage
//     (+0.02), deterministic tie-break -> aaa.
//   - agentic with the same profile: aaa wins outright (+0.03 > +0.02).

type p312TelemetryProfile struct {
	name        string
	aaaModel    func(r runtime.ProviderRuntime) // nil = no model telemetry
	aaaProvider func(r runtime.ProviderRuntime) // nil = no provider telemetry
}

func p312Profiles() []p312TelemetryProfile {
	return []p312TelemetryProfile{
		{name: "none"},
		{
			name: "good-aaa",
			aaaModel: func(r runtime.ProviderRuntime) {
				for i := 0; i < 10; i++ {
					r.RecordExecutionOutcomeModel("m", true, 0)
					r.RecordToolCallOutcomeModel("m", true)
				}
			},
		},
		{
			name: "poor-aaa",
			aaaModel: func(r runtime.ProviderRuntime) {
				for i := 0; i < 3; i++ {
					r.RecordExecutionOutcomeModel("m", true, 0)
				}
				for i := 0; i < 7; i++ {
					r.RecordExecutionOutcomeModel("m", false, 0)
				}
			},
		},
		{
			name: "insufficient-aaa-plus-good-provider",
			aaaModel: func(r runtime.ProviderRuntime) {
				r.RecordExecutionOutcomeModel("m", true, 0)
				r.RecordExecutionOutcomeModel("m", true, 0)
			},
			aaaProvider: func(r runtime.ProviderRuntime) {
				for i := 0; i < 10; i++ {
					r.RecordExecutionOutcome(true, 0)
				}
			},
		},
	}
}

func TestP312RegressionMatrixModeTelemetry(t *testing.T) {
	modes := []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"}
	for _, profile := range p312Profiles() {
		for _, mode := range modes {
			t.Run(mode+"/"+profile.name, func(t *testing.T) {
				pipeline, store, _ := setupCalibPipeline(t,
					&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, maxContext: 32768, costPerUnit: 0.0005},
					&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, maxContext: 131072, costPerUnit: 0.0001},
				)
				setHealth(t, store, "aaa", runtime.StateHealthy, 100)
				setHealth(t, store, "zzz", runtime.StateHealthy, 100)
				if profile.aaaModel != nil {
					_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
						profile.aaaModel(r)
						return nil
					})
				}
				if profile.aaaProvider != nil {
					_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
						profile.aaaProvider(r)
						return nil
					})
				}

				res, err := pipeline.Execute(context.Background(), execReq(mode, "hi"), router.Environment{}, router.ConfigSnapshot{}, nil)
				if err != nil {
					t.Fatalf("Execute: %v", err)
				}

				want := "zzz"
				switch {
				case mode == "planning" && profile.name == "good-aaa":
					want = "aaa"
				case mode == "agentic" && profile.name == "good-aaa":
					want = "aaa"
				case mode == "planning" && profile.name == "insufficient-aaa-plus-good-provider":
					want = "aaa" // provider fallback ties cost; deterministic tie-break
				case mode == "agentic" && profile.name == "insufficient-aaa-plus-good-provider":
					want = "aaa"
				}
				if res.Decision.SelectedProvider != want {
					t.Fatalf("mode=%s profile=%s: expected %s, got %s", mode, profile.name, want, res.Decision.SelectedProvider)
				}
			})
		}
	}
}

// TestP312RegressionMatrixTelemetryIrrelevantOutsidePlanningAgentic locks the
// converse contract explicitly: telemetry NEVER influences selection in
// non-planning/agentic modes, even when the telemetry is the only difference
// between two otherwise identical candidates.
func TestP312RegressionMatrixTelemetryIrrelevantOutsidePlanningAgentic(t *testing.T) {
	modes := []string{"auto", "coding", "reasoning", "vision", "fast", "long_horizon"}
	for _, mode := range modes {
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, maxContext: 32768, costPerUnit: 0.0005},
			&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy, maxContext: 32768, costPerUnit: 0.0005},
		)
		setHealth(t, store, "aaa", runtime.StateHealthy, 100)
		setHealth(t, store, "zzz", runtime.StateHealthy, 100)
		_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
			for i := 0; i < 10; i++ {
				r.RecordExecutionOutcomeModel("m", true, 0)
			}
			return nil
		})

		res, err := pipeline.Execute(context.Background(), execReq(mode, "hi"), router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("%s Execute: %v", mode, err)
		}
		// Identical candidates: telemetry must not break the deterministic
		// tie-break (aaa) — and planning/agentic are excluded on purpose.
		if res.Decision.SelectedProvider != "aaa" {
			t.Fatalf("mode=%s: expected deterministic tie-break aaa (telemetry ignored), got %s", mode, res.Decision.SelectedProvider)
		}
	}
}
