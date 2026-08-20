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

// VirtualModel represents a capability-based virtual model category.
type VirtualModel string

const (
	VirtualFrontier    VirtualModel = "frontier"
	VirtualCoding      VirtualModel = "coding"
	VirtualReasoning   VirtualModel = "reasoning"
	VirtualAgentic     VirtualModel = "agentic"
	VirtualPlanning    VirtualModel = "planning"
	VirtualLongHorizon VirtualModel = "long_horizon"
	VirtualFast        VirtualModel = "fast"
	VirtualLight       VirtualModel = "light"
	VirtualVision      VirtualModel = "vision"
	VirtualAuto        VirtualModel = "auto"
)

// AllVirtualModels returns the canonical list of virtual models in display order.
func AllVirtualModels() []VirtualModel {
	return []VirtualModel{
		VirtualFrontier,
		VirtualCoding,
		VirtualReasoning,
		VirtualAgentic,
		VirtualPlanning,
		VirtualLongHorizon,
		VirtualFast,
		VirtualLight,
		VirtualVision,
		VirtualAuto,
	}
}

// VirtualModelProfile defines the capability requirements and scoring preferences
// for a virtual model category.
type VirtualModelProfile struct {
	VirtualModel      VirtualModel
	Description       string
	Traits            []string
	HardRequirements  CapabilityHint
	WeightPreferences *RoutingWeightPreferences
	CapabilityBonuses CapabilityBonuses
	RequiredContext   bool // whether to enforce context capacity for this category
}

// VirtualModelResolverConfig holds the dependencies for catalog-backed virtual
// model selection. It mirrors AutoResolverConfig so the same wiring drives both.
type VirtualModelResolverConfig struct {
	Registry         *provider.Registry
	Catalog          *catalog.Catalog
	Runtime          runtime.Manager
	BreakerPool      *BreakerPool
	Weights          config.RoutingWeights
	CostCeiling      float64
	HealthyLatencyMs int64
	Logger           *zap.Logger
}

// VirtualResolver implements catalog-backed virtual model selection for all
// capability-based virtual models. It is a first-class Conductor feature:
// it resolves whenever the catalog exists, regardless of whether the
// intelligent routing engine / DecisionPipeline is enabled.
type VirtualResolver struct {
	registry       *provider.Registry
	catalog        *catalog.Catalog
	runtime        runtime.Manager
	logger         *zap.Logger
	healthyLatency int64
	core           *scoringCore
	profiles       map[VirtualModel]*VirtualModelProfile
	mu             sync.RWMutex
}

// NewVirtualResolver creates the catalog-backed virtual model resolver.
func NewVirtualResolver(cfg VirtualModelResolverConfig) *VirtualResolver {
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
	vr := &VirtualResolver{
		registry:       cfg.Registry,
		catalog:        cfg.Catalog,
		runtime:        cfg.Runtime,
		logger:         logger,
		healthyLatency: healthyLatency,
		core:           newScoringCore(cfg.Registry, cfg.BreakerPool, scorer, costCeiling),
		profiles:       make(map[VirtualModel]*VirtualModelProfile),
	}
	vr.initProfiles()
	return vr
}

// initProfiles initializes the virtual model profiles with their capability
// requirements and scoring preferences.
func (r *VirtualResolver) initProfiles() {
	// Frontier: Best overall / strongest generally available model.
	// High capability weight, balanced health/latency/cost.
	r.profiles[VirtualFrontier] = &VirtualModelProfile{
		VirtualModel: VirtualFrontier,
		Description:  "best overall capability; strongest generally available model",
		Traits:       []string{"capability_weighted", "balanced"},
		WeightPreferences: &RoutingWeightPreferences{
			Health:     30,
			Latency:    20,
			Cost:       10,
			Capability: 40,
		},
		CapabilityBonuses: CapabilityBonuses{
			ToolCalling: 0.15,
			Reasoning:   0.20,
			Structured:  0.10,
		},
	}

	// Coding: Software engineering, code generation, debugging, repository work.
	// Strong tool-calling and reasoning preference.
	r.profiles[VirtualCoding] = &VirtualModelProfile{
		VirtualModel: VirtualCoding,
		Description:  "coding capability high; tool calling and reasoning preferred",
		Traits:       []string{"tool_calling_preference", "reasoning_preference", "capability_weighted"},
		WeightPreferences: &RoutingWeightPreferences{
			Health:     25,
			Latency:    10,
			Cost:       5,
			Capability: 60,
		},
		CapabilityBonuses: CapabilityBonuses{
			ToolCalling: 0.25,
			Reasoning:   0.15,
			Structured:  0.10,
		},
	}

	// Reasoning: Deep reasoning, analysis, difficult problem solving.
	// Highest capability weight, strong reasoning bonus.
	r.profiles[VirtualReasoning] = &VirtualModelProfile{
		VirtualModel: VirtualReasoning,
		Description:  "reasoning capability high; context preferred; latency less important than quality",
		Traits:       []string{"reasoning_preference", "capability_weighted"},
		WeightPreferences: &RoutingWeightPreferences{
			Health:     20,
			Latency:    10,
			Cost:       5,
			Capability: 65,
		},
		CapabilityBonuses: CapabilityBonuses{
			Reasoning: 0.35,
		},
	}

	// Agentic: Tool use, multi-step execution, autonomous workflows.
	// Requires reasoning + tool calling, strong execution reliability, context capacity.
	r.profiles[VirtualAgentic] = &VirtualModelProfile{
		VirtualModel: VirtualAgentic,
		Description:  "reasoning + tool_calling hard requirement; context capacity hard requirement; stronger execution reliability preference",
		Traits:       []string{"reasoning_tool_hard_requirement", "context_hard_requirement", "execution_reliability_preference_strong"},
		HardRequirements: CapabilityHint{
			Reasoning:   true,
			ToolCalling: true,
		},
		WeightPreferences: &RoutingWeightPreferences{
			Health:     55,
			Latency:    10,
			Cost:       5,
			Capability: 30,
		},
		CapabilityBonuses: CapabilityBonuses{
			ToolCalling:     0.30,
			Reasoning:       0.30,
			ContextCapacity: 0.10,
		},
		RequiredContext: true,
	}

	// Planning: Task decomposition, architecture planning, strategy.
	// Requires reasoning + tool calling, execution reliability preference.
	r.profiles[VirtualPlanning] = &VirtualModelProfile{
		VirtualModel: VirtualPlanning,
		Description:  "reasoning + tool_calling hard requirement; execution reliability preference",
		Traits:       []string{"reasoning_tool_hard_requirement", "execution_reliability_preference"},
		HardRequirements: CapabilityHint{
			Reasoning:   true,
			ToolCalling: true,
		},
		WeightPreferences: &RoutingWeightPreferences{
			Health:     40,
			Latency:    10,
			Cost:       5,
			Capability: 45,
		},
		CapabilityBonuses: CapabilityBonuses{
			ToolCalling: 0.20,
			Reasoning:   0.25,
		},
	}

	// Long Horizon: Large context and long-running tasks.
	// Hard context capacity requirement, sustained reliability.
	r.profiles[VirtualLongHorizon] = &VirtualModelProfile{
		VirtualModel: VirtualLongHorizon,
		Description:  "context capacity hard requirement; sustained reliability preference",
		Traits:       []string{"context_hard_requirement", "reliability_preference"},
		WeightPreferences: &RoutingWeightPreferences{
			Health:     40,
			Latency:    10,
			Cost:       5,
			Capability: 45,
		},
		CapabilityBonuses: CapabilityBonuses{
			ContextCapacity: 0.10,
		},
		RequiredContext: true,
	}

	// Fast: Low latency / responsive workloads.
	// Latency dominates, health still matters.
	r.profiles[VirtualFast] = &VirtualModelProfile{
		VirtualModel: VirtualFast,
		Description:  "latency-sensitive; health-protected; capability-neutral",
		Traits:       []string{"latency_sensitive", "health_protected"},
		WeightPreferences: &RoutingWeightPreferences{
			Health:     55,
			Latency:    40,
			Cost:       3,
			Capability: 2,
		},
	}

	// Light: Lightweight / economical workloads.
	// Cost strongly weighted, reasonable capability, latency preferred.
	r.profiles[VirtualLight] = &VirtualModelProfile{
		VirtualModel: VirtualLight,
		Description:  "cost-sensitive; reasonable capability; latency preferred",
		Traits:       []string{"cost_sensitive", "latency_preferred"},
		WeightPreferences: &RoutingWeightPreferences{
			Health:     30,
			Latency:    30,
			Cost:       35,
			Capability: 5,
		},
	}

	// Vision: Multimodal / image-capable workloads.
	// Vision capability is a hard requirement.
	r.profiles[VirtualVision] = &VirtualModelProfile{
		VirtualModel: VirtualVision,
		Description:  "vision capability hard requirement when the request carries image content",
		Traits:       []string{"vision_hard_requirement", "baseline_weights"},
		HardRequirements: CapabilityHint{
			Vision: true,
		},
		WeightPreferences: &RoutingWeightPreferences{
			Health:     40,
			Latency:    20,
			Cost:       15,
			Capability: 25,
		},
	}

	// Auto: Generic automatic selection / backward-compatible fallback.
	// Balanced general-purpose selection using classifier when mode not specified.
	r.profiles[VirtualAuto] = &VirtualModelProfile{
		VirtualModel: VirtualAuto,
		Description:  "balanced general-purpose selection; uses classifier when mode omitted",
		Traits:       []string{"baseline", "classifier_driven"},
		WeightPreferences: &RoutingWeightPreferences{
			Health:     40,
			Latency:    25,
			Cost:       15,
			Capability: 20,
		},
	}
}

// GetProfile returns the profile for a virtual model.
func (r *VirtualResolver) GetProfile(vm VirtualModel) (*VirtualModelProfile, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.profiles[vm]
	return p, ok
}

// Resolve selects the best provider/model for a virtual model category.
// It applies category-specific scoring and filtering to choose the optimal
// provider/model. Returns an error if no eligible model is found.
func (r *VirtualResolver) Resolve(ctx context.Context, virtualModel VirtualModel, req *apitypes.ChatCompletionRequest) (*SelectionResult, error) {
	if r.catalog == nil {
		return nil, fmt.Errorf("virtual model selection requires a catalog")
	}

	profile, ok := r.profiles[virtualModel]
	if !ok {
		return nil, fmt.Errorf("unknown virtual model: %s", virtualModel)
	}

	// Get the runtime snapshot for health/latency scoring.
	var snapshot runtime.RuntimeSnapshot
	if r.runtime != nil {
		snapshot = r.runtime.Snapshot(ctx)
	}

	// Determine the effective mode for this request.
	// For VirtualAuto, use the classifier if no explicit mode is provided.
	// For other virtual models, the mode is implicit in the category but
	// an explicit mode override can still shift weights.
	mode := ModeDefault
	if req != nil && req.Mode != "" {
		if m, err := ParseMode(req.Mode); err == nil {
			mode = m
		}
	} else if virtualModel == VirtualAuto {
		// Use classifier for auto when no explicit mode
		taskText := joinMessages(req.Messages)
		mode = ClassifyRequest(taskText).Mode
	}

	// Get the mode profile for weight/bonus overrides.
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
		return nil, fmt.Errorf("no available providers for virtual model %s", virtualModel)
	}

	// Sort by provider name for deterministic tie-breaking.
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].providerName < cands[j].providerName
	})

	// Score candidates.
	capHint := ExtractCapabilityHint(req)
	weights := r.core.scorer.LoadWeights()
	bonuses := CapabilityBonuses{}

	// For VirtualAuto, the mode profile takes precedence (allows explicit mode override).
	// For other virtual models, the virtual model profile takes precedence.
	if virtualModel == VirtualAuto {
		if mp != nil && mp.WeightPreferences != nil {
			weights = Normalize(RawWeights{
				Health:     mp.WeightPreferences.Health,
				Latency:    mp.WeightPreferences.Latency,
				Cost:       mp.WeightPreferences.Cost,
				Capability: mp.WeightPreferences.Capability,
			})
			bonuses = mp.CapabilityBonuses
		}
		// VirtualAuto profile provides fallback defaults if no mode profile matched.
		if profile.WeightPreferences != nil && (mp == nil || mp.WeightPreferences == nil) {
			weights = Normalize(RawWeights{
				Health:     profile.WeightPreferences.Health,
				Latency:    profile.WeightPreferences.Latency,
				Cost:       profile.WeightPreferences.Cost,
				Capability: profile.WeightPreferences.Capability,
			})
			bonuses = profile.CapabilityBonuses
		}
	} else {
		// For non-auto virtual models, virtual model profile takes precedence.
		if profile.WeightPreferences != nil {
			weights = Normalize(RawWeights{
				Health:     profile.WeightPreferences.Health,
				Latency:    profile.WeightPreferences.Latency,
				Cost:       profile.WeightPreferences.Cost,
				Capability: profile.WeightPreferences.Capability,
			})
			bonuses = profile.CapabilityBonuses
		}
		// Mode profile can still provide additional bonuses (but not override weights).
		if mp != nil && mp.CapabilityBonuses != (CapabilityBonuses{}) && !mp.CapabilityBonuses.IsZero() {
			// Merge bonuses - mode bonuses add to virtual model bonuses
			bonuses.ToolCalling += mp.CapabilityBonuses.ToolCalling
			bonuses.Reasoning += mp.CapabilityBonuses.Reasoning
			bonuses.Structured += mp.CapabilityBonuses.Structured
			bonuses.ContextCapacity += mp.CapabilityBonuses.ContextCapacity
		}
	}

	// Hard requirements from virtual model profile.
	var requiredContext int
	if profile.RequiredContext {
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

		// Virtual model hard requirements (e.g., vision for VirtualVision).
		if !cs.Rejected {
			if profile.HardRequirements.Vision && !cs.Rejected {
				caps := r.core.getCapabilities(c.providerName, c.modelID)
				if !caps.Vision {
					cs.Rejected = true
					cs.RejectionReason = "vision required: virtual model requires vision capability"
				}
			}
			if profile.HardRequirements.Reasoning && !cs.Rejected {
				caps := r.core.getCapabilities(c.providerName, c.modelID)
				if !caps.Reasoning {
					cs.Rejected = true
					cs.RejectionReason = "reasoning required: virtual model requires reasoning capability"
				}
			}
			if profile.HardRequirements.ToolCalling && !cs.Rejected {
				caps := r.core.getCapabilities(c.providerName, c.modelID)
				if !caps.ToolCalling {
					cs.Rejected = true
					cs.RejectionReason = "tool_calling required: virtual model requires tool calling capability"
				}
			}
		}

		// Vision hard filter from request content (in addition to profile).
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
				if profile.VirtualModel == VirtualAgentic {
					cause = "agentic requires sufficient context capacity"
				}
				cs.RejectionReason = fmt.Sprintf("%s (%d < %d)", cause, caps.MaxContext, requiredContext)
			}
		}

		// Planning and Agentic require both Reasoning and ToolCalling.
		if (profile.VirtualModel == VirtualPlanning || profile.VirtualModel == VirtualAgentic) && !cs.Rejected {
			caps := r.core.getCapabilities(c.providerName, c.modelID)
			if !caps.Reasoning || !caps.ToolCalling {
				cs.Rejected = true
				cause := "planning requires reasoning+tool_calling capabilities"
				if profile.VirtualModel == VirtualAgentic {
					cause = "agentic requires reasoning+tool_calling capabilities"
				}
				cs.RejectionReason = cause
			}
		}

		// Execution telemetry preference for Planning and Agentic.
		if (profile.VirtualModel == VirtualPlanning || profile.VirtualModel == VirtualAgentic) && !cs.Rejected {
			intensity := 1.0
			if profile.VirtualModel == VirtualAgentic {
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

	routingDuration := int64(0)

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
	decision.RequestedModelID = string(virtualModel) // Preserve original virtual model

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

// GetCatalog returns the catalog backing virtual model selection.
func (r *VirtualResolver) GetCatalog() *catalog.Catalog {
	return r.catalog
}

// UpdateWeights updates the scoring weights at runtime.
func (r *VirtualResolver) UpdateWeights(w config.RoutingWeights) {
	r.core.scorer.UpdateWeights(RawWeights{
		Health:     w.Health,
		Latency:    w.Latency,
		Cost:       w.Cost,
		Capability: w.Capability,
	})
}

// IsVirtualModel reports whether the given model ID is a virtual model.
func IsVirtualModel(modelID string) bool {
	for _, vm := range AllVirtualModels() {
		if string(vm) == modelID {
			return true
		}
	}
	return false
}

// ParseVirtualModel parses a string as a VirtualModel.
func ParseVirtualModel(s string) (VirtualModel, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, vm := range AllVirtualModels() {
		if string(vm) == s {
			return vm, nil
		}
	}
	return "", fmt.Errorf("unknown virtual model: %s", s)
}
