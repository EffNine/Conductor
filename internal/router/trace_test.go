package router_test

import (
	"context"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

func TestDecisionTraceBuilder(t *testing.T) {
	dcID := router.NewDecisionID()
	snap := runtime.RuntimeSnapshot{
		Providers: map[string]runtime.ProviderStateSnapshot{
			"openai": {State: runtime.StateHealthy, LatencyMs: 100},
		},
	}

	builder := router.NewDecisionTraceBuilder(dcID, snap)
	if builder == nil {
		t.Fatal("expected non-nil builder")
	}

	trace := builder.Build()
	if trace.DecisionID != dcID {
		t.Errorf("DecisionID = %q, want %q", trace.DecisionID, dcID)
	}
	if trace.TraceSchemaVer != router.TraceSchemaVersion() {
		t.Errorf("TraceSchemaVer = %d, want %d", trace.TraceSchemaVer, router.TraceSchemaVersion())
	}
	if trace.RuntimeHash == "" {
		t.Fatal("expected non-empty runtime hash")
	}
}

func TestDecisionTraceBuilderStageResults(t *testing.T) {
	dcID := router.NewDecisionID()
	snap := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{}}

	builder := router.NewDecisionTraceBuilder(dcID, snap)

	stage1 := router.NewStageResult("intent")
	stage1.Complete(5, nil, "")
	builder.AddStageResult(stage1)

	stage2 := router.NewStageResult("capability")
	stage2.Complete(3, nil, "")
	builder.AddStageResult(stage2)

	trace := builder.Build()
	if len(trace.StageResults) != 2 {
		t.Fatalf("expected 2 stage results, got %d", len(trace.StageResults))
	}
	if trace.StageResults[0].Name != "intent" {
		t.Errorf("stage[0].Name = %q, want intent", trace.StageResults[0].Name)
	}
	if trace.StageResults[0].DurationMs != 5 {
		t.Errorf("stage[0].DurationMs = %d, want 5", trace.StageResults[0].DurationMs)
	}
	if trace.StageResults[0].Status != router.StageStatusCompleted {
		t.Errorf("stage[0].Status = %q, want completed", trace.StageResults[0].Status)
	}
}

func TestDecisionTraceBuilderEventRecord(t *testing.T) {
	dcID := router.NewDecisionID()
	snap := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{}}

	builder := router.NewDecisionTraceBuilder(dcID, snap)

	builder.AddEvent(router.EventRecord{
		Type:      "decision.started",
		Timestamp: time.Now().UTC(),
	})
	builder.AddEvent(router.EventRecord{
		Type:      "provider.selected",
		Timestamp: time.Now().UTC(),
	})

	trace := builder.Build()
	if len(trace.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(trace.Events))
	}
}

func TestDecisionTraceBuilderWinner(t *testing.T) {
	dcID := router.NewDecisionID()
	snap := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{}}

	builder := router.NewDecisionTraceBuilder(dcID, snap)

	winner := &router.ResolvedRoute{
		ProviderName:    "openai",
		ProviderModelID: "gpt-4o",
		ModelID:         "gpt-4o",
	}
	builder.SetWinner(winner)

	trace := builder.Build()
	if trace.Winner == nil {
		t.Fatal("expected non-nil winner")
	}
	if trace.Winner.ProviderName != "openai" {
		t.Errorf("Winner.ProviderName = %q, want openai", trace.Winner.ProviderName)
	}
}

func TestDecisionTraceBuilderRejectionReasons(t *testing.T) {
	dcID := router.NewDecisionID()
	snap := runtime.RuntimeSnapshot{Providers: map[string]runtime.ProviderStateSnapshot{}}

	builder := router.NewDecisionTraceBuilder(dcID, snap)

	reasons := []router.RejectionReason{
		{Provider: "groq", Reason: "circuit breaker open"},
	}
	builder.SetRejectionReasons(reasons)

	trace := builder.Build()
	if len(trace.RejectionReasons) != 1 {
		t.Fatalf("expected 1 rejection reason, got %d", len(trace.RejectionReasons))
	}
	if trace.RejectionReasons[0].Provider != "groq" {
		t.Errorf("RejectionReasons[0].Provider = %q, want groq", trace.RejectionReasons[0].Provider)
	}
}

func TestDecisionTraceRuntimeHash(t *testing.T) {
	snap := runtime.RuntimeSnapshot{
		Providers: map[string]runtime.ProviderStateSnapshot{
			"openai": {State: runtime.StateHealthy, LatencyMs: 100},
			"groq":   {State: runtime.StateUnhealthy, LatencyMs: 500},
		},
	}
	hash := router.RuntimeHash(snap)
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	// Same snapshot should produce same hash.
	hash2 := router.RuntimeHash(snap)
	if hash != hash2 {
		t.Errorf("hash mismatch: %q != %q", hash, hash2)
	}
}

func TestDecisionPipelineTraceIntegration(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	eventBus := eventbus.NewEventBus()
	var traceReceived *router.DecisionTrace
	eventBus.Subscribe(eventbus.DecisionTraceCreated, func(e eventbus.Event) {
		if tr, ok := e.Payload.(*router.DecisionTrace); ok {
			traceReceived = tr
		}
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry: reg,
		EventBus: eventBus,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "hello"},
		},
	}

	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if traceReceived == nil {
		t.Fatal("expected trace to be received via event bus")
	}
	if traceReceived.DecisionID == "" {
		t.Fatal("expected non-empty decision ID in trace")
	}
	if len(traceReceived.StageResults) != 4 {
		t.Fatalf("expected 4 stage results, got %d", len(traceReceived.StageResults))
	}
}

func TestDecisionExplanation(t *testing.T) {
	dec := router.RoutingDecision{
		SelectedProvider:   "openai",
		SelectedModelID:    "gpt-4o",
		SelectedProviderID: "gpt-4o",
		RoutingDurationMs:  12,
		CandidateScores: []router.CandidateScore{
			{Provider: "openai", ProviderID: "gpt-4o", TotalScore: 0.9, HealthScore: 1.0, LatencyScore: 0.8, CostScore: 0.7, CapScore: 0.95, Selected: true},
			{Provider: "groq", ProviderID: "llama-3.1-8b", TotalScore: 0.6, HealthScore: 0.5, LatencyScore: 0.9, CostScore: 0.8, CapScore: 0.6, Rejected: true, RejectionReason: "lower health score"},
		},
		RejectionReasons: []router.RejectionReason{
			{Provider: "groq", Reason: "lower health score"},
		},
	}

	exp := router.NewDecisionExplanation(router.NewDecisionID(), dec, nil)
	if exp.DecisionID == "" {
		t.Fatal("expected non-empty decision ID")
	}
	if exp.SelectedProvider != "openai" {
		t.Errorf("SelectedProvider = %q, want openai", exp.SelectedProvider)
	}
	if len(exp.CandidateComparisons) != 2 {
		t.Fatalf("expected 2 candidate comparisons, got %d", len(exp.CandidateComparisons))
	}
	if len(exp.WinningSignals) == 0 {
		t.Fatal("expected winning signals")
	}
}
