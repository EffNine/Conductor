package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// TestModelTelemetryOverridesProvider verifies telemetry precedence:
// when both model-level and provider-level executions exist, the model-level
// signal must be used (provider-level never overrides it). A provider with
// good provider history but bad model history for the routed model loses to
// one with the inverse history.
func TestModelTelemetryOverridesProvider(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// aaa: bad model-level history, good provider-level history.
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", false, 0)
		}
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	// zzz: good model-level history, bad provider-level history.
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("m", true, 0)
		}
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})

	res, err := pipeline.Execute(context.Background(), execReq("agentic", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("expected zzz (model-level telemetry wins over provider-level), got %s", res.Decision.SelectedProvider)
	}
}

// TestPartialModelTelemetryFallsBackToProvider verifies the P3.12
// precedence contract for INSUFFICIENT model telemetry: a model-level entry
// with fewer than minExecutionSample (5) observations is UNKNOWN/INSUFFICIENT,
// so the provider-level signal is used instead (measured-good provider
// telemetry applies and zzz wins).
func TestPartialModelTelemetryFallsBackToProvider(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// zzz: 2 model-level outcomes (present but below the 5-sample minimum)
	// plus a great provider-level history. With per-signal fallback the
	// provider telemetry applies and zzz must win.
	_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
		r.RecordExecutionOutcomeModel("m", true, 0)
		r.RecordExecutionOutcomeModel("m", true, 0)
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})

	res, err := pipeline.Execute(context.Background(), execReq("agentic", "build this"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "zzz" {
		t.Fatalf("expected zzz (insufficient model telemetry falls back to provider), got %s", res.Decision.SelectedProvider)
	}
}

// TestTelemetryProviderFallbackWhenModelAbsent verifies provider-level
// telemetry IS used when no model-level entry exists at all.
func TestTelemetryProviderFallbackWhenModelAbsent(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 100)
	setHealth(t, store, "zzz", runtime.StateHealthy, 100)

	// aaa: provider-level only (no model-level entries at all).
	_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
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
		t.Fatalf("expected aaa (provider-level fallback when model absent), got %s", res.Decision.SelectedProvider)
	}
}
