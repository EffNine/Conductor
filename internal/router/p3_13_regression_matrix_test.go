package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"

	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 14: MODE x PRIMARY SIGNAL x COUNTER-SIGNAL regression matrix.
//
// Each row is a deterministic scenario where the mode's PRIMARY signal must
// beat a strong COUNTER signal. This is the consolidated regression net over
// the Phase 2-12 findings; any future change that weakens a mode's identity
// flips one of these rows.

type matrixRow struct {
	mode    string
	name    string
	primary string
}

func runMatrixRow(t *testing.T, row matrixRow) {
	t.Helper()
	var req *apitypes.ChatCompletionRequest
	switch row.name {
	case "health_vs_latency":
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 2000, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
			&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 20, healthState: runtime.StateDegraded, costPerUnit: 0.0002, maxContext: 128000},
		)
		setHealth(t, store, "aaa", runtime.StateHealthy, 2000)
		setHealth(t, store, "zzz", runtime.StateDegraded, 20)
		req = execReq(row.mode, "do it")
		assertMatrixWinner(t, pipeline, req, "aaa", row)
		return
	case "cost_vs_latency":
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 2000, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
			&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0009, maxContext: 128000},
		)
		setHealth(t, store, "aaa", runtime.StateHealthy, 2000)
		setHealth(t, store, "zzz", runtime.StateHealthy, 20)
		req = execReq(row.mode, "do it")
		assertMatrixWinner(t, pipeline, req, "aaa", row)
		return
	case "capability_vs_health":
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateUnhealthy, costPerUnit: 0.0005, maxContext: 128000},
			&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
		)
		setHealth(t, store, "aaa", runtime.StateUnhealthy, 500)
		setHealth(t, store, "zzz", runtime.StateHealthy, 100)
		req = hintReq(row.mode)
		assertMatrixWinner(t, pipeline, req, "aaa", row)
		return
	case "latency_vs_health":
		// Fast: 20ms/health 0.95 beats 500ms/health 0.99 (below crossover).
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
			&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		)
		setErrorHealth(t, store, "aaa", 20, 0.10)  // health 0.95
		setErrorHealth(t, store, "zzz", 500, 0.02) // health 0.99
		req = execReq(row.mode, "do it")
		assertMatrixWinner(t, pipeline, req, "aaa", row)
		return
	case "health_vs_latency_fast":
		// Fast: 500ms/health 1.0 beats 20ms/health 0.95 (above crossover).
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
			&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		)
		setErrorHealth(t, store, "aaa", 20, 0.10) // health 0.95
		setHealth(t, store, "zzz", runtime.StateHealthy, 500)
		req = execReq(row.mode, "do it")
		assertMatrixWinner(t, pipeline, req, "zzz", row)
		return
	case "vision_hard":
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "aaa", supportsAll: true, vision: false, reasoning: true, toolCalling: true, structured: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
			&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 2000, healthState: runtime.StateDegraded, costPerUnit: 0.0009, maxContext: 128000},
		)
		setHealth(t, store, "aaa", runtime.StateHealthy, 20)
		setHealth(t, store, "zzz", runtime.StateDegraded, 2000)
		req = imageReq(row.mode)
		assertMatrixWinner(t, pipeline, req, "zzz", row)
		return
	case "context_vs_latency":
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 16384},
			&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 2000, healthState: runtime.StateHealthy, costPerUnit: 0.0009, maxContext: 128000},
		)
		setHealth(t, store, "aaa", runtime.StateHealthy, 20)
		setHealth(t, store, "zzz", runtime.StateHealthy, 2000)
		req = reqForContext(t, row.mode, 32768)
		assertMatrixWinner(t, pipeline, req, "zzz", row)
		return
	case "telemetry_vs_latency":
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
			&calibStubProvider{name: "zzz", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		)
		setHealth(t, store, "aaa", runtime.StateHealthy, 20)
		setHealth(t, store, "zzz", runtime.StateHealthy, 500)
		// aaa: poor execution history (30% success). zzz: 100% success.
		_ = store.Update("aaa", func(r runtime.ProviderRuntime) error {
			for i := 0; i < 3; i++ {
				r.RecordExecutionOutcomeModel("m", true, 0)
			}
			for i := 0; i < 7; i++ {
				r.RecordExecutionOutcomeModel("m", false, 0)
			}
			return nil
		})
		_ = store.Update("zzz", func(r runtime.ProviderRuntime) error {
			for i := 0; i < 10; i++ {
				r.RecordExecutionOutcomeModel("m", true, 0)
			}
			r.RecordToolCallOutcomeModel("m", true)
			return nil
		})
		req = execReq(row.mode, "plan and execute")
		assertMatrixWinner(t, pipeline, req, "zzz", row)
		return
	default:
		t.Fatalf("unknown matrix row %q", row.name)
	}
}

func assertMatrixWinner(t *testing.T, pipeline *router.DecisionPipeline, req *apitypes.ChatCompletionRequest, want string, row matrixRow) {
	t.Helper()
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("%s %s Execute: %v", row.mode, row.name, err)
	}
	if got := res.Decision.SelectedProvider; got != want {
		t.Fatalf("%s %s: expected %q (%s primary signal), got %q", row.mode, row.name, want, row.primary, got)
	}
}

// TestP313RegressionMatrix is the MODE x PRIMARY SIGNAL x COUNTER-SIGNAL net.
func TestP313RegressionMatrix(t *testing.T) {
	rows := []matrixRow{
		{"auto", "health_vs_latency", "health"},
		{"auto", "cost_vs_latency", "cost"},
		{"coding", "capability_vs_health", "capability"},
		{"reasoning", "capability_vs_health", "capability"},
		{"fast", "latency_vs_health", "latency"},
		{"fast", "health_vs_latency_fast", "health"},
		{"vision", "vision_hard", "vision (hard filter)"},
		{"planning", "telemetry_vs_latency", "execution telemetry"},
		{"agentic", "telemetry_vs_latency", "execution telemetry"},
		{"long_horizon", "context_vs_latency", "context capacity"},
	}
	for _, row := range rows {
		runMatrixRow(t, row)
	}
}
