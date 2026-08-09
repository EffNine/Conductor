package router

import (
	"context"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/policy"
	"github.com/EffNine/conductor/internal/provider"
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

// IntentStage resolves the request intent (task type, confidence, description).
// This is a placeholder implementation that preserves current behaviour.
type IntentStage struct{}

// NewIntentStage creates a new IntentStage.
func NewIntentStage() *IntentStage { return &IntentStage{} }

func (s *IntentStage) Name() string { return "intent" }

func (s *IntentStage) Execute(_ context.Context, dc *DecisionContext) error {
	// Current behaviour: no ML-based intent resolution.
	// The pipeline preserves existing routing logic via the Selection stage.
	return nil
}

// CapabilityStage resolves the capability requirements of a request.
// It derives CapabilityHint from the request and stores it in the context.
type CapabilityStage struct{}

// NewCapabilityStage creates a new CapabilityStage.
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
	return nil
}

// CandidateStage generates provider candidates for the request.
// Each candidate contains provider, model, capability match, runtime snapshot,
// estimated cost, estimated latency, and availability.
type CandidateStage struct {
	registry *provider.Registry
}

// NewCandidateStage creates a new CandidateStage.
func NewCandidateStage(reg *provider.Registry) *CandidateStage {
	return &CandidateStage{registry: reg}
}

func (s *CandidateStage) Name() string { return "candidate" }

func (s *CandidateStage) Execute(ctx context.Context, dc *DecisionContext) error {
	// Candidate generation is handled by the Selection stage using RouterEngine.
	// This stage is a placeholder for future explicit candidate generation logic.
	_ = ctx
	_ = dc
	_ = s.registry
	return nil
}

// SelectionStage performs the final provider selection.
// It delegates to the existing RouterEngine logic to preserve behaviour.
type SelectionStage struct {
	engine *RouterEngine
}

// NewSelectionStage creates a new SelectionStage.
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
	result, err := s.engine.SelectBestProvider(dc.Context(), dc.TaskMetadata().ModelID, req)
	if err != nil {
		return dc.Err("selection failed", err)
	}
	if result == nil {
		return nil
	}
	// Store the selection result in the context for downstream consumers.
	dc.SetSelection(result)
	return nil
}
