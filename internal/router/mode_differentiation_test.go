package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// calibStubProvider is a test provider with configurable capabilities,
// per-model MaxContext metadata, and per-token pricing.
type calibStubProvider struct {
	name        string
	supportsAll bool
	vision      bool
	reasoning   bool
	toolCalling bool
	structured  bool
	latencyMs   int64
	healthState runtime.ProviderState
	maxContext  int     // 0 = unknown
	costPerUnit float64 // InputPrice per UnitSize units; 0 = no pricing info
}

func (s *calibStubProvider) Name() string { return s.name }
func (s *calibStubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *calibStubProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (s *calibStubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *calibStubProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (s *calibStubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	if s.costPerUnit <= 0 {
		return nil, nil
	}
	return map[string]provider.PricingInfo{
		"m": {UnitSize: 1, InputPrice: s.costPerUnit},
	}, nil
}
func (s *calibStubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *calibStubProvider) SupportsModel(string) bool { return s.supportsAll }
func (s *calibStubProvider) GetMetadata() provider.Metadata {
	return provider.NewMetadata(s.name, provider.Capabilities{
		Streaming:   true,
		Vision:      s.vision,
		Reasoning:   s.reasoning,
		ToolCalling: s.toolCalling,
		Structured:  s.structured,
	})
}

// setupCalibPipeline creates a DecisionPipeline wired to a RuntimeStore with
// per-model MaxContext metadata registered on the engine.
func setupCalibPipeline(t *testing.T, providers ...*calibStubProvider) (*router.DecisionPipeline, *runtime.RuntimeStore, *router.RouterEngine) {
	t.Helper()
	reg := provider.NewRegistry()
	store := runtime.NewRuntimeStore(nil)
	for _, p := range providers {
		reg.Register(p)
		_ = store.Register(runtime.NewProviderRuntime(p.name, p))
	}
	manager := runtime.NewManager(store)

	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Runtime:  manager,
		Weights:  config.DefaultRoutingWeights(),
	})
	for _, p := range providers {
		if p.maxContext > 0 {
			// SetModelCapabilities overrides are REPLACEMENT profiles: bool
			// fields cannot distinguish "not set" from "false", so the full
			// capability struct must be populated or declared capabilities
			// (reasoning, tool_calling, vision, ...) silently reset.
			eng.SetModelCapabilities(p.name, "m", router.Capabilities{
				Streaming:   true,
				Vision:      p.vision,
				Reasoning:   p.reasoning,
				ToolCalling: p.toolCalling,
				Structured:  p.structured,
				MaxContext:  p.maxContext,
			})
		}
	}

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		RoutingEngine:  eng,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})
	return pipeline, store, eng
}

// setHealth updates provider runtime state and latency.
func setHealth(t *testing.T, store *runtime.RuntimeStore, name string, state runtime.ProviderState, latencyMs int64) {
	t.Helper()
	err := store.Update(name, func(r runtime.ProviderRuntime) error {
		r.UpdateState(state, "", nil)
		if latencyMs > 0 {
			r.RecordLatency(latencyMs)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("update %s: %v", name, err)
	}
}

// execReq builds a chat completion request for calibration tests.
func execReq(mode string, content string) *apitypes.ChatCompletionRequest {
	return &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     mode,
		Messages: []apitypes.Message{{Role: "user", Content: content}},
	}
}

// ---- PHASE 3: MODE DIFFERENTIATION ----

// TestFastVsDefaultCanDiffer verifies that Fast mode can select a different
// winner than Default when latency is the differentiating tradeoff:
// Default prefers the healthy-but-slow cheap provider, Fast the degraded-but-
// fast provider.
func TestFastVsDefaultCanDiffer(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "stable-slow", supportsAll: true, latencyMs: 4900, healthState: runtime.StateHealthy, costPerUnit: 0.0001},
		&calibStubProvider{name: "quick-degraded", supportsAll: true, latencyMs: 100, healthState: runtime.StateDegraded, costPerUnit: 0.0005},
	)
	setHealth(t, store, "stable-slow", runtime.StateHealthy, 4900)
	setHealth(t, store, "quick-degraded", runtime.StateDegraded, 100)

	fastReq := execReq("fast", "hi")
	fastRes, err := pipeline.Execute(context.Background(), fastReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("fast Execute: %v", err)
	}
	if fastRes.Decision.SelectedProvider != "quick-degraded" {
		t.Fatalf("fast: expected quick-degraded (latency dominates), got %s", fastRes.Decision.SelectedProvider)
	}

	defReq := execReq("auto", "hi")
	defRes, err := pipeline.Execute(context.Background(), defReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("default Execute: %v", err)
	}
	if defRes.Decision.SelectedProvider != "stable-slow" {
		t.Fatalf("default: expected stable-slow (health dominates), got %s", defRes.Decision.SelectedProvider)
	}
	if fastRes.Decision.SelectedProvider == defRes.Decision.SelectedProvider {
		t.Fatal("fast and default should produce different winners in this scenario")
	}
}

// TestCodingVsDefaultCanDiffer verifies Coding mode can select a different
// winner than Default when tool-calling capability is the tradeoff: Coding
// prefers the tool-calling provider even when degraded, Default prefers the
// healthy provider without tools.
func TestCodingVsDefaultCanDiffer(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "solid", supportsAll: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001},
		&calibStubProvider{name: "toolish", supportsAll: true, toolCalling: true, latencyMs: 3000, healthState: runtime.StateDegraded, costPerUnit: 0.0005},
	)
	setHealth(t, store, "solid", runtime.StateHealthy, 100)
	setHealth(t, store, "toolish", runtime.StateDegraded, 3000)

	req := execReq("auto", "write a function")
	req.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "t"}}}

	codingReq := req
	codingReq.Mode = "coding"
	codingRes, err := pipeline.Execute(context.Background(), codingReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("coding Execute: %v", err)
	}
	if codingRes.Decision.SelectedProvider != "toolish" {
		t.Fatalf("coding: expected toolish (tool calling dominates), got %s", codingRes.Decision.SelectedProvider)
	}

	defReq := execReq("auto", "write a function")
	defReq.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "t"}}}
	defRes, err := pipeline.Execute(context.Background(), defReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("default Execute: %v", err)
	}
	if defRes.Decision.SelectedProvider != "solid" {
		t.Fatalf("default: expected solid (health dominates), got %s", defRes.Decision.SelectedProvider)
	}
}

// TestReasoningVsDefaultCanDiffer verifies Reasoning mode can select a
// different winner than Default when reasoning capability is the tradeoff.
func TestReasoningVsDefaultCanDiffer(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "solid", supportsAll: true, reasoning: false, latencyMs: 100, healthState: runtime.StateHealthy, costPerUnit: 0.0001},
		&calibStubProvider{name: "thinker", supportsAll: true, reasoning: true, latencyMs: 3000, healthState: runtime.StateDegraded, costPerUnit: 0.0005},
	)
	setHealth(t, store, "solid", runtime.StateHealthy, 100)
	setHealth(t, store, "thinker", runtime.StateDegraded, 3000)

	reasonReq := execReq("reasoning", "analyze this")
	reasonReq.ReasoningEffort = "high"
	reasonRes, err := pipeline.Execute(context.Background(), reasonReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("reasoning Execute: %v", err)
	}
	if reasonRes.Decision.SelectedProvider != "thinker" {
		t.Fatalf("reasoning: expected thinker (reasoning dominates), got %s", reasonRes.Decision.SelectedProvider)
	}

	defReq := execReq("auto", "analyze this")
	defReq.ReasoningEffort = "high"
	defRes, err := pipeline.Execute(context.Background(), defReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("default Execute: %v", err)
	}
	if defRes.Decision.SelectedProvider != "solid" {
		t.Fatalf("default: expected solid (health dominates), got %s", defRes.Decision.SelectedProvider)
	}
}

// TestPlanningVsReasoningDiffer verifies Planning hard-rejects candidates
// lacking tool calling while Reasoning merely soft-scores them.
func TestPlanningVsReasoningDiffer(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "no-tools", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "no-tools", runtime.StateHealthy, 50)

	req := execReq("", "plan a release")
	req.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "t"}}}

	planReq := *req
	planReq.Mode = "planning"
	planRes, err := pipeline.Execute(context.Background(), &planReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("planning Execute: %v", err)
	}
	if planRes.Decision.SelectedProvider != "" {
		t.Fatalf("planning: expected no selection (hard tool_calling requirement), got %s", planRes.Decision.SelectedProvider)
	}

	reasonReq := *req
	reasonReq.Mode = "reasoning"
	reasonRes, err := pipeline.Execute(context.Background(), &reasonReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("reasoning Execute: %v", err)
	}
	if reasonRes.Decision.SelectedProvider != "no-tools" {
		t.Fatalf("reasoning: expected no-tools accepted (soft scoring), got %s", reasonRes.Decision.SelectedProvider)
	}
}

// TestAgenticVsPlanningCanDiffer verifies Agentic and Planning can select
// different winners when health vs capability weighting is the tradeoff:
// Planning's higher capability weight (45) favors the structured-capable but
// degraded provider; Agentic's higher health weight (55) favors the healthy
// provider without structured output.
func TestAgenticVsPlanningCanDiffer(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "cap-strong", supportsAll: true, reasoning: true, toolCalling: true, structured: true, latencyMs: 100, healthState: runtime.StateDegraded},
		&calibStubProvider{name: "health-strong", supportsAll: true, reasoning: true, toolCalling: true, structured: false, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "cap-strong", runtime.StateDegraded, 100)
	setHealth(t, store, "health-strong", runtime.StateHealthy, 100)

	base := execReq("", "plan and execute")
	base.Tools = []apitypes.Tool{{Function: apitypes.FunctionDef{Name: "t"}}}
	base.ResponseFormat = map[string]interface{}{"type": "json_object"}

	planReq := *base
	planReq.Mode = "planning"
	planRes, err := pipeline.Execute(context.Background(), &planReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("planning Execute: %v", err)
	}
	if planRes.Decision.SelectedProvider != "cap-strong" {
		t.Fatalf("planning: expected cap-strong (capability weight dominates), got %s", planRes.Decision.SelectedProvider)
	}

	agentReq := *base
	agentReq.Mode = "agentic"
	agentRes, err := pipeline.Execute(context.Background(), &agentReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("agentic Execute: %v", err)
	}
	if agentRes.Decision.SelectedProvider != "health-strong" {
		t.Fatalf("agentic: expected health-strong (health weight dominates), got %s", agentRes.Decision.SelectedProvider)
	}
	if planRes.Decision.SelectedProvider == agentRes.Decision.SelectedProvider {
		t.Fatal("planning and agentic should produce different winners in this scenario")
	}
}

// TestLongHorizonVsDefaultDiffer verifies Long Horizon rejects a candidate
// whose known MaxContext is below the request requirement while Default accepts it.
func TestLongHorizonVsDefaultDiffer(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "small-ctx", supportsAll: true, maxContext: 4096, latencyMs: 50, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "large-ctx", supportsAll: true, maxContext: 128000, latencyMs: 200, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "small-ctx", runtime.StateHealthy, 50)
	setHealth(t, store, "large-ctx", runtime.StateHealthy, 200)

	// ~14k input chars => ~3.5k input tokens + 4096 default output => ~8k required.
	longReq := execReq("", "x")
	longReq.Messages = []apitypes.Message{{Role: "user", Content: string(make([]byte, 14000))}}

	lhReq := *longReq
	lhReq.Mode = "long_horizon"
	lhRes, err := pipeline.Execute(context.Background(), &lhReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("long_horizon Execute: %v", err)
	}
	if lhRes.Decision.SelectedProvider != "large-ctx" {
		t.Fatalf("long_horizon: expected large-ctx (small-ctx hard rejected), got %s", lhRes.Decision.SelectedProvider)
	}

	defRes, err := pipeline.Execute(context.Background(), longReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("default Execute: %v", err)
	}
	if defRes.Decision.SelectedProvider != "small-ctx" {
		t.Fatalf("default: expected small-ctx (no context constraint), got %s", defRes.Decision.SelectedProvider)
	}
}

// TestLongHorizonContextThreshold verifies Phase 2's canonical case:
// a request requiring >32k tokens rejects the 32k candidate and accepts 128k.
func TestLongHorizonContextThreshold(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "ctx32k", supportsAll: true, maxContext: 32768, latencyMs: 50, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "ctx128k", supportsAll: true, maxContext: 128000, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "ctx32k", runtime.StateHealthy, 50)
	setHealth(t, store, "ctx128k", runtime.StateHealthy, 100)

	// 100k chars => ~25k input tokens + 4096 output => ~29k required (< 32k).
	// 130k chars => ~32.5k input tokens + 4096 output => ~36.6k required (> 32k).
	longReq := execReq("long_horizon", "x")
	longReq.Messages = []apitypes.Message{{Role: "user", Content: string(make([]byte, 130000))}}

	res, err := pipeline.Execute(context.Background(), longReq, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "ctx128k" {
		t.Fatalf("expected ctx128k (32k rejected for >32k requirement), got %s", res.Decision.SelectedProvider)
	}
	for _, cs := range res.Decision.CandidateScores {
		if cs.Provider == "ctx32k" && !cs.Rejected {
			t.Fatal("ctx32k must be hard-rejected for a >32k requirement")
		}
	}
}

// TestAgenticTelemetryPreferred verifies Agentic prefers good-telemetry
// candidates when telemetry has sufficient observations (Phase 2 canonical case).
func TestAgenticTelemetryPreferred(t *testing.T) {
	pipeline, store, _ := setupCalibPipeline(t,
		&calibStubProvider{name: "proven", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&calibStubProvider{name: "flaky", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	setHealth(t, store, "proven", runtime.StateHealthy, 100)
	setHealth(t, store, "flaky", runtime.StateHealthy, 100)

	_ = store.Update("proven", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	_ = store.Update("flaky", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})

	req := execReq("agentic", "build this")
	res, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Decision.SelectedProvider != "proven" {
		t.Fatalf("expected proven (good execution telemetry), got %s", res.Decision.SelectedProvider)
	}
}
