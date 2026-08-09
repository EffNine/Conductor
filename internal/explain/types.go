// Package explain defines interfaces and types for explainable routing decisions.
//
// These types provide the foundation for future explainability features that
// can justify why a particular provider was selected for a request.
// No generation logic is implemented here.
package explain

import (
	"time"
)

// Reason indicates why a decision was made or a penalty applied.
type Reason string

const (
	// ReasonHealth Primary factor was provider health status.
	ReasonHealth Reason = "health"
	// ReasonLatency Primary factor was latency performance.
	ReasonLatency Reason = "latency"
	// ReasonCost Primary factor was cost efficiency.
	ReasonCost Reason = "cost"
	// ReasonCapability Primary factor was capability match.
	ReasonCapability Reason = "capability"
	// ReasonPolicy Primary factor was policy enforcement.
	ReasonPolicy Reason = "policy"
	// ReasonFallback Primary factor was fallback chain execution.
	ReasonFallback Reason = "fallback"
	// ReasonRandom Primary factor was random selection.
	ReasonRandom Reason = "random"
	// ReasonConfig Primary factor was explicit configuration.
	ReasonConfig Reason = "config"
)

// DecisionRationale captures the reasoning behind a routing decision.
type DecisionRationale struct {
	RequestID     string     `json:"request_id"`
	SelectedModel string     `json:"selected_model"`
	SelectedProvider string  `json:"selected_provider"`
	DecisionReason Reason     `json:"decision_reason"`
	Confidence    float64    `json:"confidence"`
	Timestamp     time.Time  `json:"timestamp"`
	Candidates    []CandidateRationale `json:"candidates,omitempty"`
	Signals       []SignalEntry    `json:"signals,omitempty"`
	Penalties     []PenaltyEntry   `json:"penalties,omitempty"`
	Metadata      map[string]any   `json:"metadata,omitempty"`
}

// CandidateRationale captures the reasoning for each candidate considered.
type CandidateRationale struct {
	Provider      string         `json:"provider"`
	Model         string         `json:"model"`
	Score         float64        `json:"score"`
	Rank          int            `json:"rank"`
	Reasons       []Reason       `json:"reasons,omitempty"`
	Signals       []SignalEntry  `json:"signals,omitempty"`
	Penalties     []PenaltyEntry `json:"penalties,omitempty"`
	Rejected      bool           `json:"rejected,omitempty"`
	RejectionReason string       `json:"rejection_reason,omitempty"`
}

// SignalEntry represents a positive signal that influenced the decision.
type SignalEntry struct {
	Type      string `json:"type"`
	Value     float64 `json:"value"`
	Weight    float64 `json:"weight"`
	Source    string `json:"source"`
	Timestamp time.Time `json:"timestamp"`
}

// PenaltyEntry represents a negative signal that reduced a candidate's score.
type PenaltyEntry struct {
	Type      string  `json:"type"`
	Value     float64 `json:"value"`
	Reason    string  `json:"reason"`
	Source    string  `json:"source"`
	Timestamp time.Time `json:"timestamp"`
}

// ExplainableDecision is the interface for components that can explain their decisions.
type ExplainableDecision interface {
	// Explain returns the rationale for a decision.
	Explain() *DecisionRationale
}
