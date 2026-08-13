package orchestration

import (
	"context"

	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
)

// Candidate represents a provider/model choice for a task.
type Candidate struct {
	ProviderName    string  `json:"provider_name"`
	ProviderModelID string  `json:"provider_model_id"`
	Score           float64 `json:"score"`
	HealthScore     float64 `json:"health_score"`
	LatencyMs       int64   `json:"latency_ms"`
	CapScore        float64 `json:"capability_score"`
	Selected        bool    `json:"selected"`
	Rejected        bool    `json:"rejected"`
	RejectionReason string  `json:"rejection_reason,omitempty"`
}

// GenerateCandidates produces ranked candidate provider/model pairs.
func GenerateCandidates(
	ctx context.Context,
	registry *provider.Registry,
	engine *router.RouterEngine,
	capReq *CapabilityRequirement,
	modelID string,
) []*Candidate {
	_ = ctx
	if engine == nil {
		return nil
	}

	caps := router.CapabilityHint{
		Vision:      capReq.NeedsVision,
		Reasoning:   capReq.NeedsReasoning,
		ToolCalling: capReq.NeedsToolCalling,
		Streaming:   capReq.NeedsStreaming,
	}
	scores := engine.GetProviderScores(caps)

	cands := make([]*Candidate, 0, len(scores))
	for _, s := range scores {
		c := &Candidate{
			ProviderName:    s.Provider,
			ProviderModelID: s.Provider,
			Score:           s.TotalScore,
			HealthScore:     s.HealthScore,
			LatencyMs:       int64(s.LatencyScore),
			CapScore:        s.CapScore,
			Selected:        s.Selected,
			Rejected:        s.Rejected,
			RejectionReason: s.RejectionReason,
		}
		cands = append(cands, c)
	}
	return cands
}

// SelectBestCandidate picks the highest-scoring non-rejected candidate.
func SelectBestCandidate(cands []*Candidate) *Candidate {
	if len(cands) == 0 {
		return nil
	}
	var best *Candidate
	for _, c := range cands {
		if c.Rejected {
			continue
		}
		if best == nil || c.Score > best.Score {
			best = c
		}
	}
	if best == nil {
		return cands[0]
	}
	return best
}
