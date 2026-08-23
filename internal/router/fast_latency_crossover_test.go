package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 6: Fast latency analysis.
//
// Fast (Health 55, Latency 40) is a latency preference SUBJECT TO health, not
// "lowest latency at all costs". The measured crossover:
//
//	latency gap between 20ms (score 1.0) and 500ms (score 0.93469) is 0.06531.
//	weighted: 0.40 * 0.06531 = 0.02612.
//	health gap needed to overcome it: 0.02612 / 0.55 = 0.0475.
//
// So a 25x latency difference is overcome by a 0.05 health-point difference.
// The tests below lock the boundary on both sides.

// setErrorHealth sets a healthy provider with the given error rate so the
// snapshot health score becomes 1.0 - rate*0.5.
func setErrorHealth(t *testing.T, store *runtime.RuntimeStore, name string, latencyMs int64, rate float64) {
	t.Helper()
	err := store.Update(name, func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(latencyMs)
		return nil
	})
	if err != nil {
		t.Fatalf("update %s: %v", name, err)
	}
	if rate <= 0 {
		return
	}
	total := int(1 / rate)
	if total < 2 {
		total = 2
	}
	err = store.Update(name, func(r runtime.ProviderRuntime) error {
		// The first Update above already recorded one latency sample.
		for i := 0; i < total-2; i++ {
			r.RecordLatency(latencyMs)
		}
		r.RecordError(nil)
		return nil
	})
	if err != nil {
		t.Fatalf("update %s errors: %v", name, err)
	}
}

func fastWinner(t *testing.T, pipeline *router.DecisionPipeline, mode string) string {
	t.Helper()
	req := execReq(mode, "hi")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res.Decision.SelectedProvider
}

// TestFastLatencyCrossover:
//   - A: 20ms, health 0.95 (error rate 0.10)
//   - B: 500ms, health 0.99 (error rate 0.02)
//
// Fast picks A: the health delta (0.04) is below the crossover (0.0475), so
// the latency preference wins.
func TestFastLatencyCrossover(t *testing.T) {
	// Scenario 1: B healthier by 0.04 but 25x slower — latency preference wins.
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setErrorHealth(t, store, "aaa", 20, 0.10)  // health 0.95
	setErrorHealth(t, store, "zzz", 500, 0.02) // health 0.99

	if w := fastWinner(t, pipeline, "fast"); w != "aaa" {
		t.Fatalf("fast: expected aaa (latency preference within health tolerance), got %q", w)
	}

	// Scenario 2: fresh pipeline; B fully healthy (health 1.0, no errors):
	// the 0.05 health delta crosses the 0.0475 threshold — Fast picks the
	// 25x slower provider. This locks the contract: Fast is NOT "lowest
	// latency at all costs".
	pipeline2, store2, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setErrorHealth(t, store2, "aaa", 20, 0.10)             // health 0.95
	setHealth(t, store2, "zzz", runtime.StateHealthy, 500) // health 1.00

	if w := fastWinner(t, pipeline2, "fast"); w != "zzz" {
		t.Fatalf("fast: expected zzz (health 0.05 delta overcomes 25x latency gap), got %q", w)
	}
}

// TestFastReverseHealth: when the fast provider is ALSO healthier, it wins
// on both dimensions.
func TestFastReverseHealth(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	setErrorHealth(t, store, "aaa", 20, 0.02)  // health 0.99
	setErrorHealth(t, store, "zzz", 500, 0.10) // health 0.95

	if w := fastWinner(t, pipeline, "fast"); w != "aaa" {
		t.Fatalf("fast: expected aaa (both dimensions), got %q", w)
	}
}

// TestFastCrossoverAboveAuto locks the latency-sensitivity ordering:
// Fast flips only at a 0.0475 health delta, Auto already at 0.0408. Fast's
// higher latency weight (0.40 vs 0.25) dominates its higher health weight
// (0.55 vs 0.40), so Fast is measurably MORE latency-sensitive than Auto.
func TestFastCrossoverAboveAuto(t *testing.T) {
	// Exact crossover for the 20ms vs 500ms latency pair.
	const latencyGap = 0.06530612244897959
	fastCrossover := latencyGap * 0.40 / 0.55
	autoCrossover := latencyGap * 0.25 / 0.40
	if fastCrossover <= autoCrossover {
		t.Fatalf("fast crossover %f must exceed auto crossover %f", fastCrossover, autoCrossover)
	}
	t.Logf("fast crossover health delta = %f, auto crossover = %f", fastCrossover, autoCrossover)

	// Behavioral check at health delta ~0.0455 (error rate 1/11): between the
	// two crossovers — Auto flips to zzz, Fast still prefers aaa.
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 20, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0002, maxContext: 128000},
	)
	// aaa: 10 latency samples + 1 error => error rate 1/11 => health 0.9545.
	setErrorHealth(t, store, "aaa", 20, 1.0/11.0)
	setHealth(t, store, "zzz", runtime.StateHealthy, 500) // health 1.0
	// delta = 0.04545: below fast crossover (0.0475), above auto (0.0408).

	if w := fastWinner(t, pipeline, "fast"); w != "aaa" {
		t.Fatalf("fast: expected aaa at delta 0.0455 (< fast crossover), got %q", w)
	}
	if w := fastWinner(t, pipeline, "auto"); w != "zzz" {
		t.Fatalf("auto: expected zzz at delta 0.0455 (> auto crossover), got %q", w)
	}
}
