package router

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/runtime"
	"go.uber.org/zap"
)

// scoringCore is the shared capability-hierarchy and candidate-scoring state
// used by both RouterEngine (intelligent routing) and AutoResolver (model="auto").
// A single implementation guarantees the auto contract and the routing engine
// score candidates identically — no second model-selection implementation.
type scoringCore struct {
	mu           *sync.RWMutex
	registry     *provider.Registry
	scorer       *Scorer
	breakerPool  *BreakerPool
	providerCaps map[string]Capabilities // cached provider-level capabilities
	modelCaps    map[string]Capabilities // cached model-specific capabilities (key: "provider/modelID")
	costCeiling  float64
}

func newScoringCore(registry *provider.Registry, breakerPool *BreakerPool, scorer *Scorer, costCeiling float64) *scoringCore {
	return &scoringCore{
		mu:           &sync.RWMutex{},
		registry:     registry,
		scorer:       scorer,
		breakerPool:  breakerPool,
		providerCaps: make(map[string]Capabilities),
		modelCaps:    make(map[string]Capabilities),
		costCeiling:  costCeiling,
	}
}

// getCapabilities resolves the capability hierarchy for a (provider, model) pair:
// 1. Explicit model-specific metadata (registry or SetModelCapabilities)
// 2. Provider default metadata from registry
// 3. Heuristic model-name detection (last resort)
func (c *scoringCore) getCapabilities(providerName, modelID string) Capabilities {
	c.mu.RLock()
	key := providerName + "/" + modelID
	caps, ok := c.modelCaps[key]
	c.mu.RUnlock()
	if ok {
		return caps
	}

	// Check registry for model-specific capabilities (set via SetModelCapabilities).
	regMC, regOk := c.registry.GetModelCapabilities(providerName, modelID)
	if regOk {
		providerCaps := c.registryCapabilities(providerName)
		modelCaps := providerCapsToRouter(regMC.Caps)
		caps = mergeModelOverrides(providerCaps, modelCaps)
		caps.MaxContext = regMC.MaxContextLength
		c.mu.Lock()
		c.modelCaps[key] = caps
		c.mu.Unlock()
		return caps
	}

	// Fall back to provider defaults.
	providerCaps := c.registryCapabilities(providerName)
	c.mu.RLock()
	caps, ok = c.providerCaps[providerName]
	c.mu.RUnlock()
	if ok {
		caps = mergeWithHeuristics(caps, modelID)
		c.mu.Lock()
		c.modelCaps[key] = caps
		c.providerCaps[providerName] = providerCaps
		c.mu.Unlock()
		return caps
	}

	// Compute from scratch.
	caps = providerCaps
	if caps == (Capabilities{}) {
		caps = DefaultCapabilities(providerName, modelID)
	} else {
		caps = mergeWithHeuristics(caps, modelID)
	}

	// Cache provider-level and model-specific results.
	c.mu.Lock()
	c.providerCaps[providerName] = providerCaps
	c.modelCaps[key] = caps
	c.mu.Unlock()
	return caps
}

// setModelCapabilities registers explicit model-level capability overrides.
// These take priority over provider defaults and heuristics.
// Key format: "provider/modelID" (matches the internal cache key).
func (c *scoringCore) setModelCapabilities(providerName, modelID string, caps Capabilities) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := providerName + "/" + modelID
	c.modelCaps[key] = caps
	// Invalidate provider-level cache so next lookup recomputes.
	delete(c.providerCaps, providerName)
}

func (c *scoringCore) getCostPerToken(ctx context.Context, providerName, modelID string) *float64 {
	p, ok := c.registry.Get(providerName)
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

func (c *scoringCore) registryCapabilities(providerName string) Capabilities {
	_, meta, ok := c.registry.GetProviderInfo(providerName)
	if !ok {
		return DefaultCapabilities(providerName, "")
	}
	return metadataToCapabilities(meta)
}

// scoreCandidateWithMode scores a candidate using per-decision weight overrides
// and capability bonuses from a mode profile. All operational state is derived
// from the provided RuntimeSnapshot.
func (c *scoringCore) scoreCandidateWithMode(ctx context.Context, cand candidateInfo, capHint CapabilityHint, snapshot runtime.RuntimeSnapshot, weights Weights, bonuses CapabilityBonuses) CandidateScore {
	cs := CandidateScore{
		Provider:   cand.providerName,
		ProviderID: cand.modelID,
	}

	// Check breaker.
	if c.breakerPool != nil {
		b := c.breakerPool.Get(cand.providerName)
		if b != nil && b.Allow() != breaker.ResultAllowed {
			cs.Rejected = true
			cs.RejectionReason = "circuit breaker open"
			return cs
		}
	}

	// Derive ALL operational state from the single snapshot.
	var healthScore float64
	var latencyMs int64
	if snapshot.Providers != nil {
		providerSnap, hasState := snapshot.Providers[cand.providerName]
		healthScore = healthScoreFromSnapshot(providerSnap, hasState)
		if hasState {
			latencyMs = providerSnap.LatencyMs
		}
	}
	cs.HealthScore = healthScore
	cs.LatencyScore = latencyScore(latencyMs)

	// Cost score — static metadata from registry, not runtime state.
	costPerToken := c.getCostPerToken(ctx, cand.providerName, cand.modelID)
	cs.CostScore = costScore(costPerToken, c.costCeiling)

	// Capability score — static metadata from registry.
	caps := c.getCapabilities(cand.providerName, cand.modelID)
	cs.CapScore = matchScore(capHint, caps)

	// Composite score using values derived from the single snapshot.
	c.mu.RLock()
	score := c.scorer.CompositeScoreWithBonuses(Candidate{
		ProviderName:    cand.providerName,
		ProviderModelID: cand.modelID,
		HealthScore:     healthScore,
		LatencyMs:       latencyMs,
		CostPerToken:    costPerToken,
		Capabilities:    caps,
		IsAvailable:     true,
	}, capHint, weights, bonuses)
	c.mu.RUnlock()

	cs.TotalScore = score
	return cs
}

// executionTelemetryPreference computes a mode preference score from the
// provider's execution telemetry in the snapshot. Planning and Agentic modes
// both use this; Agentic passes intensity=1.5 for stronger signal weighting.
//
// Signal-level precedence contract (P3.12):
//
// Each telemetry signal (execution success, tool-call success, retry rate) is
// resolved INDEPENDENTLY:
//   - Model MEASURED (count >= minExecutionSample): the model-level signal is used.
//   - Model UNKNOWN (no entry) or INSUFFICIENT (count < minExecutionSample):
//     the provider-level signal is used if it is MEASURED.
//   - Provider UNKNOWN/INSUFFICIENT: the signal is neutral (0.0).
//
// A measured-poor model signal never falls back to provider data (the model
// signal is authoritative once MEASURED), and a measured model signal never
// blocks a provider signal for a DIFFERENT dimension — e.g. a model with 10
// executions but no tool-call history still uses provider tool-call data.
//
// Semantics of a resolved signal:
//   - Measured good (rate >= goodThreshold): positive bonus
//   - Measured poor (rate > 0 && < goodThreshold): negative penalty
//   - Zero success rate with samples: neutral (documented P3.11 behavior)
//   - Retry rate above maxRetryRate: additional penalty
//
// The preference is bounded to [-0.10*intensity, +0.10*intensity].
const (
	minExecutionSample int64   = 5
	goodExecRate       float64 = 0.80
	goodToolRate       float64 = 0.70
	maxRetryRate       float64 = 0.30
)

// telemetrySignal carries the counters of one telemetry signal at one level.
type telemetrySignal struct {
	count   int64 // denominator: executions for exec/retry, tool calls for tool
	success int64 // successes (executions or tool calls)
	failure int64 // failures (executions or tool calls)
	retries int64 // retries (execution signal only)
}

// resolveTelemetrySignal picks the level for one signal per the P3.12
// precedence contract. It returns ok=false when every level is UNKNOWN or
// INSUFFICIENT (count < minExecutionSample), i.e. the signal is neutral.
func resolveTelemetrySignal(model, provider telemetrySignal) (telemetrySignal, bool) {
	if model.count >= minExecutionSample {
		return model, true
	}
	if provider.count >= minExecutionSample {
		return provider, true
	}
	return telemetrySignal{}, false
}

// ratePref computes the bounded contribution of one resolved rate signal.
func ratePref(sig telemetrySignal, goodRate float64, scale float64, intensity float64) float64 {
	rate := float64(sig.success) / float64(sig.success+sig.failure)
	if rate >= goodRate {
		return scale * intensity * (rate - goodRate + 0.20)
	}
	if rate > 0 {
		return -scale * intensity * (goodRate - rate)
	}
	return 0
}

// retryPref computes the bounded retry-rate penalty of one resolved signal.
func retryPref(sig telemetrySignal, intensity float64) float64 {
	retryRate := float64(sig.retries) / float64(sig.count)
	if retryRate > maxRetryRate {
		return -0.03 * intensity * (retryRate - maxRetryRate) / (1.0 - maxRetryRate)
	}
	return 0
}

func (c *scoringCore) executionTelemetryPreference(snap runtime.RuntimeSnapshot, providerName string, modelID string, intensity float64) float64 {
	if intensity <= 0 {
		intensity = 1.0
	}
	ps, hasState := snap.Providers[providerName]
	if !hasState {
		return 0.0 // unknown — neutral
	}

	// Model-level telemetry for the routed model, when an entry exists.
	var es *runtime.ModelExecutionState
	if modelID != "" && ps.ModelExecutions != nil {
		if state, ok := ps.ModelExecutions[modelID]; ok {
			es = &state
		}
	}

	modelExec := telemetrySignal{}
	modelTool := telemetrySignal{}
	if es != nil {
		modelExec = telemetrySignal{count: es.ExecutionCount, success: es.ExecutionSuccessCount, failure: es.ExecutionFailureCount, retries: es.RetryCount}
		modelTool = telemetrySignal{count: es.ToolCallSuccessCount + es.ToolCallFailureCount, success: es.ToolCallSuccessCount, failure: es.ToolCallFailureCount}
	}
	providerExec := telemetrySignal{count: ps.ExecutionCount, success: ps.ExecutionSuccessCount, failure: ps.ExecutionFailureCount, retries: ps.RetryCount}
	providerTool := telemetrySignal{count: ps.ToolCallSuccessCount + ps.ToolCallFailureCount, success: ps.ToolCallSuccessCount, failure: ps.ToolCallFailureCount}

	var pref float64

	// Execution success rate — model MEASURED wins; provider only when the
	// model signal is UNKNOWN/INSUFFICIENT.
	if execSig, ok := resolveTelemetrySignal(modelExec, providerExec); ok {
		pref += ratePref(execSig, goodExecRate, 0.05, intensity)
	}
	// Tool call success rate — resolved independently of the execution signal.
	if toolSig, ok := resolveTelemetrySignal(modelTool, providerTool); ok {
		pref += ratePref(toolSig, goodToolRate, 0.03, intensity)
	}
	// Retry rate penalty — shares the execution denominator, resolved
	// independently so an insufficient model sample cannot hide a retry-prone
	// provider, and vice versa.
	if retrySig, ok := resolveTelemetrySignal(modelExec, providerExec); ok {
		pref += retryPref(retrySig, intensity)
	}

	// Clamp to [-0.10*intensity, +0.10*intensity].
	clamp := 0.10 * intensity
	if pref > clamp {
		pref = clamp
	} else if pref < -clamp {
		pref = -clamp
	}
	return pref
}

// providerCapsToRouter converts provider.Capabilities to router.Capabilities.
func providerCapsToRouter(pc provider.Capabilities) Capabilities {
	return Capabilities{
		Streaming:   pc.Streaming,
		Vision:      pc.Vision,
		Reasoning:   pc.Reasoning,
		ToolCalling: pc.ToolCalling || pc.Functions,
		Structured:  pc.Structured,
		LongContext: pc.LongContext,
		Embeddings:  pc.Embeddings,
		Images:      pc.Images,
		Audio:       pc.Audio,
		Functions:   pc.Functions,
	}
}

// mergeModelOverrides applies explicit model capabilities over provider defaults.
// All fields in modelCaps are treated as explicit overrides (both true and false).
// Callers must populate the full struct when providing model-level overrides,
// since bool fields cannot distinguish "not set" from "false".
// The pointer-level distinction (nil = unknown, non-nil = explicit) handles
// the "unknown vs false" semantic at the metadata boundary.
func mergeModelOverrides(provider, model Capabilities) Capabilities {
	// Model caps represent the complete profile for this model.
	// Apply all fields unconditionally.
	c := provider
	c.Streaming = model.Streaming
	c.Vision = model.Vision
	c.Reasoning = model.Reasoning
	c.ToolCalling = model.ToolCalling
	c.Structured = model.Structured
	c.LongContext = model.LongContext
	c.Embeddings = model.Embeddings
	c.Images = model.Images
	c.Audio = model.Audio
	c.Functions = model.Functions
	if model.MaxContext > 0 {
		c.MaxContext = model.MaxContext
	}
	return c
}

// mergeWithHeuristics applies model-name heuristics as a compatibility fallback
// on top of provider defaults. Heuristics only ADD capabilities (set to true),
// never remove them. This preserves explicit provider declarations.
func mergeWithHeuristics(provider Capabilities, modelID string) Capabilities {
	c := provider
	lower := strings.ToLower(modelID)
	if strings.Contains(lower, "vision") || strings.Contains(lower, "vl") {
		c.Vision = true
	}
	if strings.Contains(lower, "reason") || strings.Contains(lower, "o1") || strings.Contains(lower, "o3") || strings.Contains(lower, "r1") {
		c.Reasoning = true
	}
	return c
}

func metadataToCapabilities(meta provider.Metadata) Capabilities {
	return Capabilities{
		Streaming:   meta.Capabilities.Streaming,
		Vision:      meta.Capabilities.Vision,
		Reasoning:   meta.Capabilities.Reasoning,
		ToolCalling: meta.Capabilities.ToolCalling || meta.Capabilities.Functions,
		Structured:  meta.Capabilities.Structured,
		LongContext: meta.Capabilities.LongContext,
		Embeddings:  meta.Capabilities.Embeddings,
		Images:      meta.Capabilities.Images,
		Audio:       meta.Capabilities.Audio,
		Functions:   meta.Capabilities.Functions,
		MaxContext:  meta.MaxContextLength,
	}
}

// AutoResolverConfig holds the dependencies for catalog-backed auto model
// selection (model="auto"). It mirrors RouterEngineConfig so the same wiring
// can drive both, but AutoResolver itself is independent of routing.enabled.
type AutoResolverConfig struct {
	Registry         *provider.Registry
	Catalog          *catalog.Catalog
	Runtime          runtime.Manager
	BreakerPool      *BreakerPool
	Weights          config.RoutingWeights
	CostCeiling      float64
	HealthyLatencyMs int64
	Logger           *zap.Logger
}

// AutoResolver implements the mode-aware catalog-backed auto model selection
// behind the reserved virtual model "auto". It is a first-class Conductor
// feature: it resolves whenever the catalog exists, regardless of whether the
// intelligent routing engine / DecisionPipeline is enabled.
type AutoResolver struct {
	registry       *provider.Registry
	catalog        *catalog.Catalog
	runtime        runtime.Manager
	logger         *zap.Logger
	healthyLatency int64
	core           *scoringCore
}

// NewAutoResolver creates the catalog-backed auto model resolver.
func NewAutoResolver(cfg AutoResolverConfig) *AutoResolver {
	raw := RawWeights{
		Health:     cfg.Weights.Health,
		Latency:    cfg.Weights.Latency,
		Cost:       cfg.Weights.Cost,
		Capability: cfg.Weights.Capability,
	}
	costCeiling := cfg.CostCeiling
	if costCeiling <= 0 {
		costCeiling = 0.001
	}
	healthyLatency := cfg.HealthyLatencyMs
	if healthyLatency <= 0 {
		healthyLatency = 1000
	}
	scorer := NewScorer(raw)
	scorer.SetCostCeiling(costCeiling)
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	return &AutoResolver{
		registry:       cfg.Registry,
		catalog:        cfg.Catalog,
		runtime:        cfg.Runtime,
		logger:         logger,
		healthyLatency: healthyLatency,
		core:           newScoringCore(cfg.Registry, cfg.BreakerPool, scorer, costCeiling),
	}
}

// Resolve selects the best provider/model for model="auto" using the catalog.
// It applies mode-specific scoring and filtering to choose the optimal
// provider/model. Returns an error if no eligible model is found.
func (r *AutoResolver) Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*SelectionResult, error) {
	if r.catalog == nil {
		return nil, fmt.Errorf("auto model selection requires a catalog")
	}

	// Get the runtime snapshot for health/latency scoring.
	var snapshot runtime.RuntimeSnapshot
	if r.runtime != nil {
		snapshot = r.runtime.Snapshot(ctx)
	}

	// Determine the mode profile for this request.
	mode := ModeDefault
	if req != nil && req.Mode != "" {
		if m, err := ParseMode(req.Mode); err == nil {
			mode = m
		}
	}
	mp := modeProfileForMode(mode)

	// Get healthy models from the catalog (applies reachability filter).
	entries, err := r.catalog.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list catalog: %w", err)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no healthy models available in catalog")
	}

	// Build candidates from catalog entries.
	cands := make([]candidateInfo, 0, len(entries))
	for _, entry := range entries {
		p, found := r.registry.Get(entry.Provider)
		if !found {
			continue
		}
		// Skip providers with open circuit breakers.
		if r.core.breakerPool != nil {
			b := r.core.breakerPool.Get(entry.Provider)
			if b != nil && b.State() == breaker.StateOpen {
				continue
			}
		}
		cands = append(cands, candidateInfo{
			provider:     p,
			providerName: entry.Provider,
			modelID:      entry.ProviderModelID,
		})
	}

	if len(cands) == 0 {
		return nil, fmt.Errorf("no available providers for auto selection")
	}

	// Sort by provider name for deterministic tie-breaking.
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].providerName < cands[j].providerName
	})

	// Score candidates using the same logic as SelectBestProvider.
	capHint := ExtractCapabilityHint(req)
	weights := r.core.scorer.LoadWeights()
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

	// Apply mode-specific hard filters.
	var requiredContext int
	if mp != nil && (mp.Mode == ModeLongHorizon || mp.Mode == ModeAgentic) {
		requiredContext = EstimateRequestTokens(req)
	}

	var bestScore float64 = -1
	var bestIdx int = -1
	scores := make([]CandidateScore, 0, len(cands))
	var rejections []RejectionReason

	for i, c := range cands {
		cs := r.core.scoreCandidateWithMode(ctx, c, capHint, snapshot, weights, bonuses)

		// Record mode-policy contributions.
		modeBonus, contextBonus := capabilityBonusContributions(Candidate{
			ProviderName:    c.providerName,
			ProviderModelID: c.modelID,
			Capabilities:    r.core.getCapabilities(c.providerName, c.modelID),
		}, bonuses)
		cs.ModeBonus = modeBonus
		cs.ContextBonus = contextBonus

		// Vision hard filter.
		if capHint.Vision && !cs.Rejected {
			caps := r.core.getCapabilities(c.providerName, c.modelID)
			if !caps.Vision {
				cs.Rejected = true
				cs.RejectionReason = "vision required: request contains image content"
			}
		}

		// Long Horizon/Agentic context hard filter.
		if requiredContext > 0 && !cs.Rejected {
			caps := r.core.getCapabilities(c.providerName, c.modelID)
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
			caps := r.core.getCapabilities(c.providerName, c.modelID)
			if !caps.Reasoning || !caps.ToolCalling {
				cs.Rejected = true
				cause := "planning requires reasoning+tool_calling capabilities"
				if mp.Mode == ModeAgentic {
					cause = "agentic requires reasoning+tool_calling capabilities"
				}
				cs.RejectionReason = cause
			}
		}

		// Execution telemetry preference.
		if mp != nil && (mp.Mode == ModePlanning || mp.Mode == ModeAgentic) && !cs.Rejected {
			intensity := 1.0
			if mp.Mode == ModeAgentic {
				intensity = 1.5
			}
			pref := r.core.executionTelemetryPreference(snapshot, c.providerName, c.modelID, intensity)
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

	routingDuration := int64(0) // Could track actual duration if needed.

	decision := RoutingDecision{
		CandidateScores:   scores,
		RejectionReasons:  rejections,
		RoutingDurationMs: routingDuration,
	}

	if bestIdx < 0 {
		decision.SelectedProvider = ""
		decision.SelectedModelID = ""
		return &SelectionResult{Decision: decision}, nil
	}

	best := cands[bestIdx]
	decision.SelectedProvider = best.providerName
	decision.SelectedModelID = best.modelID
	decision.SelectedProviderID = best.modelID

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

// GetCatalog returns the catalog backing auto model selection, if any.
func (r *AutoResolver) GetCatalog() *catalog.Catalog {
	return r.catalog
}

// UpdateWeights updates the scoring weights at runtime.
func (r *AutoResolver) UpdateWeights(w config.RoutingWeights) {
	r.core.scorer.UpdateWeights(RawWeights{
		Health:     w.Health,
		Latency:    w.Latency,
		Cost:       w.Cost,
		Capability: w.Capability,
	})
}

// setModelCapabilities registers explicit model-level capability overrides so
// the auto path honors the same capability hierarchy as the routing engine.
func (r *AutoResolver) setModelCapabilities(providerName, modelID string, caps Capabilities) {
	r.core.setModelCapabilities(providerName, modelID, caps)
}
