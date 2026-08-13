package orchestration

import (
	"context"
	"fmt"

	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

// OrchestrationPipeline runs the full task understanding → planning → routing flow.
type OrchestrationPipeline struct {
	registry *provider.Registry
	engine   *router.RouterEngine
	eventBus *eventbus.EventBus
	logger   *zap.Logger
	verify   VerifyFunc
}

// PipelineConfig holds configuration for the orchestration pipeline.
type PipelineConfig struct {
	Registry *provider.Registry
	Engine   *router.RouterEngine
	EventBus *eventbus.EventBus
	Logger   *zap.Logger
	Verify   VerifyFunc
}

// NewPipeline creates a new orchestration pipeline.
func NewPipeline(cfg PipelineConfig) *OrchestrationPipeline {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	if cfg.Verify == nil {
		cfg.Verify = DefaultVerifier
	}
	return &OrchestrationPipeline{
		registry: cfg.Registry,
		engine:   cfg.Engine,
		eventBus: cfg.EventBus,
		logger:   cfg.Logger,
		verify:   cfg.Verify,
	}
}

// Context holds the mutable state flowing through the pipeline.
type Context struct {
	TaskID       string
	Input        string
	Intent       *Intent
	Capabilities *CapabilityRequirement
	Candidates   []*Candidate
	Selected     *Candidate
	Plan         *Plan
	Verification *VerificationResult
	ToolNames    []string
}

// Execute runs the full orchestration flow for a task.
func (p *OrchestrationPipeline) Execute(ctx context.Context, taskID, input string, toolNames []string) (*Context, error) {
	oc := &Context{
		TaskID:    taskID,
		Input:     input,
		ToolNames: toolNames,
	}

	// Phase 1: Understand (Intent)
	oc.Intent = ClassifyIntent(ctx, input)
	p.logger.Info("intent classified",
		zap.String("task_id", taskID),
		zap.String("intent", oc.Intent.TaskType),
		zap.Float64("confidence", oc.Intent.Confidence),
	)
	p.publishEvent(ctx, "orchestration.intent_resolved", oc)

	// Phase 2: Capabilities
	oc.Capabilities = ResolveCapabilities(ctx, input, oc.Intent, toolNames)
	p.publishEvent(ctx, "orchestration.capabilities_resolved", oc)

	// Phase 3: Candidates
	oc.Candidates = GenerateCandidates(ctx, p.registry, p.engine, oc.Capabilities, "")
	if len(oc.Candidates) > 0 {
		oc.Selected = SelectBestCandidate(oc.Candidates)
	}
	p.publishEvent(ctx, "orchestration.candidates_generated", oc)

	// Phase 4: Plan
	oc.Plan = GeneratePlan(input, oc.Intent.TaskType, oc.Capabilities)
	oc.Plan.TaskID = taskID
	p.publishEvent(ctx, "orchestration.plan_generated", oc)

	return oc, nil
}

// Verify runs verification on the final output.
func (p *OrchestrationPipeline) Verify(ctx context.Context, oc *Context, output string) (*VerificationResult, error) {
	result, err := p.verify(ctx, oc.Input, output, oc.Intent.TaskType)
	if err != nil {
		return nil, fmt.Errorf("verification failed: %w", err)
	}
	oc.Verification = result
	p.publishEvent(ctx, "orchestration.verification_completed", oc)
	return result, nil
}

func (p *OrchestrationPipeline) publishEvent(ctx context.Context, eventType string, oc *Context) {
	if p.eventBus == nil {
		return
	}
	p.eventBus.PublishSync(ctx, eventbus.Event{
		Type:      eventbus.EventType(eventType),
		Payload:   oc,
		Timestamp: 0,
	})
}
