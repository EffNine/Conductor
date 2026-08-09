package plugin

import (
	"context"

	"github.com/EffNine/conductor/internal/contracts"
)

// ExtensionPoint identifies a hook in the routing pipeline where plugins can intercept.
type ExtensionPoint string

const (
	// BeforePipeline fires before any pipeline stage runs.
	// Plugins can abort the pipeline by returning an error.
	BeforePipeline ExtensionPoint = "before_pipeline"

	// AfterIntent fires after the Intent stage resolves the request intent.
	// Plugins can observe or modify the intent.
	AfterIntent ExtensionPoint = "after_intent"

	// AfterCapability fires after the Capability stage resolves requirements.
	// Plugins can observe or modify capability requirements.
	AfterCapability ExtensionPoint = "after_capability"

	// AfterCandidateGeneration fires after candidates are generated.
	// Plugins can add, remove, or modify candidates.
	AfterCandidateGeneration ExtensionPoint = "after_candidate_generation"

	// AfterSelection fires after a provider is selected.
	// Plugins can observe or override the selection.
	AfterSelection ExtensionPoint = "after_selection"

	// BeforeExecution fires before the selected provider is called.
	// Plugins can modify the request before execution.
	BeforeExecution ExtensionPoint = "before_execution"

	// AfterExecution fires after the provider returns a response.
	// Plugins can observe or transform the response.
	AfterExecution ExtensionPoint = "after_execution"

	// AfterDecision fires after the full routing decision is complete.
	// Plugins can log, metric, or trigger learning updates.
	AfterDecision ExtensionPoint = "after_decision"
)

// ExtensionHook is the interface for a plugin that intercepts a specific extension point.
type ExtensionHook interface {
	// Point returns the extension point this hook listens to.
	Point() ExtensionPoint

	// Execute runs the hook logic. Returning an error aborts the pipeline
	// for BeforePipeline or triggers error handling for other points.
	Execute(ctx context.Context, input any) (output any, err error)
}

// ExtensionPointFn is an adapter that turns a function into an ExtensionHook.
type ExtensionPointFn struct {
	point   ExtensionPoint
	execute func(ctx context.Context, input any) (output any, err error)
}

// NewExtensionHook creates an ExtensionHook from a function.
func NewExtensionHook(point ExtensionPoint, fn func(ctx context.Context, input any) (output any, err error)) ExtensionHook {
	return &ExtensionPointFn{
		point:   point,
		execute: fn,
	}
}

// Point returns the extension point.
func (h *ExtensionPointFn) Point() ExtensionPoint {
	return h.point
}

// Execute runs the hook function.
func (h *ExtensionPointFn) Execute(ctx context.Context, input any) (output any, err error) {
	return h.execute(ctx, input)
}

// PipelineHook is the typed extension hook for the routing pipeline.
// It provides strongly-typed inputs and outputs for each extension point.
type PipelineHook interface {
	ExtensionHook

	// OnBeforePipeline is called before any pipeline stage.
	// Input: any (the request)
	// Output: (any, error) — return modified request or error to abort
	OnBeforePipeline(ctx context.Context, req any) (any, error)

	// OnAfterIntent is called after intent resolution.
	// Input: *contracts.DecisionContext
	// Output: (*contracts.DecisionContext, error)
	OnAfterIntent(ctx context.Context, dc *contracts.DecisionContext) (*contracts.DecisionContext, error)

	// OnAfterCapability is called after capability resolution.
	// Input: *contracts.DecisionContext
	// Output: (*contracts.DecisionContext, error)
	OnAfterCapability(ctx context.Context, dc *contracts.DecisionContext) (*contracts.DecisionContext, error)

	// OnAfterCandidateGeneration is called after candidate generation.
	// Input: []*contracts.Candidate
	// Output: ([]*contracts.Candidate, error)
	OnAfterCandidateGeneration(ctx context.Context, candidates []*contracts.Candidate) ([]*contracts.Candidate, error)

	// OnAfterSelection is called after provider selection.
	// Input: *contracts.DecisionResult
	// Output: (*contracts.DecisionResult, error)
	OnAfterSelection(ctx context.Context, result *contracts.DecisionResult) (*contracts.DecisionResult, error)

	// OnBeforeExecution is called before provider execution.
	// Input: any (the request)
	// Output: (any, error)
	OnBeforeExecution(ctx context.Context, req any) (any, error)

	// OnAfterExecution is called after provider execution.
	// Input: *contracts.ExecutionResult
	// Output: (*contracts.ExecutionResult, error)
	OnAfterExecution(ctx context.Context, result *contracts.ExecutionResult) (*contracts.ExecutionResult, error)

	// OnAfterDecision is called after the full decision is complete.
	// Input: *contracts.DecisionTrace
	// Output: (*contracts.DecisionTrace, error)
	OnAfterDecision(ctx context.Context, trace *contracts.DecisionTrace) (*contracts.DecisionTrace, error)
}
