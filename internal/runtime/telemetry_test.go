package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/EffNine/conductor/internal/eventbus"
)

func TestExecutionTelemetryRecordSuccess(t *testing.T) {
	r := NewProviderRuntime("test-provider", nil)
	r.RecordExecutionOutcome(true, 0)
	r.RecordExecutionOutcome(true, 0)
	r.RecordExecutionOutcome(true, 1)

	snap := r.Snapshot(context.Background())
	if snap.ExecutionSuccessCount != 3 {
		t.Errorf("expected 3 execution successes, got %d", snap.ExecutionSuccessCount)
	}
	if snap.ExecutionFailureCount != 0 {
		t.Errorf("expected 0 execution failures, got %d", snap.ExecutionFailureCount)
	}
	if snap.ExecutionCount != 3 {
		t.Errorf("expected 3 total executions, got %d", snap.ExecutionCount)
	}
	if snap.RetryCount != 1 {
		t.Errorf("expected 1 retry, got %d", snap.RetryCount)
	}
}

func TestExecutionTelemetryRecordFailure(t *testing.T) {
	r := NewProviderRuntime("test-provider", nil)
	r.RecordExecutionOutcome(false, 0)
	r.RecordExecutionOutcome(false, 2)

	snap := r.Snapshot(context.Background())
	if snap.ExecutionFailureCount != 2 {
		t.Errorf("expected 2 execution failures, got %d", snap.ExecutionFailureCount)
	}
	if snap.ExecutionSuccessCount != 0 {
		t.Errorf("expected 0 execution successes, got %d", snap.ExecutionSuccessCount)
	}
	if snap.RetryCount != 2 {
		t.Errorf("expected 2 retries, got %d", snap.RetryCount)
	}
}

func TestToolCallSuccess(t *testing.T) {
	r := NewProviderRuntime("test-provider", nil)
	r.RecordToolCallOutcome(true)
	r.RecordToolCallOutcome(true)

	snap := r.Snapshot(context.Background())
	if snap.ToolCallSuccessCount != 2 {
		t.Errorf("expected 2 tool call successes, got %d", snap.ToolCallSuccessCount)
	}
}

func TestToolCallFailure(t *testing.T) {
	r := NewProviderRuntime("test-provider", nil)
	r.RecordToolCallOutcome(false)
	r.RecordToolCallOutcome(false)
	r.RecordToolCallOutcome(false)

	snap := r.Snapshot(context.Background())
	if snap.ToolCallFailureCount != 3 {
		t.Errorf("expected 3 tool call failures, got %d", snap.ToolCallFailureCount)
	}
}

func TestExecutionStepCount(t *testing.T) {
	r := NewProviderRuntime("test-provider", nil)
	for i := 0; i < 5; i++ {
		r.RecordExecutionOutcome(true, 0)
	}

	snap := r.Snapshot(context.Background())
	if snap.ExecutionCount != 5 {
		t.Errorf("expected 5 executions, got %d", snap.ExecutionCount)
	}
	if snap.ExecutionSuccessCount != 5 {
		t.Errorf("expected 5 successes, got %d", snap.ExecutionSuccessCount)
	}
}

func TestExecutionRetryCount(t *testing.T) {
	r := NewProviderRuntime("test-provider", nil)
	r.RecordExecutionOutcome(false, 1)
	r.RecordExecutionOutcome(false, 2)
	r.RecordExecutionOutcome(true, 1)

	snap := r.Snapshot(context.Background())
	if snap.RetryCount != 4 {
		t.Errorf("expected 4 total retries, got %d", snap.RetryCount)
	}
}

func TestExecutionDuration(t *testing.T) {
	r := NewProviderRuntime("test-provider", nil)
	r.RecordLatency(150)
	r.RecordLatency(200)

	snap := r.Snapshot(context.Background())
	if snap.LatencyMs != 200 {
		t.Errorf("expected latest latency 200, got %d", snap.LatencyMs)
	}
}

func TestProviderModelTelemetryIsolation(t *testing.T) {
	r1 := NewProviderRuntime("provider-a", nil)
	r2 := NewProviderRuntime("provider-b", nil)

	r1.RecordExecutionOutcome(true, 0)
	r1.RecordToolCallOutcome(true)
	r2.RecordExecutionOutcome(false, 1)
	r2.RecordToolCallOutcome(false)

	snap1 := r1.Snapshot(context.Background())
	snap2 := r2.Snapshot(context.Background())

	if snap1.ExecutionSuccessCount != 1 {
		t.Errorf("provider-a expected 1 success, got %d", snap1.ExecutionSuccessCount)
	}
	if snap2.ExecutionSuccessCount != 0 {
		t.Errorf("provider-b expected 0 successes, got %d", snap2.ExecutionSuccessCount)
	}
	if snap1.ToolCallSuccessCount != 1 {
		t.Errorf("provider-a expected 1 tool success, got %d", snap1.ToolCallSuccessCount)
	}
	if snap2.ToolCallFailureCount != 1 {
		t.Errorf("provider-b expected 1 tool failure, got %d", snap2.ToolCallFailureCount)
	}
}

func TestUnknownTelemetryState(t *testing.T) {
	r := NewProviderRuntime("test-provider", nil)

	snap := r.Snapshot(context.Background())
	if snap.ExecutionCount != 0 {
		t.Errorf("expected 0 executions for unknown state, got %d", snap.ExecutionCount)
	}
	if snap.ExecutionSuccessCount != 0 {
		t.Errorf("expected 0 success count for unknown state, got %d", snap.ExecutionSuccessCount)
	}
	if snap.ToolCallSuccessCount != 0 {
		t.Errorf("expected 0 tool call success for unknown state, got %d", snap.ToolCallSuccessCount)
	}
	if snap.ToolCallFailureCount != 0 {
		t.Errorf("expected 0 tool call failure for unknown state, got %d", snap.ToolCallFailureCount)
	}
}

func TestTelemetryConcurrentUpdates(t *testing.T) {
	r := NewProviderRuntime("concurrent", nil)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.RecordExecutionOutcome(true, 0)
			r.RecordToolCallOutcome(true)
		}()
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Snapshot(context.Background())
		}()
	}

	wg.Wait()

	snap := r.Snapshot(context.Background())
	if snap.ExecutionCount != 100 {
		t.Errorf("expected 100 executions, got %d", snap.ExecutionCount)
	}
	if snap.ToolCallSuccessCount != 100 {
		t.Errorf("expected 100 tool call successes, got %d", snap.ToolCallSuccessCount)
	}
}

func TestRuntimeSnapshotIncludesExecutionTelemetry(t *testing.T) {
	r := NewProviderRuntime("test", nil)
	r.UpdateState(StateHealthy, "test", nil)
	r.RecordLatency(100)
	r.RecordExecutionOutcome(true, 0)
	r.RecordExecutionOutcome(false, 1)
	r.RecordToolCallOutcome(true)
	r.RecordToolCallOutcome(false)

	snap := r.Snapshot(context.Background())
	if snap.State != StateHealthy {
		t.Errorf("expected StateHealthy, got %s", snap.State)
	}
	if snap.ExecutionCount != 2 {
		t.Errorf("expected 2 executions, got %d", snap.ExecutionCount)
	}
	if snap.ExecutionSuccessCount != 1 {
		t.Errorf("expected 1 success, got %d", snap.ExecutionSuccessCount)
	}
	if snap.ExecutionFailureCount != 1 {
		t.Errorf("expected 1 failure, got %d", snap.ExecutionFailureCount)
	}
	if snap.ToolCallSuccessCount != 1 {
		t.Errorf("expected 1 tool success, got %d", snap.ToolCallSuccessCount)
	}
	if snap.ToolCallFailureCount != 1 {
		t.Errorf("expected 1 tool failure, got %d", snap.ToolCallFailureCount)
	}
	if snap.RetryCount != 1 {
		t.Errorf("expected 1 retry, got %d", snap.RetryCount)
	}
}

func TestStoreSnapshotIncludesExecutionTelemetry(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := NewRuntimeStore(eb)

	r1 := NewProviderRuntime("openai", nil)
	r1.RecordExecutionOutcome(true, 0)
	r1.RecordToolCallOutcome(true)
	_ = store.Register(r1)

	r2 := NewProviderRuntime("anthropic", nil)
	r2.RecordExecutionOutcome(false, 1)
	r2.RecordToolCallOutcome(false)
	_ = store.Register(r2)

	snap := store.Snapshot(context.Background())
	if snap.Providers["openai"].ExecutionSuccessCount != 1 {
		t.Errorf("expected openai 1 success, got %d", snap.Providers["openai"].ExecutionSuccessCount)
	}
	if snap.Providers["anthropic"].ExecutionFailureCount != 1 {
		t.Errorf("expected anthropic 1 failure, got %d", snap.Providers["anthropic"].ExecutionFailureCount)
	}
}

func TestModelLevelTelemetryIsolation(t *testing.T) {
	r := NewProviderRuntime("openai", nil)

	// Record model-level telemetry for two different models.
	r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
	r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
	r.RecordExecutionOutcomeModel("gpt-4o", false, 0)
	r.RecordExecutionOutcomeModel("gpt-4o-mini", true, 0)
	r.RecordExecutionOutcomeModel("gpt-4o-mini", true, 0)

	snap := r.Snapshot(context.Background())

	// Provider-level should aggregate both models.
	if snap.ExecutionCount != 5 {
		t.Errorf("expected 5 total executions, got %d", snap.ExecutionCount)
	}
	if snap.ExecutionSuccessCount != 4 {
		t.Errorf("expected 4 successes, got %d", snap.ExecutionSuccessCount)
	}
	if snap.ExecutionFailureCount != 1 {
		t.Errorf("expected 1 failure, got %d", snap.ExecutionFailureCount)
	}

	// Model-level should be isolated.
	models := snap.ModelExecutions
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	gpt4o := models["gpt-4o"]
	if gpt4o.ExecutionCount != 3 {
		t.Errorf("gpt-4o expected 3 executions, got %d", gpt4o.ExecutionCount)
	}
	if gpt4o.ExecutionSuccessCount != 2 {
		t.Errorf("gpt-4o expected 2 successes, got %d", gpt4o.ExecutionSuccessCount)
	}
	if gpt4o.ExecutionFailureCount != 1 {
		t.Errorf("gpt-4o expected 1 failure, got %d", gpt4o.ExecutionFailureCount)
	}
	gpt4oMini := models["gpt-4o-mini"]
	if gpt4oMini.ExecutionCount != 2 {
		t.Errorf("gpt-4o-mini expected 2 executions, got %d", gpt4oMini.ExecutionCount)
	}
	if gpt4oMini.ExecutionSuccessCount != 2 {
		t.Errorf("gpt-4o-mini expected 2 successes, got %d", gpt4oMini.ExecutionSuccessCount)
	}
	if gpt4oMini.ExecutionFailureCount != 0 {
		t.Errorf("gpt-4o-mini expected 0 failures, got %d", gpt4oMini.ExecutionFailureCount)
	}
}

func TestModelLevelToolTelemetryIsolation(t *testing.T) {
	r := NewProviderRuntime("openai", nil)

	r.RecordToolCallOutcomeModel("gpt-4o", true)
	r.RecordToolCallOutcomeModel("gpt-4o", false)
	r.RecordToolCallOutcomeModel("gpt-4o-mini", true)
	r.RecordToolCallOutcomeModel("gpt-4o-mini", true)

	snap := r.Snapshot(context.Background())

	// Provider-level aggregates.
	if snap.ToolCallSuccessCount != 3 {
		t.Errorf("expected 3 tool successes, got %d", snap.ToolCallSuccessCount)
	}
	if snap.ToolCallFailureCount != 1 {
		t.Errorf("expected 1 tool failure, got %d", snap.ToolCallFailureCount)
	}

	// Model-level isolated.
	models := snap.ModelExecutions
	gpt4o := models["gpt-4o"]
	if gpt4o.ToolCallSuccessCount != 1 {
		t.Errorf("gpt-4o expected 1 tool success, got %d", gpt4o.ToolCallSuccessCount)
	}
	if gpt4o.ToolCallFailureCount != 1 {
		t.Errorf("gpt-4o expected 1 tool failure, got %d", gpt4o.ToolCallFailureCount)
	}
	gpt4oMini := models["gpt-4o-mini"]
	if gpt4oMini.ToolCallSuccessCount != 2 {
		t.Errorf("gpt-4o-mini expected 2 tool successes, got %d", gpt4oMini.ToolCallSuccessCount)
	}
	if gpt4oMini.ToolCallFailureCount != 0 {
		t.Errorf("gpt-4o-mini expected 0 tool failures, got %d", gpt4oMini.ToolCallFailureCount)
	}
}

func TestEmptyModelIDFallsBackToProviderLevel(t *testing.T) {
	r := NewProviderRuntime("openai", nil)

	// Empty model ID should only update provider-level counters.
	r.RecordExecutionOutcomeModel("", true, 0)
	r.RecordToolCallOutcomeModel("", true)

	snap := r.Snapshot(context.Background())
	if snap.ExecutionSuccessCount != 1 {
		t.Errorf("expected 1 success, got %d", snap.ExecutionSuccessCount)
	}
	if snap.ToolCallSuccessCount != 1 {
		t.Errorf("expected 1 tool success, got %d", snap.ToolCallSuccessCount)
	}
	if snap.ModelExecutions != nil {
		t.Errorf("expected no model executions for empty modelID, got %v", snap.ModelExecutions)
	}
}

func TestModelLevelRetryTelemetry(t *testing.T) {
	r := NewProviderRuntime("openai", nil)

	r.RecordExecutionOutcomeModel("gpt-4o", true, 2)
	r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
	r.RecordExecutionOutcomeModel("gpt-4o-mini", false, 1)

	snap := r.Snapshot(context.Background())

	// Provider-level retry count aggregates.
	if snap.RetryCount != 3 {
		t.Errorf("expected 3 total retries, got %d", snap.RetryCount)
	}

	// Model-level retry counts are isolated.
	gpt4o := snap.ModelExecutions["gpt-4o"]
	if gpt4o.RetryCount != 2 {
		t.Errorf("gpt-4o expected 2 retries, got %d", gpt4o.RetryCount)
	}
	gpt4oMini := snap.ModelExecutions["gpt-4o-mini"]
	if gpt4oMini.RetryCount != 1 {
		t.Errorf("gpt-4o-mini expected 1 retry, got %d", gpt4oMini.RetryCount)
	}
}

func TestConcurrentModelLevelTelemetry(t *testing.T) {
	r := NewProviderRuntime("openai", nil)
	var wg sync.WaitGroup

	// Concurrent model-level writes.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.RecordExecutionOutcomeModel("gpt-4o", true, 0)
		}()
	}
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.RecordExecutionOutcomeModel("gpt-4o-mini", false, 1)
		}()
	}
	// Concurrent reads.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Snapshot(context.Background())
		}()
	}

	wg.Wait()

	snap := r.Snapshot(context.Background())
	if snap.ExecutionCount != 80 {
		t.Errorf("expected 80 total executions, got %d", snap.ExecutionCount)
	}
	gpt4o := snap.ModelExecutions["gpt-4o"]
	if gpt4o.ExecutionCount != 50 {
		t.Errorf("gpt-4o expected 50 executions, got %d", gpt4o.ExecutionCount)
	}
	gpt4oMini := snap.ModelExecutions["gpt-4o-mini"]
	if gpt4oMini.ExecutionCount != 30 {
		t.Errorf("gpt-4o-mini expected 30 executions, got %d", gpt4oMini.ExecutionCount)
	}
	if gpt4oMini.RetryCount != 30 {
		t.Errorf("gpt-4o-mini expected 30 retries, got %d", gpt4oMini.RetryCount)
	}
}
