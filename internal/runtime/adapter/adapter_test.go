package adapter_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/runtime"
	"github.com/EffNine/conductor/internal/runtime/adapter"
	"github.com/EffNine/conductor/internal/usage"
)

// ── Runtime provider registration ───────────────────────────────────────────

func TestRuntimeProviderRegistration(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)

	r := runtime.NewProviderRuntime("openai", nil)
	if err := store.Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := store.Get("openai")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name() != "openai" {
		t.Errorf("Name = %q, want openai", got.Name())
	}

	err = store.Register(runtime.NewProviderRuntime("openai", nil))
	if err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

// ── Runtime state update ────────────────────────────────────────────────────

func TestRuntimeStateUpdate(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)

	r := runtime.NewProviderRuntime("test", nil)
	if err := store.Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := store.Update("test", func(rt runtime.ProviderRuntime) error {
		rt.UpdateState(runtime.StateHealthy, "probe passed", nil)
		rt.RecordLatency(50)
		rt.RecordSuccess()
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.Get("test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State() != runtime.StateHealthy {
		t.Errorf("State = %q, want healthy", got.State())
	}
	snap := got.Snapshot(context.Background())
	if snap.LatencyMs != 50 {
		t.Errorf("LatencyMs = %d, want 50", snap.LatencyMs)
	}
}

// ── Runtime stats aggregation ───────────────────────────────────────────────

func TestRuntimeStatsAggregation(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)
	mgr := runtime.NewManager(store)

	r1 := runtime.NewProviderRuntime("a", nil)
	r1.UpdateState(runtime.StateHealthy, "", nil)
	r1.RecordSuccess()
	r1.RecordSuccess()
	r1.RecordError(errors.New("boom"))
	r1.RecordLatency(100)

	r2 := runtime.NewProviderRuntime("b", nil)
	r2.UpdateState(runtime.StateDegraded, "", nil)
	r2.RecordSuccess()
	r2.RecordLatency(200)

	_ = store.Register(r1)
	_ = store.Register(r2)

	stats := mgr.AggregateStats()
	if stats.TotalProviders != 2 {
		t.Errorf("TotalProviders = %d, want 2", stats.TotalProviders)
	}
	if stats.TotalSuccess != 3 {
		t.Errorf("TotalSuccess = %d, want 3", stats.TotalSuccess)
	}
	if stats.TotalFailure != 1 {
		t.Errorf("TotalFailure = %d, want 1", stats.TotalFailure)
	}
}

// ── Snapshot contains real stats ────────────────────────────────────────────

func TestSnapshotContainsRealStats(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)

	r := runtime.NewProviderRuntime("test", nil)
	r.UpdateState(runtime.StateHealthy, "", nil)
	r.RecordSuccess()
	r.RecordError(errors.New("fail"))
	r.RecordLatency(42)
	_ = store.Register(r)

	snap := store.Snapshot(context.Background())
	if len(snap.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(snap.Providers))
	}
	ps := snap.Providers["test"]
	if ps.LatencyMs != 42 {
		t.Errorf("LatencyMs = %d, want 42", ps.LatencyMs)
	}
	if ps.ErrorRate <= 0 {
		t.Errorf("ErrorRate = %f, want > 0", ps.ErrorRate)
	}
}

// ── Health adapter updates runtime ──────────────────────────────────────────

func TestHealthAdapterUpdatesRuntime(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)
	a := adapter.NewHealthToRuntimeAdapter(store)

	_ = store.Register(runtime.NewProviderRuntime("openai", nil))

	a.OnProbeResult(health.ProbeResult{
		Provider:  "openai",
		Success:   true,
		LatencyMs: 100,
	})

	got, err := store.Get("openai")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State() != runtime.StateHealthy {
		t.Errorf("State = %q, want healthy", got.State())
	}
}

// ── Usage adapter updates runtime ────────────────────────────────────────────

func TestUsageAdapterUpdatesRuntime(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)
	a := adapter.NewUsageToRuntimeAdapter(store)

	_ = store.Register(runtime.NewProviderRuntime("openai", nil))

	a.OnUsageRecord(&usage.Record{
		Provider:  "openai",
		LatencyMs: 75,
	})

	got, err := store.Get("openai")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	snap := got.Snapshot(context.Background())
	if snap.LatencyMs != 75 {
		t.Errorf("LatencyMs = %d, want 75", snap.LatencyMs)
	}
}

// ── Breaker adapter updates runtime ─────────────────────────────────────────

func TestBreakerAdapterUpdatesRuntime(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)
	a := adapter.NewBreakerToRuntimeAdapter(store)

	_ = store.Register(runtime.NewProviderRuntime("openai", nil))

	a.OnBreakerStateChange("openai", breaker.BreakerStats{State: breaker.StateClosed})
	got, _ := store.Get("openai")
	if got.State() != runtime.StateHealthy {
		t.Errorf("State = %q, want healthy", got.State())
	}

	a.OnBreakerStateChange("openai", breaker.BreakerStats{State: breaker.StateOpen})
	got, _ = store.Get("openai")
	if got.State() != runtime.StateUnhealthy {
		t.Errorf("State = %q, want unhealthy", got.State())
	}
}

// ── Concurrent runtime updates ──────────────────────────────────────────────

func TestConcurrentRuntimeUpdates(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)

	r := runtime.NewProviderRuntime("concurrent", nil)
	_ = store.Register(r)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.Update("concurrent", func(rt runtime.ProviderRuntime) error {
				rt.RecordSuccess()
				rt.RecordLatency(10)
				return nil
			})
		}()
	}
	wg.Wait()

	got, err := store.Get("concurrent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	snap := got.Snapshot(context.Background())
	if snap.LatencyMs != 10 {
		t.Errorf("LatencyMs = %d, want 10", snap.LatencyMs)
	}
}

// ── Concurrent snapshot reads ───────────────────────────────────────────────

func TestConcurrentSnapshotReads(t *testing.T) {
	eb := eventbus.NewEventBus()
	store := runtime.NewRuntimeStore(eb)

	r := runtime.NewProviderRuntime("reader", nil)
	_ = store.Register(r)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.Update("reader", func(rt runtime.ProviderRuntime) error {
				rt.RecordSuccess()
				return nil
			})
			_ = store.Snapshot(context.Background())
		}()
	}
	wg.Wait()
}
