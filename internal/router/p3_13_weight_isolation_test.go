package router_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.13 Phase 11: weight isolation and concurrency safety.
//
// Mode profiles are per-decision overrides: executing one mode must never
// mutate the engine's global weights or the shared profile table, and
// concurrent decisions in different modes must behave identically to serial
// execution.

// TestP313ModeWeightsArePerDecision: mode executions use per-decision weights
// without mutating the engine's global default weights. Requests carry
// tool+reasoning+structured hints so capability actually differentiates.
func TestP313ModeWeightsArePerDecision(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0005, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 500)
	setHealth(t, store, "zzz", runtime.StateHealthy, 50)

	modeWinner := func(mode string) string {
		t.Helper()
		req := hintReq(mode)
		res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		return res.Decision.SelectedProvider
	}

	eng := pipeline.RoutingEngine()
	before := eng.GetScorer().LoadWeights()

	// Coding: capability weight 0.60 -> capability-strong aaa wins.
	if w := modeWinner("coding"); w != "aaa" {
		t.Fatalf("coding: expected aaa, got %q", w)
	}
	// Fast: capability weight 0.02 -> healthy/fast zzz wins.
	if w := modeWinner("fast"); w != "zzz" {
		t.Fatalf("fast: expected zzz, got %q", w)
	}
	// Auto afterwards must still use global defaults (40/25/15/20): with
	// default weights and no capability hints, healthy/fast zzz wins.
	req := execReq("auto", "do it")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if w := res.Decision.SelectedProvider; w != "zzz" {
		t.Fatalf("auto after mode executions: expected zzz (default weights intact), got %q", w)
	}
	after := eng.GetScorer().LoadWeights()
	if before != after {
		t.Fatalf("engine global weights mutated: %+v -> %+v", before, after)
	}
}

// TestP313ModeProfilesAreFreshCopies: DefaultModeProfiles() must return
// defensive copies — mutating a returned profile cannot corrupt the next call.
func TestP313ModeProfilesAreFreshCopies(t *testing.T) {
	first := router.DefaultModeProfiles()[router.ModeCoding]
	first.WeightPreferences.Capability = 99
	first.CapabilityBonuses.Reasoning = 99
	second := router.DefaultModeProfiles()[router.ModeCoding]
	if got := second.WeightPreferences.Capability; got != 60 {
		t.Fatalf("profile table mutated: capability weight = %v, want 60", got)
	}
	if got := second.CapabilityBonuses.Reasoning; got != 0.15 {
		t.Fatalf("profile table mutated: reasoning bonus = %f, want 0.15", got)
	}
}

// TestP313ConcurrentAllModes: 8 modes concurrently over a shared pipeline
// produce the same winners as serial execution (deterministic; run under
// -race to prove no data races).
func TestP313ConcurrentAllModes(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "aaa", supportsAll: true, vision: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 500, healthState: runtime.StateHealthy, costPerUnit: 0.0005, maxContext: 128000},
		&calibStubProvider{name: "zzz", supportsAll: true, vision: false, reasoning: false, toolCalling: false, structured: false, latencyMs: 50, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 128000},
	)
	setHealth(t, store, "aaa", runtime.StateHealthy, 500)
	setHealth(t, store, "zzz", runtime.StateHealthy, 50)

	modes := []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"}

	serial := map[string]string{}
	for _, mode := range modes {
		serial[mode] = fastWinner(t, pipeline, mode)
	}

	var wg sync.WaitGroup
	results := make([]string, len(modes))
	errs := make([]error, len(modes))
	for i, mode := range modes {
		wg.Add(1)
		go func(i int, mode string) {
			defer wg.Done()
			req := execReq(mode, "do it")
			res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = res.Decision.SelectedProvider
		}(i, mode)
	}
	wg.Wait()

	for i, mode := range modes {
		if errs[i] != nil {
			t.Fatalf("mode %s concurrent execute: %v", mode, errs[i])
		}
		if results[i] != serial[mode] {
			t.Fatalf("mode %s: concurrent winner %q != serial winner %q", mode, results[i], serial[mode])
		}
	}
}
