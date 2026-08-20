package router

import (
	"context"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/runtime"
	"go.uber.org/zap"
)

// DecisionPipeline orchestrates the deterministic decision flow.
type DecisionPipeline struct {
	stages        []PipelineStage
	eventBus      *eventbus.EventBus
	logger        *zap.Logger
	routingEngine *RouterEngine
	runtimeMgr    runtime.Manager
}

// PipelineConfig holds configuration for the decision pipeline.
type PipelineConfig struct {
	Registry         *provider.Registry
	RoutingEngine    *RouterEngine
	RuntimeManager   runtime.Manager
	BreakerPool      *BreakerPool
	Logger           *zap.Logger
	EventBus         *eventbus.EventBus
	Weights          config.RoutingWeights
	CostCeiling      float64
	HealthyLatencyMs int64
}

// NewDecisionPipeline creates a pipeline with the default stage ordering:
//
//	Intent → Capability → Candidate → Selection
func NewDecisionPipeline(cfg PipelineConfig) *DecisionPipeline {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	re := cfg.RoutingEngine
	if re == nil {
		// Fallback: construct a minimal engine when none is provided.
		re = NewRouterEngine(RouterEngineConfig{
			Registry:         cfg.Registry,
			Runtime:          cfg.RuntimeManager,
			BreakerPool:      cfg.BreakerPool,
			Logger:           logger,
			Weights:          cfg.Weights,
			CostCeiling:      cfg.CostCeiling,
			HealthyLatencyMs: cfg.HealthyLatencyMs,
		})
	}

	p := &DecisionPipeline{
		eventBus:      cfg.EventBus,
		logger:        logger,
		routingEngine: re,
		runtimeMgr:    cfg.RuntimeManager,
	}

	// Stages in deterministic order.
	p.stages = []PipelineStage{
		NewIntentStage(),
		NewCapabilityStage(),
		NewCandidateStage(re),
		NewSelectionStage(re),
	}

	return p
}

// Stages returns the ordered list of pipeline stages (for testing).
func (p *DecisionPipeline) Stages() []PipelineStage { return p.stages }

// RoutingEngine returns the underlying router engine (for dashboard access).
func (p *DecisionPipeline) RoutingEngine() *RouterEngine { return p.routingEngine }

// Execute runs the full decision pipeline for a request.
func (p *DecisionPipeline) Execute(
	ctx context.Context,
	req *apitypes.ChatCompletionRequest,
	env Environment,
	cfgSnap ConfigSnapshot,
	candidates []ResolvedRoute,
) (*SelectionResult, error) {
	if p.logger == nil {
		p.logger = zap.NewNop()
	}

	// Build task metadata.
	taskMeta := TaskMetadata{
		ModelID:      req.Model,
		IsStream:     req.Stream,
		MessageCount: len(req.Messages),
	}
	for _, m := range req.Messages {
		if m.HasContentParts() {
			for _, part := range m.Content.([]apitypes.ContentPart) {
				if part.Type == apitypes.ContentPartImageURL && part.ImageURL != nil && part.ImageURL.URL != "" {
					taskMeta.HasImage = true
				}
			}
		}
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		taskMeta.HasTools = true
	}

	// Acquire the authoritative runtime snapshot — one per decision.
	var rSnap runtime.RuntimeSnapshot
	if p.runtimeMgr != nil {
		rSnap = p.runtimeMgr.Snapshot(ctx)
	}

	// Create decision context.
	dc := NewDecisionContext(req, rSnap, cfgSnap, taskMeta, env, p.logger, p.eventBus)
	if len(candidates) > 0 {
		dc.SetCandidates(candidates)
	}
	defer dc.Close()

	// Create trace builder.
	builder := NewDecisionTraceBuilder(dc.ID(), rSnap)

	// Publish DecisionStarted.
	p.publishEvent(ctx, eventbus.DecisionStarted, dc)
	builder.AddEvent(EventRecord{
		Type:      string(eventbus.DecisionStarted),
		Timestamp: time.Now().UTC(),
	})

	// Execute each stage in order, recording traces.
	for _, stage := range p.stages {
		stageStart := time.Now()
		stageResult := NewStageResult(stage.Name())
		builder.AddEvent(EventRecord{
			Type:      stage.Name() + ".started",
			Timestamp: stageStart.UTC(),
		})

		if err := stage.Execute(ctx, dc); err != nil {
			stageResult.Fail(time.Since(stageStart).Milliseconds(), map[string]any{"error": err.Error()})
			builder.AddStageResult(stageResult)
			p.logger.Warn("pipeline stage failed",
				zap.String("stage", stage.Name()),
				zap.String("decision_id", string(dc.ID())),
				zap.Error(err),
			)
			// Failure trace: explain the failed decision without logs. Uses
			// only data already present in the decision context — no second
			// snapshot, no provider calls, no scoring pass.
			populateTraceFromContext(builder, dc)
			builder.AddEvent(EventRecord{
				Type:      string(eventbus.DecisionFinished),
				Timestamp: time.Now().UTC(),
			})
			failureTrace := builder.Build()
			// The decision finished — with failure. Publish DecisionFinished
			// carrying the failure trace so persistence consumers capture
			// failed decisions too.
			p.publishEvent(ctx, eventbus.DecisionFinished, failureTrace)
			p.publishTraceEvent(ctx, failureTrace)
			return nil, fmt.Errorf("stage %s failed: %w", stage.Name(), err)
		}

		stageResult.Complete(time.Since(stageStart).Milliseconds(), nil, "")
		builder.AddStageResult(stageResult)

		switch stage.Name() {
		case "intent":
			p.publishEvent(ctx, eventbus.IntentResolved, dc)
			builder.AddEvent(EventRecord{
				Type:      string(eventbus.IntentResolved),
				Timestamp: time.Now().UTC(),
				Payload: map[string]any{
					"resolved_mode":  string(dc.ModeProfile().Mode),
					"mode_source":    dc.ModeSource(),
					"requested_mode": dc.RequestedMode(),
				},
			})
		case "capability":
			p.publishEvent(ctx, eventbus.CapabilityResolved, dc)
			builder.AddEvent(EventRecord{
				Type:      string(eventbus.CapabilityResolved),
				Timestamp: time.Now().UTC(),
			})
		case "candidate":
			p.publishEvent(ctx, eventbus.CandidatesGenerated, dc)
			builder.AddEvent(EventRecord{
				Type:      string(eventbus.CandidatesGenerated),
				Timestamp: time.Now().UTC(),
			})
		case "selection":
			p.publishEvent(ctx, eventbus.ProviderSelected, dc)
			builder.AddEvent(EventRecord{
				Type:      string(eventbus.ProviderSelected),
				Timestamp: time.Now().UTC(),
			})
		}
	}

	// Build final result.
	result := dc.Selection()
	if result == nil {
		result = &SelectionResult{
			Decision: RoutingDecision{
				SelectedModelID: req.Model,
			},
		}
	}

	// Populate trace from result.
	var winner *ResolvedRoute
	if result.Candidate != nil {
		winner = &ResolvedRoute{
			ProviderName:    result.Candidate.ProviderName,
			ProviderModelID: result.Candidate.ProviderModelID,
			ModelID:         result.Decision.RequestedModelID, // Use original requested model for trace
		}
	}
	populateTraceFromContext(builder, dc)
	builder.SetWinner(winner)
	builder.SetRejectionReasons(result.Decision.RejectionReasons)
	builder.SetCandidateScores(result.Decision.CandidateScores)

	// Finalize the trace before publishing DecisionFinished so the event
	// carries the complete decision artifact (mode resolution, candidate
	// scores, winner, rejections, runtime hash, stage results).
	builder.AddEvent(EventRecord{
		Type:      string(eventbus.DecisionFinished),
		Timestamp: time.Now().UTC(),
	})
	trace := builder.Build()

	// Publish DecisionFinished with the final trace as payload. Persistence
	// consumers subscribe here; routing correctness never depends on them.
	p.publishEvent(ctx, eventbus.DecisionFinished, trace)

	// Publish trace created event.
	p.publishTraceEvent(ctx, trace)

	return result, nil
}

// populateTraceFromContext copies the canonical mode/intent/capability/policy
// fields from the decision context into the trace. It reads only data already
// produced by pipeline stages — it never acquires a snapshot, calls providers,
// or performs another scoring pass.
func populateTraceFromContext(builder *DecisionTraceBuilder, dc *DecisionContext) {
	builder.SetModeResolution(dc.RequestedMode(), dc.ModeProfile(), dc.ModeSource())
	builder.SetRequestedModel(dc.Request().Model)
	builder.SetIntent(dc.Intent())
	builder.SetCapabilityRequirements(dc.Capability())
	builder.SetContextRequirement(dc.ContextRequirement())
	builder.SetEffectiveWeights(dc.EffectiveWeights())
	builder.SetModeBonuses(dc.ModeBonuses())
}

func (p *DecisionPipeline) publishEvent(ctx context.Context, typ eventbus.EventType, payload any) {
	if p.eventBus == nil {
		return
	}
	p.eventBus.PublishSync(ctx, eventbus.Event{
		Type:      typ,
		Payload:   payload,
		Timestamp: time.Now().UnixNano(),
	})
}

func (p *DecisionPipeline) publishTraceEvent(ctx context.Context, trace *DecisionTrace) {
	if p.eventBus == nil {
		return
	}
	p.eventBus.PublishSync(ctx, eventbus.Event{
		Type:      eventbus.DecisionTraceCreated,
		Payload:   trace,
		Timestamp: time.Now().UnixNano(),
	})
}
