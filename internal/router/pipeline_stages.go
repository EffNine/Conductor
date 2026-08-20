package router

import (
	"context"
	"fmt"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/policy"
)

// PipelineStage is the interface that every decision pipeline stage must implement.
// Each stage receives a mutable DecisionContext and produces typed outputs that
// are stored back into the context by the pipeline orchestrator.
type PipelineStage interface {
	// Name returns the human-readable stage name.
	Name() string

	// Execute runs the stage logic. Errors abort the pipeline.
	Execute(ctx context.Context, dc *DecisionContext) error
}

// IntentStage resolves the request intent (task type, confidence, description)
// and derives the mode profile with per-decision scoring weights.
type IntentStage struct{}

// NewIntentStage creates a new IntentStage.
func NewIntentStage() *IntentStage { return &IntentStage{} }

func (s *IntentStage) Name() string { return "intent" }

func (s *IntentStage) Execute(_ context.Context, dc *DecisionContext) error {
	req := dc.Request()

	requestedMode := ""
	if req != nil {
		requestedMode = req.Mode
		// Record the raw request mode before any validation so a failed
		// decision still produces an explainable trace.
		dc.SetRequestedMode(req.Mode)
	}

	// Resolve mode: explicit request.mode takes precedence over classifier.
	var mode Mode
	var modeSource string
	var confidence float64
	if requestedMode != "" {
		var err error
		mode, err = ParseMode(requestedMode)
		if err != nil {
			return dc.Err("invalid mode", err)
		}
		modeSource = "explicit"
		confidence = 0.9
	} else {
		taskText := ""
		if len(req.Messages) > 0 {
			taskText = req.Messages[0].ContentString()
		}
		profile := ClassifyRequest(taskText)
		mode = profile.Mode
		modeSource = "classifier"
		confidence = profile.Confidence
	}
	dc.SetModeSource(modeSource)

	// Inactive modes are recognized by the public API but have no routing
	// implementation. Surface a clear client error instead of falling back.
	// Mode resolution metadata is recorded before this check so the failure
	// trace can explain the rejected mode.
	mp := modeProfileForMode(mode)
	dc.SetModeProfile(mp)
	if mp != nil && !mp.Active {
		return dc.Err(fmt.Sprintf("mode %q is not yet supported", mode), fmt.Errorf("mode %q is not yet supported", mode))
	}

	policyIntent := &policy.Intent{
		TaskType:    taskTypeFromMode(mode),
		Confidence:  confidence,
		Description: string(mode),
		Metadata: map[string]any{
			"mode_source":    modeSource,
			"requested_mode": requestedMode,
		},
	}
	dc.SetIntent(policyIntent)

	// Derive the mode profile and per-decision effective weights.
	dc.SetEffectiveWeights(mp.effectiveWeights())
	return nil
}

// modeProfileForMode returns a copy of the ModeProfile for a given mode,
// falling back to the default profile for unknown modes. The returned profile
// is a defensive copy so that request-local mutations cannot affect global state.
func modeProfileForMode(mode Mode) *ModeProfile {
	profiles := DefaultModeProfiles()
	if mp, ok := profiles[mode]; ok {
		return mp.copy()
	}
	if mp, ok := profiles[ModeDefault]; ok {
		return mp.copy()
	}
	return &ModeProfile{Mode: mode}
}

// copy returns a shallow copy of the mode profile with a newly allocated
// WeightPreferences so that per-request mutations cannot leak into global state.
func (mp *ModeProfile) copy() *ModeProfile {
	c := *mp
	if mp.WeightPreferences != nil {
		wp := *mp.WeightPreferences
		c.WeightPreferences = &wp
	}
	return &c
}

// effectiveWeights returns the normalized weights for this mode profile.
// When WeightPreferences is nil, global defaults are used.
func (mp *ModeProfile) effectiveWeights() Weights {
	if mp == nil || mp.WeightPreferences == nil {
		return Normalize(RawWeights{Health: 40, Latency: 25, Cost: 15, Capability: 20})
	}
	return Normalize(RawWeights{
		Health:     mp.WeightPreferences.Health,
		Latency:    mp.WeightPreferences.Latency,
		Cost:       mp.WeightPreferences.Cost,
		Capability: mp.WeightPreferences.Capability,
	})
}

// CapabilityStage resolves the capability requirements of a request.
type CapabilityStage struct{}

func NewCapabilityStage() *CapabilityStage { return &CapabilityStage{} }

func (s *CapabilityStage) Name() string { return "capability" }

func (s *CapabilityStage) Execute(_ context.Context, dc *DecisionContext) error {
	hint := ExtractCapabilityHint(dc.Request())
	cr := &policy.CapabilityRequirement{
		NeedsStreaming:   hint.Streaming,
		NeedsVision:      hint.Vision,
		NeedsReasoning:   hint.Reasoning,
		NeedsToolCalling: hint.ToolCalling,
		NeedsStructured:  hint.Structured,
	}
	dc.SetCapability(cr)

	// Compute the estimated context requirement for modes that enforce it.
	if mp := dc.ModeProfile(); mp != nil && (mp.Mode == ModeLongHorizon || mp.Mode == ModeAgentic) {
		dc.SetContextRequirement(EstimateRequestTokens(dc.Request()))
	}
	return nil
}

// CandidateStage generates provider candidates for the request.
type CandidateStage struct {
	engine *RouterEngine
}

func NewCandidateStage(engine *RouterEngine) *CandidateStage {
	return &CandidateStage{engine: engine}
}

func (s *CandidateStage) Name() string { return "candidate" }

func (s *CandidateStage) Execute(ctx context.Context, dc *DecisionContext) error {
	if s.engine == nil || dc.Request() == nil {
		return nil
	}
	hint := ExtractCapabilityHint(dc.Request())
	weights := dc.EffectiveWeights()
	bonuses := dc.ModeBonuses()

	// If pre-resolved candidates are available, score only those.
	if candidates := dc.Candidates(); len(candidates) > 0 {
		scores := make([]ProviderScoreView, 0, len(candidates))
		for _, c := range candidates {
			cs := s.engine.scoreCandidateFromRouteWithMode(ctx, c, hint, dc.RuntimeSnapshot(), weights, bonuses)
			scores = append(scores, ProviderScoreView{
				Provider:        cs.Provider,
				TotalScore:      cs.TotalScore,
				HealthScore:     cs.HealthScore,
				LatencyScore:    cs.LatencyScore,
				CostScore:       cs.CostScore,
				CapScore:        cs.CapScore,
				Selected:        cs.Selected,
				Rejected:        cs.Rejected,
				RejectionReason: cs.RejectionReason,
			})
		}
		dc.SetCandidateScores(scores)
		return nil
	}

	scores := s.engine.GetProviderScoresWithSnapshot(hint, dc.RuntimeSnapshot())
	if len(scores) == 0 {
		return nil
	}
	dc.SetCandidateScores(scores)
	return nil
}

// SelectionStage performs the final provider selection.
type SelectionStage struct {
	engine *RouterEngine
}

func NewSelectionStage(engine *RouterEngine) *SelectionStage {
	return &SelectionStage{engine: engine}
}

func (s *SelectionStage) Name() string { return "selection" }

func (s *SelectionStage) Execute(_ context.Context, dc *DecisionContext) error {
	if s.engine == nil {
		return nil
	}
	req := dc.Request()
	if req == nil {
		req = &apitypes.ChatCompletionRequest{}
	}

	// Pass the pipeline's authoritative snapshot explicitly to avoid a second
	// RuntimeManager.Snapshot() call during selection.
	snapshot := dc.RuntimeSnapshot()
	mp := dc.ModeProfile()

	// If pre-resolved candidates are available, score only those.
	if candidates := dc.Candidates(); len(candidates) > 0 {
		result, err := s.engine.selectFromRoutesWithMode(dc.Context(), candidates, req, snapshot, mp)
		if err != nil {
			return dc.Err("selection failed", err)
		}
		if result == nil {
			return nil
		}
		dc.SetSelection(result)
		return nil
	}

	result, err := s.engine.selectBestProviderWithMode(dc.Context(), dc.TaskMetadata().ModelID, req, snapshot, mp)
	if err != nil {
		return dc.Err("selection failed", err)
	}
	if result == nil {
		return nil
	}
	dc.SetSelection(result)
	return nil
}

// taskTypeFromMode maps a canonical Mode to a policy.TaskType.
// elite and coding both map to TaskTypeCode; fast and default map to TaskTypeChat.
func taskTypeFromMode(mode Mode) policy.TaskType {
	switch mode {
	case ModeElite, ModeCoding:
		return policy.TaskTypeCode
	case ModeReasoning:
		return policy.TaskTypeReasoning
	case ModeVision:
		return policy.TaskTypeVision
	case ModeFast, ModeDefault:
		return policy.TaskTypeChat
	case ModePlanning, ModeAgentic, ModeLongHorizon:
		return policy.TaskTypeChat
	default:
		return policy.TaskTypeChat
	}
}
