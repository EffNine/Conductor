package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/eventbus"
)

func TestNewProviderRuntime(t *testing.T) {
	runtime := NewProviderRuntime("test-provider", nil)

	if runtime.Name() != "test-provider" {
		t.Errorf("expected 'test-provider', got %s", runtime.Name())
	}
	if runtime.State() != StateUnknown {
		t.Errorf("expected StateUnknown, got %s", runtime.State())
	}
	if runtime.IsHealthy() != true {
		t.Error("expected to be healthy by default")
	}
}

func TestProviderRuntimeStateTransition(t *testing.T) {
	runtime := NewProviderRuntime("test", nil)

	// Transition to healthy
	runtime.UpdateState(StateHealthy, "probe passed", nil)
	if runtime.State() != StateHealthy {
		t.Errorf("expected StateHealthy, got %s", runtime.State())
	}
	if !runtime.IsHealthy() {
		t.Error("expected to be healthy after transition")
	}

	// Transition to degraded
	runtime.UpdateState(StateDegraded, "high error rate", nil)
	if runtime.State() != StateDegraded {
		t.Errorf("expected StateDegraded, got %s", runtime.State())
	}
	if !runtime.IsHealthy() {
		t.Error("expected to be healthy (degraded is still healthy)")
	}

	// Transition to unhealthy
	runtime.UpdateState(StateUnhealthy, "probe failed", nil)
	if runtime.State() != StateUnhealthy {
		t.Errorf("expected StateUnhealthy, got %s", runtime.State())
	}
	if runtime.IsHealthy() {
		t.Error("expected to not be healthy after unhealthy transition")
	}
}

func TestProviderRuntimeLatencyRecording(t *testing.T) {
	runtime := NewProviderRuntime("test", nil)

	runtime.RecordLatency(100)
	snap := runtime.Snapshot(context.Background())
	if snap.LatencyMs != 100 {
		t.Errorf("expected 100ms latency, got %d", snap.LatencyMs)
	}

	runtime.RecordLatency(200)
	snap = runtime.Snapshot(context.Background())
	if snap.LatencyMs != 200 {
		t.Errorf("expected 200ms latency, got %d", snap.LatencyMs)
	}
}

func TestProviderRuntimeSuccessFailureRecording(t *testing.T) {
	runtime := NewProviderRuntime("test", nil)

	runtime.RecordSuccess()
	runtime.RecordSuccess()
	runtime.RecordError(nil)

	stats := runtime.GetStats()
	if stats.SuccessCount != 2 {
		t.Errorf("expected 2 successes, got %d", stats.SuccessCount)
	}
	if stats.FailureCount != 1 {
		t.Errorf("expected 1 failure, got %d", stats.FailureCount)
	}
	if stats.TotalRequests != 3 {
		t.Errorf("expected 3 total requests, got %d", stats.TotalRequests)
	}
	if stats.SuccessRate != 2.0/3.0 {
		t.Errorf("expected success rate %.2f, got %f", 2.0/3.0, stats.SuccessRate)
	}
}

func TestProviderRuntimeSnapshot(t *testing.T) {
	runtime := NewProviderRuntime("test", nil)

	runtime.UpdateState(StateHealthy, "initial", nil)
	runtime.RecordLatency(150)
	runtime.RecordSuccess()
	runtime.RecordError(nil)

	snap := runtime.Snapshot(context.Background())

	if snap.State != StateHealthy {
		t.Errorf("expected StateHealthy, got %s", snap.State)
	}
	if snap.LatencyMs != 150 {
		t.Errorf("expected 150ms latency, got %d", snap.LatencyMs)
	}
	// RecordLatency, RecordSuccess, RecordError = 3 total requests, 1 error
	if snap.ErrorRate != 1.0/3.0 {
		t.Errorf("expected error rate 0.333, got %f", snap.ErrorRate)
	}
}

func TestProviderRuntimeStateChanges(t *testing.T) {
	runtime := NewProviderRuntime("test", nil)

	runtime.UpdateState(StateHealthy, "probe passed", map[string]any{"score": 0.95})
	runtime.UpdateState(StateDegraded, "high error rate", nil)
	runtime.UpdateState(StateHealthy, "recovered", nil)

	changes := runtime.GetStateChanges()
	if len(changes) != 3 {
		t.Errorf("expected 3 state changes, got %d", len(changes))
	}

	if changes[0].From != StateUnknown || changes[0].To != StateHealthy {
		t.Error("expected first change from unknown to healthy")
	}
	if changes[1].From != StateHealthy || changes[1].To != StateDegraded {
		t.Error("expected second change from healthy to degraded")
	}
	if changes[2].From != StateDegraded || changes[2].To != StateHealthy {
		t.Error("expected third change from degraded to healthy")
	}
}

func TestProviderRuntimeMetadata(t *testing.T) {
	runtime := NewProviderRuntime("test", nil)

	runtime.SetMetadata("region", "us-east-1")
	runtime.SetMetadata("version", "1.0")

	meta := runtime.GetMetadata()
	if meta["region"] != "us-east-1" {
		t.Error("expected region to be us-east-1")
	}
	if meta["version"] != "1.0" {
		t.Error("expected version to be 1.0")
	}
}

func TestProviderRuntimeTags(t *testing.T) {
	runtime := NewProviderRuntime("test", nil)

	runtime.SetTag("env", "production")
	runtime.SetTag("team", "platform")

	val, ok := runtime.GetTag("env")
	if !ok || val != "production" {
		t.Error("expected env tag to be production")
	}

	val, ok = runtime.GetTag("team")
	if !ok || val != "platform" {
		t.Error("expected team tag to be platform")
	}

	_, ok = runtime.GetTag("missing")
	if ok {
		t.Error("expected missing tag to not exist")
	}
}

func TestRuntimeStoreRegisterAndGet(t *testing.T) {
	bus := eventbus.NewEventBus()
	store := NewRuntimeStore(bus)

	runtime1 := NewProviderRuntime("openai", nil)
	runtime2 := NewProviderRuntime("anthropic", nil)

	if err := store.Register(runtime1); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := store.Register(runtime2); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	retrieved, err := store.Get("openai")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if retrieved.Name() != "openai" {
		t.Errorf("expected 'openai', got %s", retrieved.Name())
	}

	retrieved, err = store.Get("anthropic")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if retrieved.Name() != "anthropic" {
		t.Errorf("expected 'anthropic', got %s", retrieved.Name())
	}
}

func TestRuntimeStoreDuplicateRegister(t *testing.T) {
	store := NewRuntimeStore(nil)

	runtime := NewProviderRuntime("test", nil)
	if err := store.Register(runtime); err != nil {
		t.Fatalf("expected no error on first register, got %v", err)
	}

	if err := store.Register(runtime); err == nil {
		t.Error("expected error on duplicate register")
	}
}

func TestRuntimeStoreDeregister(t *testing.T) {
	store := NewRuntimeStore(nil)

	runtime := NewProviderRuntime("test", nil)
	_ = store.Register(runtime)

	if err := store.Deregister("test"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, err := store.Get("test")
	if err == nil {
		t.Error("expected error after deregister")
	}
}

func TestRuntimeStoreGetAll(t *testing.T) {
	store := NewRuntimeStore(nil)

	_ = store.Register(NewProviderRuntime("openai", nil))
	_ = store.Register(NewProviderRuntime("anthropic", nil))
	_ = store.Register(NewProviderRuntime("gemini", nil))

	all, err := store.GetAll()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 providers, got %d", len(all))
	}
}

func TestRuntimeStoreSnapshot(t *testing.T) {
	store := NewRuntimeStore(nil)

	r1 := NewProviderRuntime("openai", nil)
	r1.UpdateState(StateHealthy, "probe passed", nil)
	r1.RecordLatency(100)
	_ = store.Register(r1)

	r2 := NewProviderRuntime("anthropic", nil)
	r2.UpdateState(StateDegraded, "high latency", nil)
	r2.RecordLatency(500)
	_ = store.Register(r2)

	r3 := NewProviderRuntime("gemini", nil)
	r3.UpdateState(StateUnhealthy, "probe failed", nil)
	_ = store.Register(r3)

	snap := store.Snapshot(context.Background())

	if snap.GlobalState.TotalProviders != 3 {
		t.Errorf("expected 3 providers, got %d", snap.GlobalState.TotalProviders)
	}
	if snap.GlobalState.HealthyProviders != 1 {
		t.Errorf("expected 1 healthy, got %d", snap.GlobalState.HealthyProviders)
	}
	if snap.GlobalState.DegradedProviders != 1 {
		t.Errorf("expected 1 degraded, got %d", snap.GlobalState.DegradedProviders)
	}
	if snap.GlobalState.UnhealthyProviders != 1 {
		t.Errorf("expected 1 unhealthy, got %d", snap.GlobalState.UnhealthyProviders)
	}
}

func TestRuntimeStoreUpdate(t *testing.T) {
	store := NewRuntimeStore(nil)

	runtime := NewProviderRuntime("test", nil)
	_ = store.Register(runtime)

	err := store.Update("test", func(r ProviderRuntime) error {
		r.UpdateState(StateHealthy, "updated", nil)
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	retrieved, _ := store.Get("test")
	if retrieved.State() != StateHealthy {
		t.Errorf("expected StateHealthy, got %s", retrieved.State())
	}
}

func TestRuntimeStoreWatch(t *testing.T) {
	store := NewRuntimeStore(nil)

	runtime := NewProviderRuntime("test", nil)
	_ = store.Register(runtime)

	done := make(chan ProviderStateSnapshot, 1)
	id := store.Watch("test", func(snap ProviderStateSnapshot) {
		done <- snap
	})

	// Trigger update
	_ = store.Update("test", func(r ProviderRuntime) error {
		r.UpdateState(StateHealthy, "watch test", nil)
		return nil
	})

	// Wait for notification with timeout
	select {
	case snap := <-done:
		if snap.State != StateHealthy {
			t.Errorf("expected received StateHealthy, got %s", snap.State)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for watch notification")
	}

	store.Unwatch(id)
}

func TestRuntimeStoreWatchAll(t *testing.T) {
	store := NewRuntimeStore(nil)

	runtime := NewProviderRuntime("test", nil)
	_ = store.Register(runtime)

	done := make(chan ProviderStateSnapshot, 1)
	id := store.Watch("", func(snap ProviderStateSnapshot) {
		done <- snap
	})

	// Trigger update
	_ = store.Update("test", func(r ProviderRuntime) error {
		r.UpdateState(StateHealthy, "watch all", nil)
		return nil
	})

	select {
	case snap := <-done:
		if snap.State != StateHealthy {
			t.Errorf("expected received StateHealthy, got %s", snap.State)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for watch notification")
	}

	store.Unwatch(id)
}

func TestRuntimeStoreEventPublishing(t *testing.T) {
	bus := eventbus.NewEventBus()
	store := NewRuntimeStore(bus)

	done := make(chan string, 1)
	bus.Subscribe(eventbus.ProviderRegistered, func(e eventbus.Event) {
		done <- e.Payload.(string)
	})

	runtime := NewProviderRuntime("test", nil)
	_ = store.Register(runtime)

	select {
	case name := <-done:
		if name != "test" {
			t.Errorf("expected 'test', got %s", name)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for registered event")
	}
}

func TestRuntimeStoreNegativeCases(t *testing.T) {
	store := NewRuntimeStore(nil)

	// Get non-existent provider
	_, err := store.Get("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent provider")
	}

	// Deregister non-existent provider
	err = store.Deregister("nonexistent")
	if err == nil {
		t.Error("expected error when deregistering non-existent provider")
	}

	// Update non-existent provider
	err = store.Update("nonexistent", func(r ProviderRuntime) error {
		return nil
	})
	if err == nil {
		t.Error("expected error when updating non-existent provider")
	}
}

func TestRuntimeConcurrentAccess(t *testing.T) {
	store := NewRuntimeStore(nil)

	runtime := NewProviderRuntime("test", nil)
	_ = store.Register(runtime)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			_ = store.Update("test", func(r ProviderRuntime) error {
				r.RecordLatency(int64(100 + val))
				return nil
			})
		}(i)
	}

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.Snapshot(context.Background())
		}()
	}

	wg.Wait()
}

func TestRuntimeUptime(t *testing.T) {
	runtime := NewProviderRuntime("test", nil)

	uptime := runtime.GetUptime()
	if uptime < 0 {
		t.Error("expected non-negative uptime")
	}
	if uptime > time.Second {
		t.Error("expected uptime less than 1 second for fresh runtime")
	}
}

func TestRuntimeErrorRecording(t *testing.T) {
	runtime := NewProviderRuntime("test", nil)

	testErr := errors.New("test error")
	runtime.RecordError(testErr)

	stats := runtime.GetStats()
	if stats.LastError != "test error" {
		t.Errorf("expected 'test error', got %s", stats.LastError)
	}
	if stats.FailureCount != 1 {
		t.Errorf("expected 1 failure, got %d", stats.FailureCount)
	}
}

func TestRuntimeStateChangeLimit(t *testing.T) {
	runtime := NewProviderRuntime("test", nil)

	// Record more than 100 state changes
	for i := 0; i < 150; i++ {
		if i%2 == 0 {
			runtime.UpdateState(StateHealthy, "healthy", nil)
		} else {
			runtime.UpdateState(StateUnhealthy, "unhealthy", nil)
		}
	}

	changes := runtime.GetStateChanges()
	if len(changes) > 100 {
		t.Errorf("expected at most 100 state changes, got %d", len(changes))
	}
	if len(changes) != 100 {
		t.Errorf("expected exactly 100 state changes, got %d", len(changes))
	}
}
