package shadow

import (
	"encoding/json"
	"time"
)

// ShadowObservation is one persisted routing observation with its JEV
// advisory result and the eventual execution outcome. It answers: "Given the
// same routing state, what did Conductor choose, what did JEV advise, and
// what actually happened?"
//
// The row is written asynchronously by the shadow consumer (see Advisor),
// off the request path. Retention pruning bounds growth.
//
// Safety contract: the observation never contains prompts, API keys,
// authorization headers, provider secrets, or raw request bodies — only
// routing-state identifiers, scores, and the JEV advisory judgment.
type ShadowObservation struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// CreatedAt is the observation timestamp (UTC).
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
	DecisionID     string    `gorm:"size:64;uniqueIndex" json:"decision_id"`
	RequestedModel string    `gorm:"size:191;index" json:"requested_model"`
	ResolvedMode   string    `gorm:"size:64;index" json:"resolved_mode"`
	// Deterministic routing decision.
	SelectedProvider   string  `gorm:"size:64;index" json:"selected_provider"`
	SelectedModel      string  `gorm:"size:191" json:"selected_model"`
	SelectedScore      float64 `json:"selected_score"`
	CandidateCount     int     `json:"candidate_count"`
	CandidateProviders string  `gorm:"size:512" json:"candidate_providers"`
	// JEV advisory result (empty when JEV failed or was not configured).
	AdvisoryProvider      string   `gorm:"size:64" json:"advisory_provider"`
	AdvisoryConfidence    *float64 `json:"advisory_confidence"`
	AdvisoryProbabilities string   `gorm:"size:1024" json:"advisory_probabilities"`
	// JEV call metadata.
	JEVModel        string `gorm:"size:64" json:"jev_model"`
	JEVDurationMs   int64  `json:"jev_duration_ms"`
	JEVInputTokens  int    `json:"jev_input_tokens"`
	JEVOutputTokens int    `json:"jev_output_tokens"`
	// AdvisoryStatus: "ok", "timeout", "unauthorized", "rate_limited",
	// "malformed", "error", or "not_configured".
	AdvisoryStatus string `gorm:"size:32;index" json:"advisory_status"`
	// Eventual execution outcome (backfilled by the execution-attempt bridge).
	Outcome           string `gorm:"size:16" json:"outcome"`
	ProviderLatencyMs int64  `json:"provider_latency_ms"`
}

// advisoryProbabilities marshals a probability map for column storage.
func marshalProbabilities(p map[string]float64) string {
	if len(p) == 0 {
		return ""
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}
