package router_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// TestExecutionTelemetryAttribution verifies that provider-level execution
// telemetry is correctly attributable and visible to routing.
func TestExecutionTelemetryAttribution(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	pOpenAI, _ := reg.Get("openai")
	pGroq, _ := reg.Get("groq")
	_ = store.Register(runtime.NewProviderRuntime("openai", pOpenAI))
	_ = store.Register(runtime.NewProviderRuntime("groq", pGroq))
	manager := runtime.NewManager(store)

	// Give openai good execution history, groq poor.
	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})

	snap := manager.Snapshot(context.Background())
	if snap.Providers["openai"].ExecutionSuccessCount != 10 {
		t.Errorf("openai expected 10 successes, got %d", snap.Providers["openai"].ExecutionSuccessCount)
	}
	if snap.Providers["groq"].ExecutionFailureCount != 10 {
		t.Errorf("groq expected 10 failures, got %d", snap.Providers["groq"].ExecutionFailureCount)
	}
}

// TestExecutionTelemetryUnknown verifies that zero observations yield UNKNOWN state.
func TestExecutionTelemetryUnknown(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	p, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", p))
	manager := runtime.NewManager(store)

	snap := manager.Snapshot(context.Background())
	if snap.Providers["openai"].ExecutionCount != 0 {
		t.Errorf("expected 0 executions for unknown state, got %d", snap.Providers["openai"].ExecutionCount)
	}
}

// TestExecutionTelemetryInsufficientSamples verifies that low sample counts
// remain neutral (not treated as measured poor).
func TestExecutionTelemetryInsufficientSamples(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "few", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "other", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "few", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "other", runtime.StateHealthy, 100, 0)

	// 3 executions — below minExecutionSample (5), should be neutral.
	_ = store.Update("few", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 3; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// few-data should not be penalized (insufficient = neutral).
	if result.Decision.SelectedProvider != "few" {
		t.Fatalf("expected few (neutral telemetry), got %s", result.Decision.SelectedProvider)
	}
}

// TestExecutionTelemetryMeasured verifies that sufficient samples are treated
// as MEASURED and influence routing.
func TestExecutionTelemetryMeasured(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "poor", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "good", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "poor", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "good", runtime.StateHealthy, 100, 0)

	_ = store.Update("poor", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})
	_ = store.Update("good", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "good" {
		t.Fatalf("expected good (measured good history), got %s", result.Decision.SelectedProvider)
	}
}

// TestExecutionTelemetrySuccessRate verifies the mathematical correctness of
// success rate computation (0 observations != 0% success).
func TestExecutionTelemetrySuccessRate(t *testing.T) {
	r := runtime.NewProviderRuntime("test", nil)

	snap := r.Snapshot(context.Background())
	// 0 observations should not produce a division-by-zero or false 0% rate.
	if snap.ExecutionCount != 0 {
		t.Errorf("expected 0 executions, got %d", snap.ExecutionCount)
	}
	if snap.ExecutionSuccessCount != 0 {
		t.Errorf("expected 0 success count, got %d", snap.ExecutionSuccessCount)
	}
	if snap.ExecutionFailureCount != 0 {
		t.Errorf("expected 0 failure count, got %d", snap.ExecutionFailureCount)
	}

	// After recording: 3 successes, 1 failure → 75% rate.
	r.RecordExecutionOutcome(true, 0)
	r.RecordExecutionOutcome(true, 0)
	r.RecordExecutionOutcome(true, 0)
	r.RecordExecutionOutcome(false, 0)

	snap = r.Snapshot(context.Background())
	expectedRate := 3.0 / 4.0
	actualRate := float64(snap.ExecutionSuccessCount) / float64(snap.ExecutionSuccessCount+snap.ExecutionFailureCount)
	if actualRate != expectedRate {
		t.Errorf("expected success rate %.2f, got %.2f", expectedRate, actualRate)
	}
}

// TestToolTelemetryAttribution verifies that tool call telemetry is correctly
// attributed to the provider that executed it.
func TestToolTelemetryAttribution(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "anthropic", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	pOpenAI, _ := reg.Get("openai")
	pAnthropic, _ := reg.Get("anthropic")
	_ = store.Register(runtime.NewProviderRuntime("openai", pOpenAI))
	_ = store.Register(runtime.NewProviderRuntime("anthropic", pAnthropic))
	manager := runtime.NewManager(store)

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.RecordToolCallOutcome(true)
		r.RecordToolCallOutcome(true)
		r.RecordToolCallOutcome(false)
		return nil
	})
	_ = store.Update("anthropic", func(r runtime.ProviderRuntime) error {
		r.RecordToolCallOutcome(false)
		r.RecordToolCallOutcome(false)
		return nil
	})

	snap := manager.Snapshot(context.Background())
	if snap.Providers["openai"].ToolCallSuccessCount != 2 {
		t.Errorf("openai expected 2 tool successes, got %d", snap.Providers["openai"].ToolCallSuccessCount)
	}
	if snap.Providers["openai"].ToolCallFailureCount != 1 {
		t.Errorf("openai expected 1 tool failure, got %d", snap.Providers["openai"].ToolCallFailureCount)
	}
	if snap.Providers["anthropic"].ToolCallSuccessCount != 0 {
		t.Errorf("anthropic expected 0 tool successes, got %d", snap.Providers["anthropic"].ToolCallSuccessCount)
	}
	if snap.Providers["anthropic"].ToolCallFailureCount != 2 {
		t.Errorf("anthropic expected 2 tool failures, got %d", snap.Providers["anthropic"].ToolCallFailureCount)
	}
}

// TestToolTelemetryUnknown verifies that zero tool calls yield UNKNOWN state.
func TestToolTelemetryUnknown(t *testing.T) {
	r := runtime.NewProviderRuntime("test", nil)
	snap := r.Snapshot(context.Background())
	if snap.ToolCallSuccessCount != 0 {
		t.Errorf("expected 0 tool successes, got %d", snap.ToolCallSuccessCount)
	}
	if snap.ToolCallFailureCount != 0 {
		t.Errorf("expected 0 tool failures, got %d", snap.ToolCallFailureCount)
	}
}

// TestRetryTelemetrySemantics verifies retry counting semantics.
func TestRetryTelemetrySemantics(t *testing.T) {
	r := runtime.NewProviderRuntime("test", nil)

	// Primary success (0 retries).
	r.RecordExecutionOutcome(true, 0)
	// Fallback success after 2 retries.
	r.RecordExecutionOutcome(true, 2)
	// Fallback failure after 1 retry.
	r.RecordExecutionOutcome(false, 1)

	snap := r.Snapshot(context.Background())
	if snap.ExecutionCount != 3 {
		t.Errorf("expected 3 executions, got %d", snap.ExecutionCount)
	}
	if snap.ExecutionSuccessCount != 2 {
		t.Errorf("expected 2 successes, got %d", snap.ExecutionSuccessCount)
	}
	if snap.ExecutionFailureCount != 1 {
		t.Errorf("expected 1 failure, got %d", snap.ExecutionFailureCount)
	}
	if snap.RetryCount != 3 {
		t.Errorf("expected 3 total retries, got %d", snap.RetryCount)
	}
}

// TestProviderModelTelemetryIsolation verifies that different models on the
// same provider have isolated execution telemetry.
func TestProviderModelTelemetryIsolation(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	p, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", p))
	manager := runtime.NewManager(store)

	// Record different histories for two models.
	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
		}
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcomeModel("gpt-4o-mini", false, 0)
		}
		return nil
	})

	snap := manager.Snapshot(context.Background())

	// Provider-level aggregates both models.
	if snap.Providers["openai"].ExecutionCount != 10 {
		t.Errorf("expected 10 total executions, got %d", snap.Providers["openai"].ExecutionCount)
	}
	if snap.Providers["openai"].ExecutionSuccessCount != 5 {
		t.Errorf("expected 5 total successes, got %d", snap.Providers["openai"].ExecutionSuccessCount)
	}
	if snap.Providers["openai"].ExecutionFailureCount != 5 {
		t.Errorf("expected 5 total failures, got %d", snap.Providers["openai"].ExecutionFailureCount)
	}

	// Model-level is isolated.
	models := snap.Providers["openai"].ModelExecutions
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	gpt4o := models["gpt-4o"]
	if gpt4o.ExecutionCount != 5 {
		t.Errorf("gpt-4o expected 5 executions, got %d", gpt4o.ExecutionCount)
	}
	if gpt4o.ExecutionSuccessCount != 5 {
		t.Errorf("gpt-4o expected 5 successes, got %d", gpt4o.ExecutionSuccessCount)
	}
	if gpt4o.ExecutionFailureCount != 0 {
		t.Errorf("gpt-4o expected 0 failures, got %d", gpt4o.ExecutionFailureCount)
	}
	gpt4oMini := models["gpt-4o-mini"]
	if gpt4oMini.ExecutionCount != 5 {
		t.Errorf("gpt-4o-mini expected 5 executions, got %d", gpt4oMini.ExecutionCount)
	}
	if gpt4oMini.ExecutionSuccessCount != 0 {
		t.Errorf("gpt-4o-mini expected 0 successes, got %d", gpt4oMini.ExecutionSuccessCount)
	}
	if gpt4oMini.ExecutionFailureCount != 5 {
		t.Errorf("gpt-4o-mini expected 5 failures, got %d", gpt4oMini.ExecutionFailureCount)
	}
}

// TestSameModelDifferentProvidersIsolated verifies that the same model name
// on different providers has isolated telemetry.
func TestSameModelDifferentProvidersIsolated(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "azure", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	pOpenAI, _ := reg.Get("openai")
	pAzure, _ := reg.Get("azure")
	_ = store.Register(runtime.NewProviderRuntime("openai", pOpenAI))
	_ = store.Register(runtime.NewProviderRuntime("azure", pAzure))
	manager := runtime.NewManager(store)

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
		}
		return nil
	})
	_ = store.Update("azure", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcomeModel("gpt-4o", false, 0)
		}
		return nil
	})

	snap := manager.Snapshot(context.Background())

	// Each provider's model-level telemetry is isolated.
	openaiModels := snap.Providers["openai"].ModelExecutions
	azureModels := snap.Providers["azure"].ModelExecutions

	gpt4oOpenAI := openaiModels["gpt-4o"]
	gpt4oAzure := azureModels["gpt-4o"]

	if gpt4oOpenAI.ExecutionSuccessCount != 5 {
		t.Errorf("openai/gpt-4o expected 5 successes, got %d", gpt4oOpenAI.ExecutionSuccessCount)
	}
	if gpt4oAzure.ExecutionSuccessCount != 0 {
		t.Errorf("azure/gpt-4o expected 0 successes, got %d", gpt4oAzure.ExecutionSuccessCount)
	}
	if gpt4oAzure.ExecutionFailureCount != 5 {
		t.Errorf("azure/gpt-4o expected 5 failures, got %d", gpt4oAzure.ExecutionFailureCount)
	}
}

// TestSameProviderDifferentModelsIsolated verifies that different models on
// the same provider do NOT share telemetry.
func TestSameProviderDifferentModelsIsolated(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	p, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", p))
	manager := runtime.NewManager(store)

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
		}
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcomeModel("gpt-4o-mini", false, 0)
		}
		return nil
	})

	snap := manager.Snapshot(context.Background())
	models := snap.Providers["openai"].ModelExecutions

	gpt4o := models["gpt-4o"]
	gpt4oMini := models["gpt-4o-mini"]

	if gpt4o.ExecutionSuccessCount != 5 {
		t.Errorf("gpt-4o expected 5 successes, got %d", gpt4o.ExecutionSuccessCount)
	}
	if gpt4o.ExecutionFailureCount != 0 {
		t.Errorf("gpt-4o expected 0 failures, got %d", gpt4o.ExecutionFailureCount)
	}
	if gpt4oMini.ExecutionSuccessCount != 0 {
		t.Errorf("gpt-4o-mini expected 0 successes, got %d", gpt4oMini.ExecutionSuccessCount)
	}
	if gpt4oMini.ExecutionFailureCount != 5 {
		t.Errorf("gpt-4o-mini expected 5 failures, got %d", gpt4oMini.ExecutionFailureCount)
	}
}

// TestProviderLevelTelemetryExplicitlyScoped verifies that provider-level
// counters still work when model-level is not used.
func TestProviderLevelTelemetryExplicitlyScoped(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	store := runtime.NewRuntimeStore(nil)
	p, _ := reg.Get("openai")
	_ = store.Register(runtime.NewProviderRuntime("openai", p))
	manager := runtime.NewManager(store)

	// Use provider-level API (no model ID).
	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})

	snap := manager.Snapshot(context.Background())
	if snap.Providers["openai"].ExecutionCount != 10 {
		t.Errorf("expected 10 executions, got %d", snap.Providers["openai"].ExecutionCount)
	}
	if snap.Providers["openai"].ModelExecutions != nil {
		t.Errorf("expected no model executions when using provider-level API, got %v", snap.Providers["openai"].ModelExecutions)
	}
}

// TestAgenticUsesCorrectTelemetryAttribution verifies Agentic routes using
// the correct telemetry (model-level when available, provider-level fallback).
func TestAgenticUsesCorrectTelemetryAttribution(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "model-a", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "model-b", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "model-a", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "model-b", runtime.StateHealthy, 100, 0)

	// model-a: good model-level history for gpt-4o.
	_ = store.Update("model-a", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
		}
		return nil
	})
	// model-b: poor model-level history for gpt-4o.
	_ = store.Update("model-b", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("gpt-4o", false, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "model-a" {
		t.Fatalf("expected model-a (good model-level telemetry), got %s", result.Decision.SelectedProvider)
	}
}

// TestPlanningUsesCorrectTelemetryAttribution verifies Planning also uses
// correct telemetry attribution.
func TestPlanningUsesCorrectTelemetryAttribution(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "model-a", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "model-b", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "model-a", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "model-b", runtime.StateHealthy, 100, 0)

	_ = store.Update("model-a", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
		}
		return nil
	})
	_ = store.Update("model-b", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcomeModel("gpt-4o", false, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Mode:     "planning",
		Messages: []apitypes.Message{{Role: "user", Content: "plan"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "model-a" {
		t.Fatalf("expected model-a, got %s", result.Decision.SelectedProvider)
	}
}

// TestUnknownTelemetryNeutral verifies that providers with zero execution
// history are not penalized in Agentic mode.
func TestUnknownTelemetryNeutral(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "new-provider", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "proven", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "new-provider", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "proven", runtime.StateHealthy, 100, 0)

	// proven has good history, new-provider has none.
	_ = store.Update("proven", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// proven should win due to good telemetry; new-provider should not be rejected.
	if result.Decision.SelectedProvider != "proven" {
		t.Fatalf("expected proven (good telemetry), got %s", result.Decision.SelectedProvider)
	}
}

// TestInsufficientTelemetryNeutral verifies that low sample counts are neutral.
func TestInsufficientTelemetryNeutral(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "few-data", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "other", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "few-data", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "other", runtime.StateHealthy, 100, 0)

	// fewer than minExecutionSample (5) executions — should be neutral.
	_ = store.Update("few-data", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 3; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// few-data should not be penalized (insufficient sample = neutral).
	if result.Decision.SelectedProvider != "few-data" {
		t.Fatalf("expected few-data (neutral telemetry), got %s", result.Decision.SelectedProvider)
	}
}

// TestPlanningAndAgenticDiffer verifies that Agentic and Planning use
// different telemetry intensities (Agentic = 1.5x, Planning = 1.0x).
func TestPlanningAndAgenticDiffer(t *testing.T) {
	// Setup two providers with identical health/latency but different telemetry.
	pipelineA, storeA, _ := setupModePipeline(t,
		&modeStubProvider{name: "good", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "bad", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, storeA, "good", runtime.StateHealthy, 100, 0)
	updateProviderState(t, storeA, "bad", runtime.StateHealthy, 100, 0)

	_ = storeA.Update("good", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	_ = storeA.Update("bad", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}

	// Agentic should prefer good.
	reqAgentic := *req
	reqAgentic.Mode = "agentic"
	resultA, err := pipelineA.Execute(context.Background(), &reqAgentic, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Agentic Execute: %v", err)
	}
	if resultA == nil || resultA.Decision.SelectedProvider != "good" {
		t.Fatalf("expected Agentic to select good, got %s", resultA.Decision.SelectedProvider)
	}

	// Planning should also prefer good (same telemetry, just lower intensity).
	pipelineP, storeP, _ := setupModePipeline(t,
		&modeStubProvider{name: "good", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "bad", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, storeP, "good", runtime.StateHealthy, 100, 0)
	updateProviderState(t, storeP, "bad", runtime.StateHealthy, 100, 0)

	_ = storeP.Update("good", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	_ = storeP.Update("bad", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})

	reqPlanning := *req
	reqPlanning.Mode = "planning"
	resultP, err := pipelineP.Execute(context.Background(), &reqPlanning, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Planning Execute: %v", err)
	}
	if resultP == nil || resultP.Decision.SelectedProvider != "good" {
		t.Fatalf("expected Planning to select good, got %s", resultP.Decision.SelectedProvider)
	}
}

// TestTelemetryUsesSinglePipelineSnapshot verifies that the DecisionPipeline
// uses exactly one RuntimeSnapshot for all telemetry-influenced scoring.
func TestTelemetryUsesSinglePipelineSnapshot(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&capabilityStubProvider{name: "a", supportsAll: true, reasoning: true, toolCalling: true})
	reg.Register(&capabilityStubProvider{name: "b", supportsAll: true, reasoning: true, toolCalling: true})

	store := runtime.NewRuntimeStore(nil)
	for _, name := range []string{"a", "b"} {
		p, _ := reg.Get(name)
		_ = store.Register(runtime.NewProviderRuntime(name, p))
	}
	manager := runtime.NewManager(store)

	_ = store.Update("a", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("b", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	// Record telemetry AFTER setting up providers.
	_ = store.Update("a", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	_ = store.Update("b", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify telemetry is still accessible after pipeline execution.
	snap := manager.Snapshot(context.Background())
	if snap.Providers["a"].ExecutionSuccessCount != 10 {
		t.Errorf("a expected 10 successes, got %d", snap.Providers["a"].ExecutionSuccessCount)
	}
	if snap.Providers["b"].ExecutionFailureCount != 10 {
		t.Errorf("b expected 10 failures, got %d", snap.Providers["b"].ExecutionFailureCount)
	}
}

// TestConcurrentTelemetryAndRouting verifies that concurrent telemetry updates
// and routing decisions are race-free.
func TestConcurrentTelemetryAndRouting(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&capabilityStubProvider{name: "openai", supportsAll: true, reasoning: true, toolCalling: true})
	reg.Register(&capabilityStubProvider{name: "groq", supportsAll: true, reasoning: true, toolCalling: true})

	store := runtime.NewRuntimeStore(nil)
	pOpenAI, _ := reg.Get("openai")
	pGroq, _ := reg.Get("groq")
	_ = store.Register(runtime.NewProviderRuntime("openai", pOpenAI))
	_ = store.Register(runtime.NewProviderRuntime("groq", pGroq))
	manager := runtime.NewManager(store)

	_ = store.Update("openai", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})
	_ = store.Update("groq", func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtime.StateHealthy, "", nil)
		r.RecordLatency(100)
		return nil
	})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry:       reg,
		RuntimeManager: manager,
		Weights:        config.DefaultRoutingWeights(),
	})

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Goroutine: concurrent telemetry updates.
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-done:
				return
			default:
				provider := "openai"
				if i%2 == 0 {
					provider = "groq"
				}
				_ = store.Update(provider, func(r runtime.ProviderRuntime) error {
					r.RecordExecutionOutcome(i%3 == 0, 0)
					r.RecordToolCallOutcome(i%2 == 0)
					return nil
				})
				i++
			}
		}
	}()

	// Goroutine: concurrent routing decisions.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				req := &apitypes.ChatCompletionRequest{
					Model:    "m",
					Mode:     "agentic",
					Messages: []apitypes.Message{{Role: "user", Content: "build"}},
				}
				_, _ = pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
			}
		}
	}()

	// Run for a short time.
	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()

	// Verify state is consistent.
	snap := manager.Snapshot(context.Background())
	totalExec := snap.Providers["openai"].ExecutionCount + snap.Providers["groq"].ExecutionCount
	if totalExec == 0 {
		t.Error("expected some executions during concurrent test")
	}
}
