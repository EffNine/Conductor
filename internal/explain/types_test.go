package explain

import (
	"testing"
	"time"
)

func TestReasonConstants(t *testing.T) {
	expected := []Reason{
		ReasonHealth,
		ReasonLatency,
		ReasonCost,
		ReasonCapability,
		ReasonPolicy,
		ReasonFallback,
		ReasonRandom,
		ReasonConfig,
	}

	for _, reason := range expected {
		if reason == "" {
			t.Error("expected non-empty reason constant")
		}
	}
}

func TestDecisionRationale(t *testing.T) {
	now := time.Now()
	rationale := DecisionRationale{
		RequestID:        "req-123",
		SelectedModel:    "gpt-4o",
		SelectedProvider: "openai",
		DecisionReason:   ReasonHealth,
		Confidence:       0.95,
		Timestamp:        now,
		Metadata: map[string]any{
			"weight_health":  0.4,
			"weight_latency": 0.25,
		},
	}

	if rationale.RequestID != "req-123" {
		t.Errorf("expected 'req-123', got %s", rationale.RequestID)
	}
	if rationale.SelectedProvider != "openai" {
		t.Errorf("expected 'openai', got %s", rationale.SelectedProvider)
	}
	if rationale.DecisionReason != ReasonHealth {
		t.Errorf("expected ReasonHealth, got %s", rationale.DecisionReason)
	}
	if rationale.Confidence != 0.95 {
		t.Errorf("expected 0.95 confidence, got %f", rationale.Confidence)
	}
}

func TestCandidateRationale(t *testing.T) {
	candidate := CandidateRationale{
		Provider:        "anthropic",
		Model:           "claude-3-sonnet",
		Score:           0.85,
		Rank:            2,
		Reasons:         []Reason{ReasonCost, ReasonCapability},
		Rejected:        false,
		RejectionReason: "",
	}

	if candidate.Provider != "anthropic" {
		t.Errorf("expected 'anthropic', got %s", candidate.Provider)
	}
	if candidate.Rank != 2 {
		t.Errorf("expected rank 2, got %d", candidate.Rank)
	}
	if candidate.Score != 0.85 {
		t.Errorf("expected 0.85 score, got %f", candidate.Score)
	}
}

func TestSignalEntry(t *testing.T) {
	signal := SignalEntry{
		Type:      "latency_improvement",
		Value:     0.15,
		Weight:    0.25,
		Source:    "health_monitor",
		Timestamp: time.Now(),
	}

	if signal.Type != "latency_improvement" {
		t.Errorf("expected 'latency_improvement', got %s", signal.Type)
	}
	if signal.Weight != 0.25 {
		t.Errorf("expected 0.25 weight, got %f", signal.Weight)
	}
}

func TestPenaltyEntry(t *testing.T) {
	penalty := PenaltyEntry{
		Type:      "circuit_breaker_open",
		Value:     1.0,
		Reason:    "provider openai has open circuit breaker",
		Source:    "breaker_pool",
		Timestamp: time.Now(),
	}

	if penalty.Type != "circuit_breaker_open" {
		t.Errorf("expected 'circuit_breaker_open', got %s", penalty.Type)
	}
	if penalty.Reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestDecisionRationaleWithCandidates(t *testing.T) {
	now := time.Now()
	rationale := DecisionRationale{
		RequestID:        "req-456",
		SelectedModel:    "gpt-4o",
		SelectedProvider: "openai",
		DecisionReason:   ReasonCost,
		Confidence:       0.88,
		Timestamp:        now,
		Candidates: []CandidateRationale{
			{
				Provider: "openai",
				Model:    "gpt-4o",
				Score:    0.92,
				Rank:     1,
			},
			{
				Provider: "anthropic",
				Model:    "claude-3-sonnet",
				Score:    0.85,
				Rank:     2,
			},
		},
		Signals: []SignalEntry{
			{Type: "low_cost", Value: 0.1, Weight: 0.15, Source: "pricing"},
		},
		Penalties: []PenaltyEntry{
			{Type: "high_latency", Value: 0.3, Reason: "latency above threshold", Source: "health"},
		},
	}

	if len(rationale.Candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(rationale.Candidates))
	}
	if len(rationale.Signals) != 1 {
		t.Errorf("expected 1 signal, got %d", len(rationale.Signals))
	}
	if len(rationale.Penalties) != 1 {
		t.Errorf("expected 1 penalty, got %d", len(rationale.Penalties))
	}
}
