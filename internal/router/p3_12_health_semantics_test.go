package router_test

import (
	"context"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// P3.12 unhealthy vs circuit-open semantics:
//
//   - Unhealthy (or recovering) provider state is a SOFT signal: the health
//     score drops to 0.1 but the candidate stays eligible and can be selected
//     when it is the only (or best) candidate. It never hard-rejects.
//   - An open circuit breaker is a HARD rejection: the candidate is rejected
//     before soft scoring in every mode and can never be selected.
//
// These tests pin both behaviors across every public mode.

var p312AllPublicModes = []string{"auto", "coding", "reasoning", "vision", "fast", "planning", "agentic", "long_horizon"}

// TestP312UnhealthySoleCandidateEligibleAcrossModes verifies an unhealthy
// provider is never hard-rejected: as the sole candidate it is selected in
// every mode.
func TestP312UnhealthySoleCandidateEligibleAcrossModes(t *testing.T) {
	for _, mode := range p312AllPublicModes {
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "only", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateUnhealthy},
		)
		setHealth(t, store, "only", runtime.StateUnhealthy, 100)

		res, err := pipeline.Execute(context.Background(), execReq(mode, "hi"), router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("%s Execute: %v", mode, err)
		}
		if res.Decision.SelectedProvider != "only" {
			t.Fatalf("%s: expected unhealthy sole candidate to be selected (soft penalty, not rejection), got %q", mode, res.Decision.SelectedProvider)
		}
		for _, cs := range res.Decision.CandidateScores {
			if cs.Rejected {
				t.Fatalf("%s: unhealthy candidate must not be rejected: %s", mode, cs.RejectionReason)
			}
			if cs.HealthScore > 0.15 {
				t.Fatalf("%s: unhealthy candidate must score low (~0.1), got %f", mode, cs.HealthScore)
			}
		}
	}
}

// TestP312UnhealthyLosesToHealthyBySoftMargin verifies unhealthy is a soft
// penalty: a healthy competitor wins on health even when the unhealthy
// candidate dominates every other axis (cheap, fast, capable).
func TestP312UnhealthyLosesToHealthyBySoftMargin(t *testing.T) {
	for _, mode := range p312AllPublicModes {
		pipeline, store, _ := setupCalibPipeline(t,
			&calibStubProvider{name: "aaa", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 10, healthState: runtime.StateUnhealthy, costPerUnit: 0.0001},
			&calibStubProvider{name: "zzz", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 5000, healthState: runtime.StateHealthy, costPerUnit: 0.0009},
		)
		setHealth(t, store, "aaa", runtime.StateUnhealthy, 10)
		setHealth(t, store, "zzz", runtime.StateHealthy, 5000)

		res, err := pipeline.Execute(context.Background(), execReq(mode, "hi"), router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("%s Execute: %v", mode, err)
		}
		if res.Decision.SelectedProvider != "zzz" {
			t.Fatalf("%s: expected healthy provider to win (unhealthy is soft), got %q", mode, res.Decision.SelectedProvider)
		}
		for _, cs := range res.Decision.CandidateScores {
			if cs.Provider == "aaa" && cs.Rejected {
				t.Fatalf("%s: unhealthy must not be hard-rejected: %s", mode, cs.RejectionReason)
			}
		}
	}
}

// TestP312CircuitOpenHardRejectsAcrossModes verifies an open breaker hard-
// rejects the candidate in every mode, even when it is the sole candidate.
func TestP312CircuitOpenHardRejectsAcrossModes(t *testing.T) {
	for _, mode := range p312AllPublicModes {
		reg := provider.NewRegistry()
		store := runtime.NewRuntimeStore(nil)
		prov := &calibStubProvider{name: "open", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 10, healthState: runtime.StateHealthy}
		reg.Register(prov)
		_ = store.Register(runtime.NewProviderRuntime(prov.name, prov))
		setHealth(t, store, "open", runtime.StateHealthy, 10)
		manager := runtime.NewManager(store)

		pool := router.NewBreakerPool(breaker.Config{FailureThreshold: 3, RecoveryTimeout: time.Minute, SuccessThreshold: 1})
		eng := router.NewRouterEngine(router.RouterEngineConfig{
			Registry:    reg,
			Runtime:     manager,
			BreakerPool: pool,
		})
		pipeline := router.NewDecisionPipeline(router.PipelineConfig{
			RoutingEngine:  eng,
			RuntimeManager: manager,
			BreakerPool:    pool,
		})

		for i := 0; i < 5; i++ {
			pool.Get("open").RecordFailure()
		}
		if pool.Get("open").Allow() == breaker.ResultAllowed {
			t.Fatal("expected breaker to be open")
		}

		res, err := pipeline.Execute(context.Background(), execReq(mode, "hi"), router.Environment{CircuitBreakerEnabled: true}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("%s Execute: %v", mode, err)
		}
		if res.Decision.SelectedProvider != "" {
			t.Fatalf("%s: expected no selection (circuit open is a hard rejection), got %q", mode, res.Decision.SelectedProvider)
		}
		for _, cs := range res.Decision.CandidateScores {
			if !cs.Rejected {
				t.Fatalf("%s: circuit-open candidate must be rejected", mode)
			}
			if cs.RejectionReason != "circuit breaker open" {
				t.Fatalf("%s: unexpected rejection reason %q", mode, cs.RejectionReason)
			}
		}
	}
}

// TestP312CircuitOpenBeatsEverySoftAdvantage verifies a circuit-open candidate
// loses to a healthy competitor even when it dominates on latency, cost, and
// capabilities.
func TestP312CircuitOpenBeatsEverySoftAdvantage(t *testing.T) {
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	provs := []*calibStubProvider{
		{name: "open", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 10, healthState: runtime.StateHealthy, costPerUnit: 0.0001, maxContext: 131072},
		{name: "fine", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 5000, healthState: runtime.StateDegraded, costPerUnit: 0.0009, maxContext: 4096},
	}
	for _, p := range provs {
		reg.Register(p)
		_ = store.Register(runtime.NewProviderRuntime(p.name, p))
		setHealth(t, store, p.name, p.healthState, p.latencyMs)
	}
	manager := runtime.NewManager(store)

	pool := router.NewBreakerPool(breaker.Config{FailureThreshold: 3, RecoveryTimeout: time.Minute, SuccessThreshold: 1})
	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:    reg,
		Runtime:     manager,
		BreakerPool: pool,
	})
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  eng,
		RuntimeManager: manager,
		BreakerPool:    pool,
	})

	for i := 0; i < 5; i++ {
		pool.Get("open").RecordFailure()
	}

	for _, mode := range p312AllPublicModes {
		res, err := pipeline.Execute(context.Background(), execReq(mode, "hi"), router.Environment{CircuitBreakerEnabled: true}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("%s Execute: %v", mode, err)
		}
		if res.Decision.SelectedProvider != "fine" {
			t.Fatalf("%s: expected fine (circuit open is hard), got %q", mode, res.Decision.SelectedProvider)
		}
	}
}
