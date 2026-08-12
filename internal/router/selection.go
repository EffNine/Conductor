package router

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/runtime"
	"go.uber.org/zap"
)

// candidateInfo holds per-provider candidate data for scoring.
type candidateInfo struct {
	provider     provider.Provider
	providerName string
	modelID      string
}

// RoutingDecision is the result of a routing decision.
type RoutingDecision struct {
	SelectedProvider   string            `json:"selected_provider"`
	SelectedModelID    string            `json:"selected_model_id"`
	SelectedProviderID string            `json:"selected_provider_model_id"`
	CandidateScores    []CandidateScore  `json:"candidate_scores"`
	RejectionReasons   []RejectionReason `json:"rejection_reasons,omitempty"`
	RoutingDurationMs  int64             `json:"routing_duration_ms"`
}

// CandidateScore is the score for one candidate provider.
type CandidateScore struct {
	Provider        string  `json:"provider"`
	ProviderID      string  `json:"provider_model_id"`
	TotalScore      float64 `json:"total_score"`
	HealthScore     float64 `json:"health_score"`
	LatencyScore    float64 `json:"latency_score"`
	CostScore       float64 `json:"cost_score"`
	CapScore        float64 `json:"capability_score"`
	Selected        bool    `json:"selected"`
	Rejected        bool    `json:"rejected"`
	RejectionReason string  `json:"rejection_reason,omitempty"`
}

// RejectionReason describes why a candidate was not selected.
type RejectionReason struct {
	Provider string `json:"provider"`
	Reason   string `json:"reason"`
}

// SelectionResult is the outcome of SelectBestProvider.
type SelectionResult struct {
	Decision RoutingDecision
	// Candidate is the winning provider, nil if no provider was available.
	Candidate *Candidate
}

// RouterEngine handles intelligent provider selection.
type RouterEngine struct {
	mu             sync.RWMutex
	registry       *provider.Registry
	healthStore    *health.ModelStatusStore
	metricsStore   *MetricsStore
	runtime        runtime.Manager
	scorer         *Scorer
	breakerPool    *BreakerPool
	logger         *zap.Logger
	providerCaps   map[string]Capabilities
	costCeiling    float64
	healthyLatency int64
}

// RouterEngineConfig holds configuration for the router engine.
type RouterEngineConfig struct {
	Registry         *provider.Registry
	HealthStore      *health.ModelStatusStore
	MetricsStore     *MetricsStore
	BreakerPool      *BreakerPool
	Runtime          runtime.Manager
	Logger           *zap.Logger
	Weights          config.RoutingWeights
	CostCeiling      float64 // max cost per token; 0 uses default
	HealthyLatencyMs int64   // expected healthy latency; 0 uses default
}

// NewRouterEngine creates a new intelligent routing engine.
func NewRouterEngine(cfg RouterEngineConfig) *RouterEngine {
	raw := RawWeights{
		Health:     cfg.Weights.Health,
		Latency:    cfg.Weights.Latency,
		Cost:       cfg.Weights.Cost,
		Capability: cfg.Weights.Capability,
	}
	healthyLatency := cfg.HealthyLatencyMs
	if healthyLatency <= 0 {
		healthyLatency = 1000
	}
	costCeiling := cfg.CostCeiling
	if costCeiling <= 0 {
		costCeiling = 0.001
	}
	return &RouterEngine{
		registry:       cfg.Registry,
		healthStore:    cfg.HealthStore,
		metricsStore:   cfg.MetricsStore,
		runtime:        cfg.Runtime,
		scorer:         NewScorer(raw),
		breakerPool:    cfg.BreakerPool,
		logger:         cfg.Logger,
		providerCaps:   make(map[string]Capabilities),
		costCeiling:    costCeiling,
		healthyLatency: healthyLatency,
	}
}

// UpdateWeights updates the scoring weights at runtime.
func (e *RouterEngine) UpdateWeights(w config.RoutingWeights) {
	e.mu.Lock()
	e.scorer.UpdateWeights(RawWeights{
		Health:     w.Health,
		Latency:    w.Latency,
		Cost:       w.Cost,
		Capability: w.Capability,
	})
	e.mu.Unlock()
}

// SelectBestProvider selects the best provider for a request from all registered providers.
// It filters by capability, circuit breaker, and health, then scores remaining candidates.
func (e *RouterEngine) SelectBestProvider(ctx context.Context, modelID string, req *apitypes.ChatCompletionRequest) (*SelectionResult, error) {
	start := time.Now()

	capHint := ExtractCapabilityHint(req)
	providers := e.registry.All()
	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers registered")
	}

	// Build candidates.
	cands := make([]candidateInfo, 0, len(providers))
	for _, p := range providers {
		if !p.SupportsModel(modelID) {
			continue
		}
		cands = append(cands, candidateInfo{
			provider:     p,
			providerName: p.Name(),
			modelID:      modelID,
		})
	}

	if len(cands) == 0 {
		return nil, fmt.Errorf("no provider supports model '%s'", modelID)
	}

	// Score each candidate.
	scores := make([]CandidateScore, 0, len(cands))
	var rejections []RejectionReason
	var bestScore float64 = -1
	var bestIdx int = -1

	for i, c := range cands {
		cs := e.scoreCandidate(ctx, c, capHint)
		scores = append(scores, cs)
		if cs.Rejected {
			rejections = append(rejections, RejectionReason{
				Provider: cs.Provider,
				Reason:   cs.RejectionReason,
			})
			continue
		}
		if cs.TotalScore > bestScore {
			bestScore = cs.TotalScore
			bestIdx = i
		}
	}

	routingDuration := time.Since(start).Milliseconds()

	decision := RoutingDecision{
		CandidateScores:   scores,
		RejectionReasons:  rejections,
		RoutingDurationMs: routingDuration,
	}

	if bestIdx < 0 {
		decision.SelectedProvider = ""
		decision.SelectedModelID = modelID
		return &SelectionResult{Decision: decision}, nil
	}

	best := cands[bestIdx]
	decision.SelectedProvider = best.providerName
	decision.SelectedModelID = best.modelID
	decision.SelectedProviderID = best.modelID

	// Mark selected.
	for j := range scores {
		if scores[j].Provider == best.providerName {
			scores[j].Selected = true
		}
	}

	return &SelectionResult{
		Decision: decision,
		Candidate: &Candidate{
			ProviderName:    best.providerName,
			ProviderModelID: best.modelID,
		},
	}, nil
}

func (e *RouterEngine) scoreCandidate(ctx context.Context, c candidateInfo, capHint CapabilityHint) CandidateScore {
	cs := CandidateScore{
		Provider:   c.providerName,
		ProviderID: c.modelID,
	}

	// Check breaker.
	if e.breakerPool != nil {
		b := e.breakerPool.Get(c.providerName)
		if b != nil && b.Allow() != breaker.ResultAllowed {
			cs.Rejected = true
			cs.RejectionReason = "circuit breaker open"
			return cs
		}
	}

	// Health score.
	healthScore := e.getHealthScore(c.providerName, c.modelID)
	cs.HealthScore = healthScore

	// Latency score.
	latencyMs := e.getLatencyMs(c.providerName)
	cs.LatencyScore = latencyScore(latencyMs)

	// Cost score.
	costPerToken := e.getCostPerToken(ctx, c.providerName, c.modelID)
	cs.CostScore = costScore(costPerToken, e.costCeiling)

	// Capability score.
	caps := e.getCapabilities(c.providerName, c.modelID)
	cs.CapScore = matchScore(capHint, caps)

	// Composite.
	e.mu.RLock()
	score := e.scorer.CompositeScore(Candidate{
		ProviderName:    c.providerName,
		ProviderModelID: c.modelID,
		HealthScore:     healthScore,
		LatencyMs:       latencyMs,
		CostPerToken:    costPerToken,
		Capabilities:    caps,
		IsAvailable:     true,
	}, capHint)
	e.mu.RUnlock()

	cs.TotalScore = score
	return cs
}

func (e *RouterEngine) getHealthScore(providerName, modelID string) float64 {
	if e.healthStore == nil {
		return e.getRuntimeHealthScore(providerName)
	}
	// Check per-model status first.
	catalogID := providerName + "/" + modelID
	if st := e.healthStore.Get(catalogID); st != nil {
		switch st.State {
		case health.StateHealthy:
			return 1.0
		case health.StateDegraded:
			return 0.6
		case health.StateRecovering, health.StateUnhealthy:
			return 0.1
		default:
			return e.getRuntimeHealthScore(providerName)
		}
	}
	// Fall back to per-provider metrics.
	if e.metricsStore != nil {
		if m := e.metricsStore.Get(providerName); m != nil {
			return m.HealthScore()
		}
	}
	return e.getRuntimeHealthScore(providerName)
}

func (e *RouterEngine) getRuntimeHealthScore(providerName string) float64 {
	if e.runtime == nil {
		return 0.5
	}
	r, err := e.runtime.Get(providerName)
	if err != nil {
		return 0.5
	}
	switch r.State() {
	case runtime.StateHealthy:
		return 1.0
	case runtime.StateDegraded:
		return 0.6
	case runtime.StateUnhealthy, runtime.StateRecovering:
		return 0.1
	default:
		return 0.5
	}
}

func (e *RouterEngine) getLatencyMs(providerName string) int64 {
	if e.metricsStore != nil {
		if m := e.metricsStore.Get(providerName); m != nil {
			return m.RollingLatencyMs()
		}
	}
	// Fall back to runtime latency.
	if e.runtime != nil {
		r, err := e.runtime.Get(providerName)
		if err == nil {
			return r.Snapshot(context.Background()).LatencyMs
		}
	}
	return 0
}

func (e *RouterEngine) getCostPerToken(ctx context.Context, providerName, modelID string) *float64 {
	p, ok := e.registry.Get(providerName)
	if !ok {
		return nil
	}
	pricing, err := p.GetPricing(ctx)
	if err != nil {
		return nil
	}
	info, ok := pricing[modelID]
	if !ok {
		return nil
	}
	// Return per-token cost (input).
	cost := info.InputPrice / float64(info.UnitSize)
	return &cost
}

func (e *RouterEngine) getCapabilities(providerName, modelID string) Capabilities {
	e.mu.RLock()
	caps, ok := e.providerCaps[providerName]
	e.mu.RUnlock()
	if ok {
		return caps
	}
	// Try to get capabilities from registry metadata first.
	caps = e.registryCapabilities(providerName)
	e.mu.Lock()
	e.providerCaps[providerName] = caps
	e.mu.Unlock()
	return caps
}

func (e *RouterEngine) registryCapabilities(providerName string) Capabilities {
	_, meta, ok := e.registry.GetProviderInfo(providerName)
	if !ok {
		return DefaultCapabilities(providerName, "")
	}
	return metadataToCapabilities(meta)
}

func metadataToCapabilities(meta provider.Metadata) Capabilities {
	return Capabilities{
		Streaming:   meta.Capabilities.Streaming,
		Vision:      meta.Capabilities.Vision,
		Reasoning:   meta.Capabilities.Reasoning,
		ToolCalling: meta.Capabilities.ToolCalling || meta.Capabilities.Functions,
		Structured:  meta.Capabilities.Structured,
		MaxContext:  meta.MaxContextLength,
	}
}

func latencyScore(latencyMs int64) float64 {
	if latencyMs <= 0 {
		return 0.5
	}
	score := 1.0 - 0.8*float64(latencyMs-100)/float64(4900)
	if score < 0 {
		score = 0
	}
	return score
}

func costScore(costPerToken *float64, ceiling float64) float64 {
	if costPerToken == nil {
		return 0.5
	}
	cost := *costPerToken
	if cost <= 0 {
		return 1.0
	}
	if cost >= ceiling {
		return 0.0
	}
	return 1.0 - cost/ceiling
}

// GetDecision returns the last routing decision (for dashboard).
func (e *RouterEngine) GetDecision() *RoutingDecision {
	return nil // decisions are per-request; dashboard uses scores
}

// GetProviderScores returns current scores for all providers (for dashboard).
func (e *RouterEngine) GetProviderScores(capHint CapabilityHint) []ProviderScoreView {
	providers := e.registry.All()
	if len(providers) == 0 {
		return nil
	}

	type cand struct {
		providerName string
		modelID      string
	}
	cands := make([]cand, 0, len(providers))
	for _, p := range providers {
		cands = append(cands, cand{providerName: p.Name(), modelID: "all"})
	}

	out := make([]ProviderScoreView, 0, len(cands))
	for _, c := range cands {
		p, _ := e.registry.Get(c.providerName)
		if p == nil {
			continue
		}
		cs := e.scoreCandidate(context.Background(), candidateInfo{
			provider:     p,
			providerName: c.providerName,
			modelID:      c.modelID,
		}, capHint)

		view := ProviderScoreView{
			Provider:        c.providerName,
			TotalScore:      cs.TotalScore,
			HealthScore:     cs.HealthScore,
			LatencyScore:    cs.LatencyScore,
			CostScore:       cs.CostScore,
			CapScore:        cs.CapScore,
			Selected:        cs.Selected,
			Rejected:        cs.Rejected,
			RejectionReason: cs.RejectionReason,
		}
		out = append(out, view)
	}
	return out
}

// ProviderScoreView is the dashboard view of a provider's routing score.
type ProviderScoreView struct {
	Provider        string  `json:"provider"`
	TotalScore      float64 `json:"total_score"`
	HealthScore     float64 `json:"health_score"`
	LatencyScore    float64 `json:"latency_score"`
	CostScore       float64 `json:"cost_score"`
	CapScore        float64 `json:"capability_score"`
	Selected        bool    `json:"selected"`
	Rejected        bool    `json:"rejected"`
	RejectionReason string  `json:"rejection_reason,omitempty"`
}

// RecordResult records a request result for a provider (for health/latency tracking).
func (e *RouterEngine) RecordResult(providerName string, latencyMs int64, success bool) {
	if e.metricsStore == nil {
		return
	}
	m := e.metricsStore.Get(providerName)
	m.RecordResult(latencyMs, success)
}

// GetMetricsStore returns the metrics store for dashboard access.
func (e *RouterEngine) GetMetricsStore() *MetricsStore {
	return e.metricsStore
}

// GetScorer returns the scorer for dashboard access.
func (e *RouterEngine) GetScorer() *Scorer {
	return e.scorer
}
