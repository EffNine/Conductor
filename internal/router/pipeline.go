package router

import (
	"context"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/provider"
	"go.uber.org/zap"
)

// DecisionPipeline orchestrates the deterministic decision flow.
type DecisionPipeline struct {
	stages        []PipelineStage
	eventBus      *eventbus.EventBus
	logger        *zap.Logger
	routingEngine *RouterEngine
	traceStore    TraceStore
}

// PipelineConfig holds configuration for the decision pipeline.
type PipelineConfig struct {
	Registry         *provider.Registry
	HealthStore      *health.ModelStatusStore
	MetricsStore     *MetricsStore
	BreakerPool      *BreakerPool
	Logger           *zap.Logger
	EventBus         *eventbus.EventBus
	Weights          config.RoutingWeights
	CostCeiling      float64
	HealthyLatencyMs int64
	TraceStore       TraceStore
}

// NewDecisionPipeline creates a pipeline with the default stage ordering:
//
//	Intent → Capability → Candidate → Selection
func NewDecisionPipeline(cfg PipelineConfig) *DecisionPipeline {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	// Build the routing engine first (needed by Selection stage).
	re := NewRouterEngine(RouterEngineConfig{
		Registry:         cfg.Registry,
		HealthStore:      cfg.HealthStore,
		MetricsStore:     cfg.MetricsStore,
		BreakerPool:      cfg.BreakerPool,
		Logger:           logger,
		Weights:          cfg.Weights,
		CostCeiling:      cfg.CostCeiling,
		HealthyLatencyMs: cfg.HealthyLatencyMs,
	})

	p := &DecisionPipeline{
		eventBus:      cfg.EventBus,
		logger:        logger,
		routingEngine: re,
		traceStore:    cfg.TraceStore,
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

	// Build runtime snapshot.
	rSnap := p.buildRuntimeSnapshot(env)

	// Create decision context.
	dc := NewDecisionContext(req, rSnap, cfgSnap, taskMeta, env, p.logger, p.eventBus)
	defer dc.Close()

	// Create trace builder.
	builder := NewDecisionTraceBuilder(dc.ID(), 1, rSnap)

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
			ModelID:         result.Decision.SelectedModelID,
		}
	}
	builder.SetWinner(winner)
	builder.SetRejectionReasons(result.Decision.RejectionReasons)

	// Publish DecisionFinished.
	p.publishEvent(ctx, eventbus.DecisionFinished, dc)
	builder.AddEvent(EventRecord{
		Type:      string(eventbus.DecisionFinished),
		Timestamp: time.Now().UTC(),
	})

	// Publish trace created event.
	trace := builder.Build()
	p.publishTraceEvent(ctx, trace)

	// Persist trace if store is configured.
	if p.traceStore != nil {
		if err := p.traceStore.Save(ctx, trace); err != nil {
			p.logger.Warn("failed to persist trace",
				zap.String("decision_id", string(trace.DecisionID)),
				zap.Error(err),
			)
		}
	}

	return result, nil
}

func (p *DecisionPipeline) buildRuntimeSnapshot(env Environment) RuntimeSnapshot {
	snap := RuntimeSnapshot{
		Providers: make(map[string]ProviderHealthInfo),
	}
	if p.routingEngine == nil {
		return snap
	}
	scores := p.routingEngine.GetProviderScores(CapabilityHint{})
	for _, s := range scores {
		snap.Providers[s.Provider] = ProviderHealthInfo{
			State:     "healthy",
			LatencyMs: 0,
		}
	}
	_ = env
	return snap
}

func (p *DecisionPipeline) publishEvent(ctx context.Context, typ eventbus.EventType, dc *DecisionContext) {
	if p.eventBus == nil {
		return
	}
	dc.PublishSync(typ, dc)
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
