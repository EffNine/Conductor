package runtime

import (
	"testing"
	"time"
)

func TestProviderStateConstants(t *testing.T) {
	expected := []ProviderState{
		StateUnknown,
		StateHealthy,
		StateDegraded,
		StateUnhealthy,
		StateRecovering,
		StateScalingUp,
		StateScalingDown,
	}

	for _, state := range expected {
		if state == "" {
			t.Error("expected non-empty state constant")
		}
	}
}

func TestStateChange(t *testing.T) {
	change := StateChange{
		ProviderName: "test-provider",
		From:         StateHealthy,
		To:           StateDegraded,
		Reason:       "high error rate",
		Timestamp:    time.Now(),
		Metadata:     map[string]any{"error_rate": 0.15},
	}

	if change.ProviderName != "test-provider" {
		t.Errorf("expected 'test-provider', got %s", change.ProviderName)
	}
	if change.From != StateHealthy {
		t.Errorf("expected StateHealthy, got %s", change.From)
	}
	if change.To != StateDegraded {
		t.Errorf("expected StateDegraded, got %s", change.To)
	}
	if change.Reason != "high error rate" {
		t.Errorf("expected 'high error rate', got %s", change.Reason)
	}
	if change.Metadata["error_rate"] != 0.15 {
		t.Error("expected error_rate to be 0.15")
	}
}

func TestProviderStateSnapshot(t *testing.T) {
	snap := ProviderStateSnapshot{
		State:           StateHealthy,
		LastHealthCheck: time.Now(),
		LatencyMs:       150,
		ErrorRate:       0.02,
		Capacity:        1.0,
		Tags:            map[string]string{"region": "us-east-1"},
	}

	if snap.State != StateHealthy {
		t.Errorf("expected StateHealthy, got %s", snap.State)
	}
	if snap.LatencyMs != 150 {
		t.Errorf("expected 150ms, got %d", snap.LatencyMs)
	}
	if snap.Tags["region"] != "us-east-1" {
		t.Error("expected region tag")
	}
}

func TestGlobalRuntimeState(t *testing.T) {
	state := GlobalRuntimeState{
		TotalProviders:     5,
		HealthyProviders:   4,
		DegradedProviders:  1,
		UnhealthyProviders: 0,
		AvgLatencyMs:       120,
		TotalQPS:           50.5,
	}

	if state.TotalProviders != 5 {
		t.Errorf("expected 5 providers, got %d", state.TotalProviders)
	}
	if state.HealthyProviders != 4 {
		t.Errorf("expected 4 healthy, got %d", state.HealthyProviders)
	}
}

func TestRuntimeSnapshot(t *testing.T) {
	snap := RuntimeSnapshot{
		Timestamp: time.Now(),
		Providers: map[string]ProviderStateSnapshot{
			"openai": {State: StateHealthy, LatencyMs: 100},
			"anthropic": {State: StateDegraded, LatencyMs: 500},
		},
		GlobalState: GlobalRuntimeState{
			TotalProviders:   2,
			HealthyProviders: 1,
			DegradedProviders: 1,
		},
	}

	if len(snap.Providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(snap.Providers))
	}
	if snap.Providers["openai"].State != StateHealthy {
		t.Error("expected openai to be healthy")
	}
}
