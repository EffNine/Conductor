package router_test

import (
	"context"
	"math"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// TestP311UnknownLatencyNeutral verifies a candidate without recorded latency
// gets the documented neutral latency score (0.5), not 0 or an error.
func TestP311UnknownLatencyNeutral(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "no-lat", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "slow", supportsAll: true, latencyMs: 5000, healthState: runtime.StateHealthy},
	)
	// Only "slow" has recorded latency; "no-lat" has none.
	setHealth(t, store, "slow", runtime.StateHealthy, 5000)
	store.Update("no-lat", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		return nil
	})

	res, err := pipeline.Execute(context.Background(), defReq("auto", "hello"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	latNoLat, latSlow := -1.0, -1.0
	for _, cs := range res.Decision.CandidateScores {
		switch cs.Provider {
		case "no-lat":
			latNoLat = cs.LatencyScore
		case "slow":
			latSlow = cs.LatencyScore
		}
	}
	if latNoLat != 0.5 {
		t.Errorf("unknown latency score = %f, want 0.5", latNoLat)
	}
	if math.Abs(latSlow-0.2) > 1e-9 {
		t.Errorf("slow latency score = %f, want 0.2", latSlow)
	}
}

// TestP311UnknownCostNeutral verifies a candidate without pricing info gets
// the documented neutral cost score (0.5) and is still routable.
func TestP311UnknownCostNeutral(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "no-price", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "priced", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0005},
	)
	setHealth(t, store, "no-price", runtime.StateHealthy, 100)
	setHealth(t, store, "priced", runtime.StateHealthy, 100)

	res, err := pipeline.Execute(context.Background(), defReq("auto", "hello"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	costNoPrice, costPriced := -1.0, -1.0
	for _, cs := range res.Decision.CandidateScores {
		switch cs.Provider {
		case "no-price":
			costNoPrice = cs.CostScore
		case "priced":
			costPriced = cs.CostScore
		}
	}
	if costNoPrice != 0.5 {
		t.Errorf("unknown cost score = %f, want 0.5", costNoPrice)
	}
	if costPriced != 0.5 {
		t.Errorf("priced cost score = %f, want 0.5", costPriced)
	}
	if res.Decision.SelectedProvider == "" {
		t.Fatal("no candidate selected with unknown cost")
	}
}

// TestP311UnknownProviderStateNeutral verifies a provider absent from the
// snapshot gets the neutral health score and cannot be silently favored.
func TestP311UnknownProviderStateNeutral(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "known", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "unknown", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	// Only "known" is registered in the runtime store; "unknown" never appears
	// in the snapshot.
	setHealth(t, store, "known", runtime.StateHealthy, 100)
	res, err := pipeline.Execute(context.Background(), defReq("auto", "hello"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, cs := range res.Decision.CandidateScores {
		switch cs.Provider {
		case "known":
			if cs.HealthScore != 1.0 {
				t.Errorf("known health = %f, want 1.0", cs.HealthScore)
			}
		case "unknown":
			if cs.HealthScore != 0.5 {
				t.Errorf("unknown health = %f, want neutral 0.5", cs.HealthScore)
			}
		}
	}
}

// TestP311DegradedNotRejected verifies degraded candidates stay eligible
// (soft penalty) — degraded must not be hard-rejected even when healthy
// alternatives exist, and must lose to a healthy candidate.
func TestP311DegradedNotRejected(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "healthy", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "degraded", supportsAll: true, latencyMs: 100, healthState: runtime.StateDegraded},
	)
	setHealth(t, store, "healthy", runtime.StateHealthy, 100)
	setHealth(t, store, "degraded", runtime.StateDegraded, 100)

	res, err := pipeline.Execute(context.Background(), defReq("auto", "hello"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var sawDegraded bool
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "degraded" {
			sawDegraded = true
			if cs.HealthScore != 0.6 {
				t.Errorf("degraded health = %f, want 0.6", cs.HealthScore)
			}
		}
	}
	if !sawDegraded {
		t.Fatal("degraded candidate was dropped from candidates")
	}
	if res.Decision.SelectedProvider != "healthy" {
		t.Errorf("expected healthy to win over degraded, got %s", res.Decision.SelectedProvider)
	}
}

// TestP311UnhealthyScoresLowButNotRejected verifies the unhealthy state is a
// soft penalty (0.1), not a hard rejection: a single unhealthy candidate still
// gets selected rather than failing the request.
func TestP311UnhealthyScoresLowButNotRejected(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "sick", supportsAll: true, latencyMs: 100, healthState: runtime.StateUnhealthy},
	)
	setHealth(t, store, "sick", runtime.StateUnhealthy, 100)

	res, err := pipeline.Execute(context.Background(), defReq("auto", "hello"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "sick" {
		t.Fatalf("expected only candidate to be selected, got %s", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "sick" && cs.HealthScore != 0.1 {
			t.Errorf("unhealthy health = %f, want 0.1", cs.HealthScore)
		}
	}
}

// TestP311UnknownMaxContextEligible verifies unknown MaxContext keeps a
// candidate eligible for long_horizon (no false hard rejection).
func TestP311UnknownMaxContextEligible(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "known-ctx", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy, maxContext: 1000},
		&calibStubProvider{name: "unknown-ctx", supportsAll: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "known-ctx", runtime.StateHealthy, 100)
	setHealth(t, store, "unknown-ctx", runtime.StateHealthy, 100)

	big := &apitypes.ChatCompletionRequest{
		Model: "m", Mode: "long_horizon",
		Messages: []apitypes.Message{{Role: "user", Content: string(make([]byte, 4096))}},
	}
	res, err := pipeline.Execute(context.Background(), big, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "unknown-ctx" {
		t.Errorf("expected unknown-ctx (neutral context) to win, got %s", res.Decision.SelectedProvider)
	}
}

// TestP311UnknownTelemetryNeutralInPlanning verifies planning telemetry is
// neutral when no execution outcomes exist: two identical candidates resolve
// alphabetically (telemetry must not invent a preference from empty data).
func TestP311UnknownTelemetryNeutralInPlanning(t *testing.T) {
	pipeline, _, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "a", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "b", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	res, err := pipeline.Execute(context.Background(), defReq("planning", "hello"), router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "a" {
		t.Errorf("expected alphabetical tie-break (neutral telemetry), got %s", res.Decision.SelectedProvider)
	}
}

func defReq(mode, content string) *apitypes.ChatCompletionRequest {
	return &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     mode,
		Messages: []apitypes.Message{{Role: "user", Content: content}},
	}
}
