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

// RoutingPreferences carries soft role-based preferences for candidate scoring.
type RoutingPreferences struct {
	PreferredProviders    []string
	ExcludedProviders     []string
	PreferredCapabilities []string
}

// GenerateCandidates produces ranked candidate provider/model pairs.
// It uses the RouterEngine's SelectBestProvider to get actual executable
// provider/model pairs rather than dashboard-level provider scores.
func GenerateCandidates(
	ctx context.Context,
	registry *provider.Registry,
	engine *router.RouterEngine,
	capReq *CapabilityRequirement,
	modelID string,
	routingPrefs RoutingPreferences,
) []*Candidate {
	if engine == nil {
		return nil
	}

	// Build a minimal request so the engine can score candidates.
	req := &struct {
		Model      string
		Stream     bool
		Tools      []any
		ToolChoice any
	}{Model: modelID}
	_ = req

	caps := router.CapabilityHint{
		Vision:      capReq.NeedsVision,
		Reasoning:   capReq.NeedsReasoning,
		ToolCalling: capReq.NeedsToolCalling,
		Streaming:   capReq.NeedsStreaming,
	}

	// Get all registered providers and score each one.
	providers := registry.All()
	if len(providers) == 0 {
		return nil
	}

	cands := make([]*Candidate, 0, len(providers))
	for _, p := range providers {
		providerName := p.Name()

		// Apply excluded providers filter.
		if isExcluded(providerName, routingPrefs.ExcludedProviders) {
			c := &Candidate{
				ProviderName:    providerName,
				ProviderModelID: providerName,
				Score:           0,
				HealthScore:     0,
				Rejected:        true,
				RejectionReason: "excluded by routing hint",
			}
			cands = append(cands, c)
			continue
		}

		// Use a generic model for scoring; the engine will pick the best match.
		scores := engine.GetProviderScores(caps)
		for _, s := range scores {
			if s.Provider != providerName {
				continue
			}
			score := s.TotalScore
			// Apply soft preference bonus for preferred providers.
			if isPreferred(providerName, routingPrefs.PreferredProviders) {
				score += 0.05
				if score > 1.0 {
					score = 1.0
				}
			}
			c := &Candidate{
				ProviderName:    s.Provider,
				ProviderModelID: providerName, // will be resolved to actual model later
				Score:           score,
				HealthScore:     s.HealthScore,
				LatencyMs:       0,
				CapScore:        s.CapScore,
				Selected:        s.Selected,
				Rejected:        s.Rejected,
				RejectionReason: s.RejectionReason,
			}
			cands = append(cands, c)
		}
	}

	// If no scores from engine, fall back to direct provider info.
	if len(cands) == 0 {
		for _, p := range providers {
			c := &Candidate{
				ProviderName:    p.Name(),
				ProviderModelID: p.Name(),
				Score:           0.5,
				HealthScore:     0.5,
				CapScore:        1.0,
			}
			cands = append(cands, c)
		}
	}

	return cands
}

func isPreferred(name string, preferred []string) bool {
	for _, p := range preferred {
		if p == name {
			return true
		}
	}
	return false
}

func isExcluded(name string, excluded []string) bool {
	for _, e := range excluded {
		if e == name {
			return true
		}
	}
	return false
}

// ResolveCandidateModel uses the basic router engine to resolve a candidate
// to an actual executable provider/model pair. Returns the resolved model ID.
func ResolveCandidateModel(ctx context.Context, engine *router.Engine, providerName, modelID string) string {
	if engine == nil || providerName == "" || modelID == "" {
		return modelID
	}
	// Try resolving with provider prefix.
	prefixed := providerName + "/" + modelID
	route, err := engine.ResolveWithContext(ctx, prefixed, nil)
	if err == nil && route != nil && route.ProviderModelID != "" {
		return route.ProviderModelID
	}
	// Fallback: use the original model ID.
	return modelID
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
