package router

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
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

// scoreEpsilon is the tolerance used when comparing candidate scores. Scores
// that differ by less than epsilon are treated as ties so that deterministic
// tie-breaking (provider-name / explicit route order) decides the winner
// instead of floating-point noise from different operation orders.
const scoreEpsilon = 1e-9

// RoutingDecision is the result of a routing decision.
type RoutingDecision struct {
	SelectedProvider   string            `json:"selected_provider"`
	SelectedModelID    string            `json:"selected_model_id"`
	SelectedProviderID string            `json:"selected_provider_model_id"`
	CandidateScores    []CandidateScore  `json:"candidate_scores"`
	RejectionReasons   []RejectionReason `json:"rejection_reasons,omitempty"`
	RoutingDurationMs  int64             `json:"routing_duration_ms"`
	RequestedModelID   string            `json:"requested_model_id,omitempty"` // original model ID before resolution (for virtual models)
}

// CandidateScore is the score for one candidate provider.
// The component fields (ModeBonus/ContextBonus/TelemetryPref) describe the
// mode-policy contribution to TotalScore. Identity contract (P3.14):
//
//	TotalScore = wH*HealthScore + wL*LatencyScore + wC*CostScore
//	           + wK*CapScore + ModeBonus + ContextBonus + TelemetryPref
//
// (within 1e-9) where w* are the EffectiveWeights recorded in the trace.
type CandidateScore struct {
	Provider        string  `json:"provider"`
	ProviderID      string  `json:"provider_model_id"`
	TotalScore      float64 `json:"total_score"`
	HealthScore     float64 `json:"health_score"`
	LatencyScore    float64 `json:"latency_score"`
	CostScore       float64 `json:"cost_score"`
	CapScore        float64 `json:"capability_score"`
	ModeBonus       float64 `json:"mode_bonus,omitempty"`
	ContextBonus    float64 `json:"context_bonus,omitempty"`
	TelemetryPref   float64 `json:"telemetry_pref,omitempty"`
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
// All operational state (health, latency, error rate, capacity) is derived from
// a single runtime.RuntimeSnapshot per routing decision. The metricsStore field
// is retained solely for observability (RecordResult / GetMetricsStore) and does
// NOT influence provider selection.
//
// Candidate scoring and the capability hierarchy live in the shared scoringCore
// so the auto model resolver (model="auto") uses the exact same implementation.
// The optional autoResolver enables catalog-backed auto model selection on this
// engine; when absent (no catalog wired), auto selection returns an error.
type RouterEngine struct {
	registry       *provider.Registry
	metricsStore   *MetricsStore
	runtime        runtime.Manager
	logger         *zap.Logger
	healthyLatency int64
	core           *scoringCore
	autoResolver   *AutoResolver // optional: enables mode-aware auto model selection
}

// RouterEngineConfig holds configuration for the router engine.
type RouterEngineConfig struct {
	Registry         *provider.Registry
	MetricsStore     *MetricsStore
	BreakerPool      *BreakerPool
	Runtime          runtime.Manager
	Logger           *zap.Logger
	Weights          config.RoutingWeights
	CostCeiling      float64          // max cost per token; 0 uses default
	HealthyLatencyMs int64            // expected healthy latency; 0 uses default
	Catalog          *catalog.Catalog // optional: enables mode-aware auto model selection
	AutoResolver     *AutoResolver    // optional: shared auto resolver (model="auto")
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
	scorer := NewScorer(raw)
	// Keep the scorer's cost factor ceiling in sync with the engine's so the
	// recorded CostScore components describe the composite score exactly.
	scorer.SetCostCeiling(costCeiling)
	core := newScoringCore(cfg.Registry, cfg.BreakerPool, scorer, costCeiling)
	e := &RouterEngine{
		registry:       cfg.Registry,
		metricsStore:   cfg.MetricsStore,
		runtime:        cfg.Runtime,
		logger:         cfg.Logger,
		healthyLatency: healthyLatency,
		core:           core,
	}
	// Auto model selection reuses the shared resolver when provided, otherwise
	// a dedicated one is built from the same wiring (tests construct the engine
	// with a Catalog directly).
	if cfg.AutoResolver != nil {
		e.autoResolver = cfg.AutoResolver
	} else if cfg.Catalog != nil {
		e.autoResolver = NewAutoResolver(AutoResolverConfig{
			Registry:         cfg.Registry,
			Catalog:          cfg.Catalog,
			Runtime:          cfg.Runtime,
			BreakerPool:      cfg.BreakerPool,
			Weights:          cfg.Weights,
			CostCeiling:      cfg.CostCeiling,
			HealthyLatencyMs: cfg.HealthyLatencyMs,
			Logger:           cfg.Logger,
		})
	}
	return e
}

// UpdateWeights updates the scoring weights at runtime.
func (e *RouterEngine) UpdateWeights(w config.RoutingWeights) {
	e.core.scorer.UpdateWeights(RawWeights{
		Health:     w.Health,
		Latency:    w.Latency,
		Cost:       w.Cost,
		Capability: w.Capability,
	})
	if e.autoResolver != nil {
		e.autoResolver.UpdateWeights(w)
	}
}

// SelectBestProvider selects the best provider for a request from all registered providers.
// It takes a single coherent runtime.RuntimeSnapshot at the start of the decision
// and derives all operational scoring inputs (health, latency, error rate, capacity)
// from that snapshot. No additional calls to runtime.Manager or MetricsStore are
// made during scoring. MetricsStore is write-only (RecordResult) and never read for routing.
func (e *RouterEngine) SelectBestProvider(ctx context.Context, modelID string, req *apitypes.ChatCompletionRequest) (*SelectionResult, error) {
	// Single coherent snapshot: all scoring inputs derive from this one point-in-time view.
	var snapshot runtime.RuntimeSnapshot
	if e.runtime != nil {
		snapshot = e.runtime.Snapshot(ctx)
	}
	return e.selectBestProviderWithSnapshot(ctx, modelID, req, snapshot)
}

// selectBestProviderWithSnapshot is the internal implementation that accepts an
// explicit snapshot, avoiding a second acquisition. Used by SelectionStage when
// the pipeline already holds the authoritative snapshot.
func (e *RouterEngine) selectBestProviderWithSnapshot(ctx context.Context, modelID string, req *apitypes.ChatCompletionRequest, snapshot runtime.RuntimeSnapshot) (*SelectionResult, error) {
	return e.selectBestProviderWithMode(ctx, modelID, req, snapshot, nil)
}

// selectBestProviderWithMode is like selectBestProviderWithSnapshot but applies
// per-decision weight overrides and capability bonuses from a mode profile.
func (e *RouterEngine) selectBestProviderWithMode(ctx context.Context, modelID string, req *apitypes.ChatCompletionRequest, snapshot runtime.RuntimeSnapshot, mp *ModeProfile) (*SelectionResult, error) {
	start := time.Now()

	capHint := ExtractCapabilityHint(req)
	providers := e.registry.All()
	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers registered")
	}

	weights := e.core.scorer.LoadWeights()
	bonuses := CapabilityBonuses{}
	if mp != nil && mp.WeightPreferences != nil {
		weights = Normalize(RawWeights{
			Health:     mp.WeightPreferences.Health,
			Latency:    mp.WeightPreferences.Latency,
			Cost:       mp.WeightPreferences.Cost,
			Capability: mp.WeightPreferences.Capability,
		})
		bonuses = mp.CapabilityBonuses
	}

	// Long Horizon and Agentic modes enforce a hard context budget: candidates
	// whose model-specific MaxContext is known and smaller than the request's
	// estimated token requirement are rejected before scoring.
	var requiredContext int
	if mp != nil && (mp.Mode == ModeLongHorizon || mp.Mode == ModeAgentic) {
		requiredContext = EstimateRequestTokens(req)
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

	// Sort by provider name for deterministic tie-breaking when no explicit
	// candidate order exists (auto-mode / unconstrained discovery).
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].providerName < cands[j].providerName
	})

	// Score each candidate using the SAME snapshot.
	scores := make([]CandidateScore, 0, len(cands))
	var rejections []RejectionReason
	var bestScore float64 = -1
	var bestIdx int = -1

	for i, c := range cands {
		cs := e.scoreCandidateWithMode(ctx, c, capHint, snapshot, weights, bonuses)
		// Record the mode-policy contributions that CompositeScoreWithBonuses
		// already added to TotalScore (single source of truth: the scorer).
		// This is trace metadata describing the score, not a second scoring pass.
		modeBonus, contextBonus := capabilityBonusContributions(Candidate{
			ProviderName:    c.providerName,
			ProviderModelID: c.modelID,
			Capabilities:    e.getCapabilities(c.providerName, c.modelID),
		}, bonuses)
		cs.ModeBonus = modeBonus
		cs.ContextBonus = contextBonus
		// Vision hard filter: a request with actual image content requires a
		// vision-capable model. Applied before soft scoring so a non-vision
		// candidate can never win on latency/cost/capability-bonus grounds.
		if capHint.Vision && !cs.Rejected {
			caps := e.getCapabilities(c.providerName, c.modelID)
			if !caps.Vision {
				cs.Rejected = true
				cs.RejectionReason = "vision required: request contains image content"
			}
		}
		// Apply Long Horizon/Agentic context hard filter after scoring so the
		// rejection reason is recorded in the candidate score.
		if requiredContext > 0 && !cs.Rejected {
			caps := e.getCapabilities(c.providerName, c.modelID)
			if caps.MaxContext > 0 && caps.MaxContext < requiredContext {
				cs.Rejected = true
				cause := "insufficient context"
				if mp != nil && mp.Mode == ModeAgentic {
					cause = "agentic requires sufficient context capacity"
				}
				cs.RejectionReason = fmt.Sprintf("%s (%d < %d)", cause, caps.MaxContext, requiredContext)
			}
		}
		// Planning and Agentic require both Reasoning and ToolCalling.
		if mp != nil && (mp.Mode == ModePlanning || mp.Mode == ModeAgentic) && !cs.Rejected {
			caps := e.getCapabilities(c.providerName, c.modelID)
			if !caps.Reasoning || !caps.ToolCalling {
				cs.Rejected = true
				cause := "planning requires reasoning+tool_calling capabilities"
				if mp.Mode == ModeAgentic {
					cause = "agentic requires reasoning+tool_calling capabilities"
				}
				cs.RejectionReason = cause
			}
		}
		// Execution telemetry preference — stronger for Agentic.
		if mp != nil && (mp.Mode == ModePlanning || mp.Mode == ModeAgentic) && !cs.Rejected {
			intensity := 1.0
			if mp.Mode == ModeAgentic {
				intensity = 1.5 // Agentic weights execution signals more heavily
			}
			pref := e.executionTelemetryPreference(snapshot, c.providerName, c.modelID, intensity)
			cs.TelemetryPref = pref
			cs.TotalScore += pref
		}
		scores = append(scores, cs)
		if cs.Rejected {
			rejections = append(rejections, RejectionReason{
				Provider: cs.Provider,
				Reason:   cs.RejectionReason,
			})
			continue
		}
		// Strictly-greater comparison with an epsilon: intended-equal scores
		// can differ by floating-point noise (~1e-16) from different operation
		// orders, which would otherwise let FP noise decide "ties" instead of
		// the documented deterministic tie-break (provider-name / slice order).
		if cs.TotalScore > bestScore+scoreEpsilon {
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

// SelectFromRoutes selects the best provider from a restricted set of resolved routes.
// It is used by the handler to pick among a primary route and its fallbacks.
// Like SelectBestProvider, it uses a single coherent RuntimeSnapshot.
func (e *RouterEngine) SelectFromRoutes(ctx context.Context, routes []ResolvedRoute, req *apitypes.ChatCompletionRequest) (*SelectionResult, error) {
	// Single coherent snapshot.
	var snapshot runtime.RuntimeSnapshot
	if e.runtime != nil {
		snapshot = e.runtime.Snapshot(ctx)
	}
	return e.selectFromRoutesWithMode(ctx, routes, req, snapshot, nil)
}

// SelectFromRoutesWithSnapshot selects the best provider from a restricted set of
// resolved routes using an externally-provided snapshot. This avoids a second
// RuntimeManager.Snapshot() call during a routing decision, preserving snapshot
// coherence. The candidate ORDER is preserved for tie-breaking: primary routes
// before fallbacks.
func (e *RouterEngine) SelectFromRoutesWithSnapshot(ctx context.Context, routes []ResolvedRoute, req *apitypes.ChatCompletionRequest, snapshot runtime.RuntimeSnapshot) (*SelectionResult, error) {
	return e.selectFromRoutesWithMode(ctx, routes, req, snapshot, nil)
}

// selectFromRoutesWithMode is like selectFromRoutesWithSnapshot but applies
// per-decision weight overrides and capability bonuses from a mode profile.
func (e *RouterEngine) selectFromRoutesWithMode(ctx context.Context, routes []ResolvedRoute, req *apitypes.ChatCompletionRequest, snapshot runtime.RuntimeSnapshot, mp *ModeProfile) (*SelectionResult, error) {
	start := time.Now()

	if len(routes) == 0 {
		return nil, fmt.Errorf("no routes provided for selection")
	}

	capHint := ExtractCapabilityHint(req)

	weights := e.core.scorer.LoadWeights()
	bonuses := CapabilityBonuses{}
	if mp != nil && mp.WeightPreferences != nil {
		weights = Normalize(RawWeights{
			Health:     mp.WeightPreferences.Health,
			Latency:    mp.WeightPreferences.Latency,
			Cost:       mp.WeightPreferences.Cost,
			Capability: mp.WeightPreferences.Capability,
		})
		bonuses = mp.CapabilityBonuses
	}

	// Long Horizon and Agentic modes enforce a hard context budget: candidates
	// whose model-specific MaxContext is known and smaller than the request's
	// estimated token requirement are rejected before scoring.
	var requiredContext int
	if mp != nil && (mp.Mode == ModeLongHorizon || mp.Mode == ModeAgentic) {
		requiredContext = EstimateRequestTokens(req)
	}

	// Score each route using the SAME snapshot.
	scores := make([]CandidateScore, 0, len(routes))
	var rejections []RejectionReason
	var bestScore float64 = -1
	var bestIdx int = -1

	for i, r := range routes {
		c := candidateInfo{
			provider:     r.Provider,
			providerName: r.ProviderName,
			modelID:      r.ProviderModelID,
		}
		cs := e.scoreCandidateWithMode(ctx, c, capHint, snapshot, weights, bonuses)
		// Record the mode-policy contributions (see selectBestProviderWithMode).
		modeBonus, contextBonus := capabilityBonusContributions(Candidate{
			ProviderName:    r.ProviderName,
			ProviderModelID: r.ProviderModelID,
			Capabilities:    e.getCapabilities(r.ProviderName, r.ProviderModelID),
		}, bonuses)
		cs.ModeBonus = modeBonus
		cs.ContextBonus = contextBonus
		// Vision hard filter (see selectBestProviderWithMode).
		if capHint.Vision && !cs.Rejected {
			caps := e.getCapabilities(r.ProviderName, r.ProviderModelID)
			if !caps.Vision {
				cs.Rejected = true
				cs.RejectionReason = "vision required: request contains image content"
			}
		}
		// Apply Long Horizon/Agentic context hard filter after scoring.
		if requiredContext > 0 && !cs.Rejected {
			caps := e.getCapabilities(r.ProviderName, r.ProviderModelID)
			if caps.MaxContext > 0 && caps.MaxContext < requiredContext {
				cs.Rejected = true
				cause := "insufficient context"
				if mp != nil && mp.Mode == ModeAgentic {
					cause = "agentic requires sufficient context capacity"
				}
				cs.RejectionReason = fmt.Sprintf("%s (%d < %d)", cause, caps.MaxContext, requiredContext)
			}
		}
		// Planning and Agentic require both Reasoning and ToolCalling.
		if mp != nil && (mp.Mode == ModePlanning || mp.Mode == ModeAgentic) && !cs.Rejected {
			caps := e.getCapabilities(r.ProviderName, r.ProviderModelID)
			if !caps.Reasoning || !caps.ToolCalling {
				cs.Rejected = true
				cause := "planning requires reasoning+tool_calling capabilities"
				if mp.Mode == ModeAgentic {
					cause = "agentic requires reasoning+tool_calling capabilities"
				}
				cs.RejectionReason = cause
			}
		}
		// Execution telemetry preference — stronger for Agentic.
		if mp != nil && (mp.Mode == ModePlanning || mp.Mode == ModeAgentic) && !cs.Rejected {
			intensity := 1.0
			if mp.Mode == ModeAgentic {
				intensity = 1.5 // Agentic weights execution signals more heavily
			}
			pref := e.executionTelemetryPreference(snapshot, r.ProviderName, r.ProviderModelID, intensity)
			cs.TelemetryPref = pref
			cs.TotalScore += pref
		}
		scores = append(scores, cs)
		if cs.Rejected {
			rejections = append(rejections, RejectionReason{
				Provider: cs.Provider,
				Reason:   cs.RejectionReason,
			})
			continue
		}
		if cs.TotalScore > bestScore+scoreEpsilon {
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
		decision.SelectedModelID = routes[0].ProviderModelID
		return &SelectionResult{Decision: decision}, nil
	}

	best := routes[bestIdx]
	decision.SelectedProvider = best.ProviderName
	decision.SelectedModelID = best.ProviderModelID
	decision.SelectedProviderID = best.ProviderModelID

	for j := range scores {
		if scores[j].Provider == best.ProviderName {
			scores[j].Selected = true
		}
	}

	return &SelectionResult{
		Decision: decision,
		Candidate: &Candidate{
			ProviderName:    best.ProviderName,
			ProviderModelID: best.ProviderModelID,
		},
	}, nil
}

// SelectAutoModel selects the best model for "auto" mode using the catalog.
// It applies mode-specific scoring and filtering to choose the optimal provider/model.
// Returns an error if auto selection is not configured or no eligible model is found.
//
// The implementation lives in AutoResolver (the shared catalog-backed auto
// selection service); the engine delegates so the auto contract is identical
// whether or not intelligent routing is enabled.
func (e *RouterEngine) SelectAutoModel(ctx context.Context, req *apitypes.ChatCompletionRequest) (*SelectionResult, error) {
	if e.autoResolver == nil {
		return nil, fmt.Errorf("auto model selection requires a catalog")
	}
	return e.autoResolver.Resolve(ctx, req)
}

// AutoModelResolver exposes the catalog-backed auto model resolver, if wired.
// Callers use it for dynamic fallback candidate generation.
func (e *RouterEngine) AutoModelResolver() *AutoResolver {
	return e.autoResolver
}

// scoreCandidateWithMode scores a candidate using per-decision weight overrides
// and capability bonuses from a mode profile. All operational state is derived
// from the provided RuntimeSnapshot. The implementation lives in scoringCore,
// shared with AutoResolver so both paths score identically.
func (e *RouterEngine) scoreCandidateWithMode(ctx context.Context, c candidateInfo, capHint CapabilityHint, snapshot runtime.RuntimeSnapshot, weights Weights, bonuses CapabilityBonuses) CandidateScore {
	return e.core.scoreCandidateWithMode(ctx, c, capHint, snapshot, weights, bonuses)
}

// scoreCandidateFromRouteWithMode is like scoreCandidateFromRoute but applies
// per-decision weight overrides and capability bonuses. It is identical to
// scoringCore.scoreCandidateWithMode except that breakers are NOT checked here
// (they are checked at execution); the core's breaker check never triggers for
// routes because execution filtering is the caller's responsibility, so the
// shared core is used directly.
func (e *RouterEngine) scoreCandidateFromRouteWithMode(ctx context.Context, route ResolvedRoute, capHint CapabilityHint, snapshot runtime.RuntimeSnapshot, weights Weights, bonuses CapabilityBonuses) CandidateScore {
	cs := CandidateScore{
		Provider:   route.ProviderName,
		ProviderID: route.ProviderModelID,
	}

	var healthScore float64
	var latencyMs int64
	if snapshot.Providers != nil {
		providerSnap, hasState := snapshot.Providers[route.ProviderName]
		healthScore = healthScoreFromSnapshot(providerSnap, hasState)
		if hasState {
			latencyMs = providerSnap.LatencyMs
		}
	}
	cs.HealthScore = healthScore
	cs.LatencyScore = latencyScore(latencyMs)

	costPerToken := e.core.getCostPerToken(ctx, route.ProviderName, route.ProviderModelID)
	cs.CostScore = costScore(costPerToken, e.core.costCeiling)

	caps := e.core.getCapabilities(route.ProviderName, route.ProviderModelID)
	cs.CapScore = matchScore(capHint, caps)

	e.core.mu.RLock()
	score := e.core.scorer.CompositeScoreWithBonuses(Candidate{
		ProviderName:    route.ProviderName,
		ProviderModelID: route.ProviderModelID,
		HealthScore:     healthScore,
		LatencyMs:       latencyMs,
		CostPerToken:    costPerToken,
		Capabilities:    caps,
		IsAvailable:     true,
	}, capHint, weights, bonuses)
	e.core.mu.RUnlock()

	cs.TotalScore = score
	return cs
}

// healthScoreFromSnapshot derives a health score in [0,1] from a RuntimeSnapshot.
// It combines the state-based score with an error-rate penalty so that providers
// with high recent error rates score lower even when in a healthy state.
func healthScoreFromSnapshot(snap runtime.ProviderStateSnapshot, hasState bool) float64 {
	if !hasState {
		return 0.5 // unknown — neutral
	}
	var baseScore float64
	switch snap.State {
	case runtime.StateHealthy:
		baseScore = 1.0
	case runtime.StateDegraded:
		baseScore = 0.6
	case runtime.StateUnhealthy, runtime.StateRecovering:
		baseScore = 0.1
	default:
		baseScore = 0.5
	}
	// Penalize high error rates. At 50%+ error rate, subtract up to 0.5.
	errorPenalty := snap.ErrorRate * 0.5
	score := baseScore - errorPenalty
	if score < 0 {
		score = 0
	}
	return score
}

// executionTelemetryPreference delegates to the shared scoringCore (single
// implementation shared with the auto resolver). Contract documented on
// scoringCore.executionTelemetryPreference.
func (e *RouterEngine) executionTelemetryPreference(snap runtime.RuntimeSnapshot, providerName string, modelID string, intensity float64) float64 {
	return e.core.executionTelemetryPreference(snap, providerName, modelID, intensity)
}

func (e *RouterEngine) getCapabilities(providerName, modelID string) Capabilities {
	return e.core.getCapabilities(providerName, modelID)
}

// SetModelCapabilities registers explicit model-level capability overrides.
// These take priority over provider defaults and heuristics.
// Key format: "provider/modelID" (matches the internal cache key).
func (e *RouterEngine) SetModelCapabilities(providerName, modelID string, caps Capabilities) {
	e.core.setModelCapabilities(providerName, modelID, caps)
	if e.autoResolver != nil {
		e.autoResolver.setModelCapabilities(providerName, modelID, caps)
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

// LoadCatalogCapabilities registers model-level capability overrides from
// catalog entries. Entries without model-level metadata are skipped — their
// provider defaults remain in effect. This is the bridge between catalog
// discovery and the router's capability hierarchy.
func (e *RouterEngine) LoadCatalogCapabilities(entries []catalog.Entry) {
	for _, entry := range entries {
		if entry.Capabilities == nil {
			continue
		}
		// entry.ModelID is the fully-qualified ID (e.g. "openai/gpt-4o").
		// Use it as the modelID key so lookups match the routing flow.
		e.SetModelCapabilities(entry.Provider, entry.ModelID, Capabilities{
			Streaming:   entry.Capabilities.Streaming,
			Vision:      entry.Capabilities.Vision,
			Reasoning:   entry.Capabilities.Reasoning,
			ToolCalling: entry.Capabilities.ToolCalling || entry.Capabilities.Functions,
			Structured:  entry.Capabilities.Structured,
			LongContext: entry.Capabilities.LongContext,
			Embeddings:  entry.Capabilities.Embeddings,
			Images:      entry.Capabilities.Images,
			Audio:       entry.Capabilities.Audio,
			Functions:   entry.Capabilities.Functions,
			MaxContext:  entry.MaxContextLength,
		})
	}
}

// GetProviderScores returns current scores for all providers (for dashboard).
// Uses a single coherent RuntimeSnapshot so all scores are consistent.
func (e *RouterEngine) GetProviderScores(capHint CapabilityHint) []ProviderScoreView {
	// Single snapshot for dashboard scores; fall back to empty if runtime is nil.
	var snapshot runtime.RuntimeSnapshot
	if e.runtime != nil {
		snapshot = e.runtime.Snapshot(context.Background())
	}
	return e.GetProviderScoresWithSnapshot(capHint, snapshot)
}

// GetProviderScoresWithSnapshot returns scores for all providers computed from
// an externally-provided snapshot. This lets the DecisionPipeline score the
// candidate-display list from the SAME snapshot the selection uses, preserving
// the single-snapshot-per-decision invariant.
func (e *RouterEngine) GetProviderScoresWithSnapshot(capHint CapabilityHint, snapshot runtime.RuntimeSnapshot) []ProviderScoreView {
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
		cs := e.scoreCandidateWithMode(context.Background(), candidateInfo{
			provider:     p,
			providerName: c.providerName,
			modelID:      c.modelID,
		}, capHint, snapshot, e.core.scorer.LoadWeights(), CapabilityBonuses{})

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
	return e.core.scorer
}

// GetCatalog returns the catalog used for auto model selection, if any.
func (e *RouterEngine) GetCatalog() *catalog.Catalog {
	if e.autoResolver == nil {
		return nil
	}
	return e.autoResolver.GetCatalog()
}
