package adapter_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/runtime"
	"github.com/EffNine/conductor/internal/runtime/adapter"
)

func TestExecutionAdapterRecordsSuccess(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)
	a := adapter.NewExecutionToRuntimeAdapter(store, eb)

	_ = store.Register(runtime.NewProviderRuntime("openai", nil))

	a.OnExecutionFinished("openai", "", true, 0)

	got, err := store.Get("openai")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	snap := got.Snapshot(context.TODO())
	if snap.ExecutionSuccessCount != 1 {
		t.Errorf("ExecutionSuccessCount = %d, want 1", snap.ExecutionSuccessCount)
	}
	if snap.ExecutionFailureCount != 0 {
		t.Errorf("ExecutionFailureCount = %d, want 0", snap.ExecutionFailureCount)
	}
}

func TestExecutionAdapterRecordsFailure(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)
	a := adapter.NewExecutionToRuntimeAdapter(store, eb)

	_ = store.Register(runtime.NewProviderRuntime("openai", nil))

	a.OnExecutionFinished("openai", "", false, 1)

	got, err := store.Get("openai")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	snap := got.Snapshot(context.TODO())
	if snap.ExecutionFailureCount != 1 {
		t.Errorf("ExecutionFailureCount = %d, want 1", snap.ExecutionFailureCount)
	}
	if snap.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", snap.RetryCount)
	}
}

func TestExecutionAdapterRecordsToolCall(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)
	a := adapter.NewExecutionToRuntimeAdapter(store, eb)

	_ = store.Register(runtime.NewProviderRuntime("openai", nil))

	a.OnToolCall("openai", "", true)
	a.OnToolCall("openai", "", true)
	a.OnToolCall("openai", "", false)

	got, err := store.Get("openai")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	snap := got.Snapshot(context.TODO())
	if snap.ToolCallSuccessCount != 2 {
		t.Errorf("ToolCallSuccessCount = %d, want 2", snap.ToolCallSuccessCount)
	}
	if snap.ToolCallFailureCount != 1 {
		t.Errorf("ToolCallFailureCount = %d, want 1", snap.ToolCallFailureCount)
	}
}

func TestExecutionAdapterEventsPublished(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)
	a := adapter.NewExecutionToRuntimeAdapter(store, eb)

	var execStarted, execFinished, toolCompleted atomic.Int64
	eb.Subscribe(eventbus.ExecutionStarted, func(e eventbus.Event) {
		execStarted.Store(1)
	})
	eb.Subscribe(eventbus.ExecutionFinished, func(e eventbus.Event) {
		execFinished.Store(1)
	})
	eb.Subscribe(eventbus.ToolCallCompleted, func(e eventbus.Event) {
		toolCompleted.Store(1)
	})

	_ = store.Register(runtime.NewProviderRuntime("openai", nil))
	a.OnExecutionStarted("openai")
	a.OnExecutionFinished("openai", "", true, 0)
	a.OnToolCall("openai", "", true)

	waitForAtomicFlags(&execStarted, &execFinished, &toolCompleted, 200)

	if execStarted.Load() != 1 {
		t.Error("expected ExecutionStarted event")
	}
	if execFinished.Load() != 1 {
		t.Error("expected ExecutionFinished event")
	}
	if toolCompleted.Load() != 1 {
		t.Error("expected ToolCallCompleted event")
	}
}

func waitForAtomicFlags(started, finished, tool *atomic.Int64, maxMs int) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if started.Load() == 1 && finished.Load() == 1 && tool.Load() == 1 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Duration(maxMs) * time.Millisecond):
	}
}

func TestExecutionAdapterNilStore(t *testing.T) {
	eb := eventbus.NewEventBus()
	a := adapter.NewExecutionToRuntimeAdapter(nil, eb)

	// Should not panic
	a.OnExecutionFinished("openai", "", true, 0)
	a.OnToolCall("openai", "", true)
	a.OnExecutionStarted("openai")
}

func TestExecutionAdapterNilEventBus(t *testing.T) {
	store := runtime.NewRuntimeStore(nil)
	a := adapter.NewExecutionToRuntimeAdapter(store, nil)

	_ = store.Register(runtime.NewProviderRuntime("openai", nil))

	// Should not panic
	a.OnExecutionStarted("openai")
	a.OnExecutionFinished("openai", "", true, 0)
	a.OnToolCall("openai", "", true)

	got, _ := store.Get("openai")
	snap := got.Snapshot(context.TODO())
	if snap.ExecutionSuccessCount != 1 {
		t.Errorf("expected 1 success even without event bus, got %d", snap.ExecutionSuccessCount)
	}
}
