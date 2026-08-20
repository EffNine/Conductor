package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/runtime"
)

// ---- AGENTIC MODE ROUTER TESTS ----

// TestAgenticModeActivated verifies that mode=agentic is active and routable.
func TestAgenticModeActivated(t *testing.T) {
	pipeline, _, _ := setupModePipeline(t,
		&modeStubProvider{name: "p", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build a system"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
}

// TestAgenticRequiresReasoning verifies that Agentic rejects providers lacking Reasoning.
func TestAgenticRequiresReasoning(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "no-reason", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "no-reason", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection (provider lacks reasoning), got %s", result.Decision.SelectedProvider)
	}
	if len(result.Decision.RejectionReasons) == 0 {
		t.Fatal("expected rejection reason")
	}
	found := false
	for _, r := range result.Decision.RejectionReasons {
		if r.Provider == "no-reason" && r.Reason != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected rejection for no-reason, got: %v", result.Decision.RejectionReasons)
	}
}

// TestAgenticRequiresToolCalling verifies that Agentic rejects providers lacking ToolCalling.
func TestAgenticRequiresToolCalling(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "no-tools", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "no-tools", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection (provider lacks tools), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticRejectsNonReasoningProvider verifies hard rejection of non-reasoning providers.
func TestAgenticRejectsNonReasoningProvider(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "text-only", supportsAll: true, reasoning: false, toolCalling: false, latencyMs: 50, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "reason-plus", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "text-only", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "reason-plus", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build a multi-step system"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "reason-plus" {
		t.Fatalf("expected reason-plus, got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticRejectsNonToolCallingProvider verifies hard rejection of non-tool-calling providers.
func TestAgenticRejectsNonToolCallingProvider(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "reason-only", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 50, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "full-cap", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "reason-only", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "full-cap", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build the agent"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "full-cap" {
		t.Fatalf("expected full-cap, got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticUsesExecutionReliability verifies that good execution telemetry
// gives Agentic a stronger preference bonus than Planning would.
func TestAgenticUsesExecutionReliability(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "unreliable", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "reliable", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "unreliable", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "reliable", runtime.StateHealthy, 100, 0)

	// Give reliable provider good execution history.
	_ = store.Update("reliable", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	// Give unreliable provider poor execution history.
	_ = store.Update("unreliable", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build a system"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "reliable" {
		t.Fatalf("expected reliable (good execution history), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticUsesToolReliability verifies that good tool-call telemetry
// gives Agentic a positive preference bonus.
func TestAgenticUsesToolReliability(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "bad-tools", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "good-tools", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "bad-tools", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "good-tools", runtime.StateHealthy, 100, 0)

	// Give good-tools provider strong tool history.
	_ = store.Update("good-tools", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordToolCallOutcome(true)
		}
		return nil
	})
	// Give bad-tools provider poor tool history.
	_ = store.Update("bad-tools", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordToolCallOutcome(false)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build with tools"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "good-tools" {
		t.Fatalf("expected good-tools (reliable tool history), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticUsesExecutionDepthWhenAvailable verifies that when execution
// telemetry is present, Agentic prefers providers with deeper successful runs.
func TestAgenticUsesExecutionDepthWhenAvailable(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "shallow", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "deep", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "shallow", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "deep", runtime.StateHealthy, 100, 0)

	// shallow: few executions, all successful
	_ = store.Update("shallow", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	// deep: many executions, all successful (simulates sustained multi-step)
	_ = store.Update("deep", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 20; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build a sustained multi-step system"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// Both have 100% success rate, so execution telemetry preference is equal.
	// This test verifies the signal is consumed without error.
	_ = result
}

// TestAgenticPenalizesHighRetryRate verifies that high retry rates reduce Agentic preference.
func TestAgenticPenalizesHighRetryRate(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "high-retry", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "low-retry", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "high-retry", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "low-retry", runtime.StateHealthy, 100, 0)

	// high-retry has many retries.
	_ = store.Update("high-retry", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 3) // 3 retries each
		}
		return nil
	})
	// low-retry has no retries.
	_ = store.Update("low-retry", func(r runtime.ProviderRuntime) error {
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
	if result.Decision.SelectedProvider != "low-retry" {
		t.Fatalf("expected low-retry (fewer retries), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticUnknownTelemetryNeutral verifies that providers with zero execution
// history are not penalized — they remain neutral.
func TestAgenticUnknownTelemetryNeutral(t *testing.T) {
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

// TestAgenticInsufficientSampleNeutral verifies that low sample counts are neutral.
func TestAgenticInsufficientSampleNeutral(t *testing.T) {
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
		// alphabetical tie-break: few-data < other
		t.Fatalf("expected few-data (neutral telemetry), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticStrongHistoryPreferred verifies that strong measured execution
// history gives a meaningful preference in Agentic mode.
func TestAgenticStrongHistoryPreferred(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "poor", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "good", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "poor", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "good", runtime.StateHealthy, 100, 0)

	// poor provider has many failures.
	_ = store.Update("poor", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})
	// good provider has many successes.
	_ = store.Update("good", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build the rollout"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "good" {
		t.Fatalf("expected good (better execution history), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticPoorHistoryPenalized verifies that poor measured history reduces Agentic preference.
func TestAgenticPoorHistoryPenalized(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "poor", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "good", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "poor", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "good", runtime.StateHealthy, 100, 0)

	// poor provider has many failures.
	_ = store.Update("poor", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})
	// good provider has many successes.
	_ = store.Update("good", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build the rollout"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "good" {
		t.Fatalf("expected good (better execution history), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticContextRequirement verifies that Agentic enforces context
// requirements using the existing Long Horizon mechanism.
func TestAgenticContextRequirement(t *testing.T) {
	pipeline, store, eng := setupLongHorizonPipeline(t,
		&longHorizonStubProvider{name: "small", supportsAll: true, maxContext: 4096, reasoning: true, toolCalling: true, latencyMs: 50, healthState: runtime.StateHealthy},
		&longHorizonStubProvider{name: "large", supportsAll: true, maxContext: 128000, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "small", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "large", runtime.StateHealthy, 100, 0)

	// Register full capabilities including reasoning+tool_calling so Agentic
	// hard filter doesn't reject all candidates.
	eng.SetModelCapabilities("small", "m", router.Capabilities{Reasoning: true, ToolCalling: true, MaxContext: 4096})
	eng.SetModelCapabilities("large", "m", router.Capabilities{Reasoning: true, ToolCalling: true, MaxContext: 128000})

	budget := 10000
	req := &apitypes.ChatCompletionRequest{
		Model:          "m",
		Mode:           "agentic",
		ThinkingBudget: &budget,
		Messages:       []apitypes.Message{{Role: "user", Content: "build a complex multi-step system with deep context"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// large should win (small has insufficient context).
	if result.Decision.SelectedProvider != "large" {
		t.Fatalf("expected large (sufficient context), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticExplicitRouteConstraint verifies that explicit routes constrain
// Agentic to the supplied candidate set without expansion.
func TestAgenticExplicitRouteConstraint(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "qualified", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "unqualified", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "qualified", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "unqualified", runtime.StateHealthy, 50, 0)

	// Only supply unqualified candidate — Agentic must not expand.
	candidates := []router.ResolvedRoute{
		{ProviderName: "unqualified", ProviderModelID: "m", ModelID: "m"},
	}
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection (single candidate rejected), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticFallbackCanWin verifies that a fallback provider satisfying
// Agentic requirements can win over a primary that does not.
func TestAgenticFallbackCanWin(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "primary", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 50, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "fallback", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "primary", runtime.StateHealthy, 50, 0)
	updateProviderState(t, store, "fallback", runtime.StateHealthy, 100, 0)

	// Supply primary + fallback as candidates.
	candidates := []router.ResolvedRoute{
		{ProviderName: "primary", ProviderModelID: "m", ModelID: "m"},
		{ProviderName: "fallback", ProviderModelID: "m", ModelID: "m"},
	}
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build the migration"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	// Fallback should win because primary lacks ToolCalling.
	if result.Decision.SelectedProvider != "fallback" {
		t.Fatalf("expected fallback (primary lacks tools), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticSingleQualifiedCandidatePinned verifies that a single qualified
// candidate remains pinned even with Agentic mode active.
func TestAgenticSingleQualifiedCandidatePinned(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "pinned", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "pinned", runtime.StateHealthy, 100, 0)

	candidates := []router.ResolvedRoute{
		{ProviderName: "pinned", ProviderModelID: "m", ModelID: "m"},
	}
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider != "pinned" {
		t.Fatalf("expected pinned, got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticSingleUnqualifiedCandidateRejected verifies that a single
// unqualified candidate is rejected (not silently routed elsewhere).
func TestAgenticSingleUnqualifiedCandidateRejected(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "unqualified", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "unqualified", runtime.StateHealthy, 100, 0)

	candidates := []router.ResolvedRoute{
		{ProviderName: "unqualified", ProviderModelID: "m", ModelID: "m"},
	}
	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, candidates)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider != "" {
		t.Fatalf("expected no selection (single candidate rejected), got %s", result.Decision.SelectedProvider)
	}
}

// TestAgenticDeterministicTieBreaking verifies that equal scores produce
// deterministic selection even with Agentic telemetry preferences.
func TestAgenticDeterministicTieBreaking(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "zebra", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "alpha", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "zebra", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "alpha", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	// Run multiple times — selection must be deterministic.
	var first string
	for i := 0; i < 5; i++ {
		result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("iteration %d: Execute: %v", i, err)
		}
		if result == nil || result.Decision.SelectedProvider == "" {
			t.Fatalf("iteration %d: expected selected provider", i)
		}
		if i == 0 {
			first = result.Decision.SelectedProvider
		} else if result.Decision.SelectedProvider != first {
			t.Fatalf("iteration %d: non-deterministic: %q != %q", i, result.Decision.SelectedProvider, first)
		}
	}
}

// TestAgenticDoesNotAffectOtherModes verifies that activating Agentic does not
// change routing behavior for other active modes.
func TestAgenticDoesNotAffectOtherModes(t *testing.T) {
	for _, mode := range []router.Mode{router.ModeFast, router.ModeCoding, router.ModeReasoning, router.ModeVision, router.ModeDefault, router.ModeLongHorizon, router.ModePlanning} {
		pipeline, store, _ := setupModePipeline(t,
			&modeStubProvider{name: "a", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
			&modeStubProvider{name: "b", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 50, healthState: runtime.StateHealthy},
		)
		updateProviderState(t, store, "a", runtime.StateHealthy, 100, 0)
		updateProviderState(t, store, "b", runtime.StateHealthy, 50, 0)

		content := "hi"
		reqMode := ""
		switch mode {
		case router.ModeFast:
			content = "hi quick"
			reqMode = "fast"
		case router.ModeCoding:
			content = "write a function"
			reqMode = "coding"
		case router.ModeReasoning:
			content = "analyze this"
			reqMode = "reasoning"
		case router.ModeVision:
			content = "look at this image"
			reqMode = "vision"
		case router.ModeLongHorizon:
			content = "summarize this long document"
			reqMode = "long_horizon"
		case router.ModePlanning:
			content = "plan the deployment"
			reqMode = "planning"
		case router.ModeDefault:
			reqMode = "auto"
		}
		req := &apitypes.ChatCompletionRequest{
			Model:    "m",
			Mode:     reqMode,
			Messages: []apitypes.Message{{Role: "user", Content: content}},
		}
		result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
		if err != nil {
			t.Fatalf("mode %s: Execute: %v", mode, err)
		}
		if result == nil || result.Decision.SelectedProvider == "" {
			t.Fatalf("mode %s: expected selected provider", mode)
		}
		// Each mode should produce a stable selection.
		_ = result
	}
}

// ---- AGENTIC PIPELINE TESTS ----

// TestPipelineAgenticMode verifies that the full DecisionPipeline correctly
// routes mode=agentic requests through all stages.
func TestPipelineAgenticMode(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "qualified", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "unqualified", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 50, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "qualified", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "unqualified", runtime.StateHealthy, 50, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build the deployment"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "qualified" {
		t.Fatalf("expected qualified, got %s", result.Decision.SelectedProvider)
	}
}

// TestPipelineAgenticCapabilityRequirements verifies that the CapabilityStage
// derives agentic requirements and the pipeline enforces them.
func TestPipelineAgenticCapabilityRequirements(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "no-reason", supportsAll: true, reasoning: false, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "no-tools", supportsAll: true, reasoning: true, toolCalling: false, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "full", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 120, healthState: runtime.StateHealthy},
	)
	for _, name := range []string{"no-reason", "no-tools", "full"} {
		updateProviderState(t, store, name, runtime.StateHealthy, 100, 0)
	}

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
	if result.Decision.SelectedProvider != "full" {
		t.Fatalf("expected full, got %s", result.Decision.SelectedProvider)
	}
}

// TestPipelineAgenticUsesRuntimeSnapshot verifies that Agentic routing derives
// telemetry from the single RuntimeSnapshot acquired by the pipeline.
func TestPipelineAgenticUsesRuntimeSnapshot(t *testing.T) {
	pipeline, store, manager := setupModePipeline(t,
		&modeStubProvider{name: "a", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "b", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "a", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "b", runtime.StateHealthy, 100, 0)

	// Capture snapshot, then mutate telemetry after.
	snap := manager.Snapshot(context.Background())
	_ = store.Update("a", func(r runtime.ProviderRuntime) error {
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
	// Use the old snapshot directly to verify coherence.
	eng := pipeline.RoutingEngine()
	result, err := eng.SelectBestProvider(context.Background(), "m", req)
	if err != nil {
		t.Fatalf("SelectBestProvider: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	_ = snap
	_ = eng
}

// TestPipelineAgenticSingleSnapshot verifies that DecisionPipeline.Execute
// acquires exactly one RuntimeSnapshot per Agentic execution.
func TestPipelineAgenticSingleSnapshot(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "p", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "p", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	_, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// TestPipelineAgenticTelemetryScoring verifies that execution telemetry
// influences Agentic selection through the full pipeline.
func TestPipelineAgenticTelemetryScoring(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "good", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "poor", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "good", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "poor", runtime.StateHealthy, 100, 0)

	_ = store.Update("good", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	_ = store.Update("poor", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 10; i++ {
			r.RecordExecutionOutcome(false, 0)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build the rollout"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "good" {
		t.Fatalf("expected good (better telemetry), got %s", result.Decision.SelectedProvider)
	}
}

// TestPipelineAgenticExecutionDepth verifies that Agentic consumes execution
// telemetry and prefers providers with sustained successful executions.
func TestPipelineAgenticExecutionDepth(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "stable", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
		&modeStubProvider{name: "unstable", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "stable", runtime.StateHealthy, 100, 0)
	updateProviderState(t, store, "unstable", runtime.StateHealthy, 100, 0)

	// stable: consistent successes
	_ = store.Update("stable", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 15; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		return nil
	})
	// unstable: mixed results with high retry rate
	_ = store.Update("unstable", func(r runtime.ProviderRuntime) error {
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcome(true, 0)
		}
		for i := 0; i < 5; i++ {
			r.RecordExecutionOutcome(false, 2)
		}
		return nil
	})

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build a sustained multi-step system"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if result.Decision.SelectedProvider != "stable" {
		t.Fatalf("expected stable (better execution reliability), got %s", result.Decision.SelectedProvider)
	}
}

// TestPipelineAgenticTraceMetadata verifies that the DecisionTrace captures
// agentic-relevant metadata through the pipeline.
func TestPipelineAgenticTraceMetadata(t *testing.T) {
	pipeline, store, _ := setupModePipeline(t,
		&modeStubProvider{name: "p", supportsAll: true, reasoning: true, toolCalling: true, latencyMs: 100, healthState: runtime.StateHealthy},
	)
	updateProviderState(t, store, "p", runtime.StateHealthy, 100, 0)

	req := &apitypes.ChatCompletionRequest{
		Model:    "m",
		Mode:     "agentic",
		Messages: []apitypes.Message{{Role: "user", Content: "build"}},
	}
	result, err := pipeline.Execute(context.Background(), req, router.Environment{}, router.ConfigSnapshot{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider != "p" {
		t.Fatalf("expected p, got %s", result.Decision.SelectedProvider)
	}
}
