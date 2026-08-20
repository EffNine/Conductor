package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.12 telemetry precedence contract:
//
// Each telemetry signal (execution success, tool-call success, retry rate) is
// resolved INDEPENDENTLY:
//   - Model MEASURED (count >= minExecutionSample = 5): the model-level signal
//     is used — a measured-poor model signal NEVER falls back to provider data.
//   - Model UNKNOWN (no entry) or INSUFFICIENT (< 5): the provider-level
//     signal is used if it is MEASURED.
//   - Provider UNKNOWN/INSUFFICIENT: the signal is neutral.
//
// A model entry with observations in one dimension does not block provider
// data in another dimension.

// TestP312MeasuredPoorModelDoesNotFallBackToProvider locks the key rule: a
// MEASURED-poor model signal is authoritative. aaa has a poor model history
// (3/10) and an excellent provider history; if the router fell back to
// provider telemetry aaa would tie zzz (good model history) and win by
// name order — but the model signal must win, so zzz is selected.
func TestP312MeasuredPoorModelDoesNotFallBackToProvider(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// aaa: MEASURED-poor model history + MEASURED-good provider history.
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 3; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		for i := 0; i < 7; i++ {
			r.RecordExecutionOutcomeModel("m", false, 0)
		}
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	// zzz: MEASURED-good model history.
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		return nil
	})

	res, err := pipeline.Execute(context.Background(), execReq("agentic", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("expected zzz (measured-poor model signal must not fall back to good provider telemetry), got %s", res.Decision.SelectedProvider)
	}
}

// TestP312MeasuredGoodModelBeatsMeasuredGoodProvider locks the reverse side:
// a provider with a good provider-level history cannot outrank a provider
// whose model-level history for the routed model is good.
func TestP312MeasuredGoodModelBeatsMeasuredGoodProvider(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// aaa: MEASURED-good model history + MEASURED-poor provider history.
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		for i := 0; i < 3; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		for i := 0; i < 7; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})
	// zzz: no model history, MEASURED-good provider history.
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})

	res, err := pipeline.Execute(context.Background(), execReq("agentic", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("expected aaa (measured-good model signal beats provider fallback), got %s", res.Decision.SelectedProvider)
	}
}

// TestP312InsufficientModelNeutralWhenProviderInsufficient verifies that when
// BOTH levels are INSUFFICIENT the signal stays neutral and the deterministic
// tie-break decides (aaa).
func TestP312InsufficientModelNeutralWhenProviderInsufficient(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 2; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		return nil
	})
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 4; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		return nil
	})

	res, err := pipeline.Execute(context.Background(), execReq("agentic", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("expected aaa (both levels insufficient -> neutral, tie-break), got %s", res.Decision.SelectedProvider)
	}
}

// TestP312UnknownModelNeutralWhenProviderUnknown verifies that when neither
// level has ANY telemetry the signal is neutral (tie-break decides).
func TestP312UnknownModelNeutralWhenProviderUnknown(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	res, err := pipeline.Execute(context.Background(), execReq("agentic", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("expected aaa (unknown both levels -> neutral, tie-break), got %s", res.Decision.SelectedProvider)
	}
}

// TestP312ToolSignalFallsBackIndependently verifies per-signal independence:
// aaa has a MEASURED model execution signal (blocks provider exec fallback)
// but NO model tool-call history, so the MEASURED-poor PROVIDER tool signal
// applies to aaa. With the old all-or-nothing switch aaa would tie zzz and
// win by name order; per-signal resolution must select zzz.
func TestP312ToolSignalFallsBackIndependently(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// aaa: MEASURED-good model executions, NO model tool calls, MEASURED-poor
	// provider tool calls (2/10).
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		for i := 0; i < 2; i++ {
			r.RecordToolCallOutcome(true)
		}
		for i := 0; i < 8; i++ {
			r.RecordToolCallOutcome(false)
		}
		return nil
	})
	// zzz: MEASURED-good model executions, no tool history at all.
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		return nil
	})

	res, err := pipeline.Execute(context.Background(), execReq("agentic", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("expected zzz (provider tool signal applies when model tool signal is unknown), got %s", res.Decision.SelectedProvider)
	}
}

// TestP312ProviderToolBonusAppliesWhenModelToolUnknown locks the positive
// side of per-signal independence: aaa's MEASURED-good provider tool history
// must contribute a bonus even though the model execution signal is measured
// at model level.
func TestP312ProviderToolBonusAppliesWhenModelToolUnknown(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// aaa: MEASURED-good model executions, no model tool calls, MEASURED-good
	// provider tool calls.
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		for i := 0; i < 10; i++ {
			r.RecordToolCallOutcome(true)
		}
		return nil
	})
	// zzz: MEASURED-good model executions, no tool history.
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		return nil
	})

	res, err := pipeline.Execute(context.Background(), execReq("agentic", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "aaa" {
		t.Fatalf("expected aaa (provider tool bonus applies when model tool signal unknown), got %s", res.Decision.SelectedProvider)
	}
}

// TestP312RetrySignalMeasuredPoorModelNoFallback verifies the retry signal
// follows the same precedence: aaa's MEASURED model history shows a 100%
// retry rate; the clean provider history must NOT mask it.
func TestP312RetrySignalMeasuredPoorModelNoFallback(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// aaa: 10 model executions, all successful, each with 1 retry (100%
	// retry rate); provider history is clean.
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 1)
		}
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	// zzz: clean model history.
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		return nil
	})

	res, err := pipeline.Execute(context.Background(), execReq("agentic", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("expected zzz (measured model retry signal must not fall back to clean provider history), got %s", res.Decision.SelectedProvider)
	}
}

// TestP312RetrySignalFallsBackWhenModelInsufficient verifies the retry signal
// falls back to provider data when the model execution signal is
// INSUFFICIENT: aaa's provider history is retry-heavy and must be penalized
// even though the model-level entry exists (but has too few samples).
func TestP312RetrySignalFallsBackWhenModelInsufficient(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// aaa: 2 model samples (INSUFFICIENT) but a provider history with 100%
	// retry rate.
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		r.RecordExecutionOutcomeModel("m", true, 0)
		r.RecordExecutionOutcomeModel("m", true, 0)
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 1)
		}
		return nil
	})

	res, err := pipeline.Execute(context.Background(), execReq("agentic", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("expected zzz (provider retry penalty applies when model signal insufficient), got %s", res.Decision.SelectedProvider)
	}
}

// TestP312TelemetryPreferenceAppliesInPlanningMode verifies the same
// precedence contract holds for Planning mode (intensity 1.0): a
// measured-poor model signal beats a fallback to measured-good provider data.
func TestP312TelemetryPreferenceAppliesInPlanningMode(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 3; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		for i := 0; i < 7; i++ {
			r.RecordExecutionOutcomeModel("m", false, 0)
		}
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		return nil
	})

	res, err := pipeline.Execute(context.Background(), execReq("planning", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("expected zzz (measured-poor model signal wins in planning mode), got %s", res.Decision.SelectedProvider)
	}
}
