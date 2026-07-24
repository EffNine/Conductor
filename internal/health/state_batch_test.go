package health_test

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/health"
)

func TestHealthyToRecovering(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	store.SetBackoff(config.ProbeBackoffConfig{
		Enabled:      true,
		InitialDelay: 30 * time.Second,
		MaxDelay:     time.Hour,
		Multiplier:   3.5,
	})
	store.MarkFilterReady()
	store.RecordSuccess("openai/gpt", "openai", "gpt", 10)
	store.RecordFailure("openai/gpt", "openai", "gpt", "model not found", http.StatusNotFound)

	st := store.Get("openai/gpt")
	if st == nil || st.State != health.StateRecovering {
		t.Fatalf("expected recovering, got %+v", st)
	}
	if st.NextProbeTime.IsZero() {
		t.Fatal("expected next probe time from backoff")
	}
	if store.ShouldAdvertise("openai/gpt", "openai") {
		t.Fatal("recovering model must not be advertised")
	}
}

func TestRecoveringToHealthy(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	store.SetBackoff(config.ProbeBackoffConfig{Enabled: true, InitialDelay: time.Second, MaxDelay: time.Hour, Multiplier: 2})
	store.RecordFailure("openai/gpt", "openai", "gpt", "model not found", http.StatusNotFound)
	store.RecordSuccess("openai/gpt", "openai", "gpt", 25)

	st := store.Get("openai/gpt")
	if st == nil || st.State != health.StateHealthy {
		t.Fatalf("expected healthy, got %+v", st)
	}
	if st.ConsecutiveFails != 0 || !st.NextProbeTime.IsZero() {
		t.Fatalf("expected reset backoff fields, got %+v", st)
	}
}

func TestUnknownStateDefault(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	store.MarkFilterReady()
	if !store.ShouldAdvertise("openai/new", "openai") {
		t.Fatal("unknown_as_reachable=true should advertise unprobed models")
	}

	storeFalse := health.NewModelStatusStore(1, false)
	storeFalse.MarkFilterReady()
	if storeFalse.ShouldAdvertise("openai/new", "openai") {
		t.Fatal("unknown_as_reachable=false should hide unprobed models after filter ready")
	}
}

func TestDegradedThresholdTransition(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	store.RecordSuccess("openai/gpt", "openai", "gpt", 10)
	store.UpdateErrorRate("openai/gpt", "openai", "gpt", 0.20, 0.15, 0.05)

	st := store.Get("openai/gpt")
	if st == nil || st.State != health.StateDegraded {
		t.Fatalf("expected degraded, got %+v", st)
	}
	if !store.ShouldAdvertise("openai/gpt", "openai") {
		t.Fatal("degraded models stay in catalog")
	}

	store.UpdateErrorRate("openai/gpt", "openai", "gpt", 0.01, 0.15, 0.05)
	st = store.Get("openai/gpt")
	if st.State != health.StateHealthy {
		t.Fatalf("expected healthy after recovery threshold, got %s", st.State)
	}
}

func TestErrorRateWindow(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	et := health.NewErrorTracker(config.ErrorTrackingConfig{
		Enabled:            true,
		Window:             time.Minute,
		UnhealthyThreshold: 0.15,
		RecoveryThreshold:  0.05,
	}, store)

	// 20% errors over 10 requests → degraded
	for i := 0; i < 8; i++ {
		et.RecordRequest("openai/gpt", "openai", "gpt", true)
	}
	for i := 0; i < 2; i++ {
		et.RecordRequest("openai/gpt", "openai", "gpt", false)
	}
	if rate := et.GetErrorRate("openai/gpt"); rate < 0.19 || rate > 0.21 {
		t.Fatalf("error rate = %v, want ~0.2", rate)
	}
	st := store.Get("openai/gpt")
	if st == nil || st.State != health.StateDegraded {
		t.Fatalf("expected degraded from error tracker, got %+v", st)
	}
}

func TestBatcherAtomic(t *testing.T) {
	var mu sync.Mutex
	var batches []int
	b := health.NewCatalogBatcher(20*time.Millisecond, func(results []health.ProbeResult) {
		mu.Lock()
		batches = append(batches, len(results))
		mu.Unlock()
	})
	b.Start()
	defer b.Stop()

	for i := 0; i < 5; i++ {
		b.Submit(health.ProbeResult{ModelID: "m", Success: true})
	}
	time.Sleep(60 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	total := 0
	for _, n := range batches {
		total += n
	}
	if total != 5 {
		t.Fatalf("batched %d results in %v, want 5", total, batches)
	}
}

func TestCatalogNoRaceCondition(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	store.MarkFilterReady()

	var advertiseHits atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if store.ShouldAdvertise("openai/gpt", "openai") {
					advertiseHits.Add(1)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			store.ApplyBatch([]health.ProbeResult{{
				ModelID: "openai/gpt", Provider: "openai", ProviderModelID: "gpt",
				Success: j%2 == 0, LatencyMs: 10,
				ErrMsg: "model not found", StatusCode: http.StatusNotFound,
			}})
		}
	}()
	wg.Wait()
	// No data race under -race; hit count is incidental.
	_ = advertiseHits.Load()
}

func TestApplyBatchSetsRecoveringBackoff(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	store.SetBackoff(config.ProbeBackoffConfig{
		Enabled: true, InitialDelay: 45 * time.Second, MaxDelay: time.Hour, Multiplier: 2, JitterFraction: 0.01,
	})
	store.ApplyBatch([]health.ProbeResult{{
		ModelID: "openai/gpt", Provider: "openai", ProviderModelID: "gpt",
		Success: false, ErrMsg: "model not found", StatusCode: http.StatusNotFound,
	}})
	st := store.Get("openai/gpt")
	if st == nil || st.State != health.StateRecovering {
		t.Fatalf("got %+v", st)
	}
	if st.NextProbeTime.Before(time.Now().Add(30 * time.Second)) {
		t.Fatalf("next probe too soon: %v", st.NextProbeTime)
	}
}

func TestModelsNeedingRetry(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	store.SetBackoff(config.ProbeBackoffConfig{Enabled: true, InitialDelay: time.Millisecond, MaxDelay: time.Hour, Multiplier: 2})
	store.RecordFailure("openai/a", "openai", "a", "model not found", http.StatusNotFound)
	time.Sleep(5 * time.Millisecond)
	due := store.ModelsNeedingRetry(time.Now().UTC())
	if len(due) != 1 || due[0].ModelID != "openai/a" {
		t.Fatalf("due=%+v", due)
	}
}

func TestStatusDetailsIncludesCountdown(t *testing.T) {
	store := health.NewModelStatusStore(1, true)
	store.SetBackoff(config.ProbeBackoffConfig{Enabled: true, InitialDelay: time.Minute, MaxDelay: time.Hour, Multiplier: 2})
	store.RecordFailure("openai/a", "openai", "a", "model not found", http.StatusNotFound)
	details := store.GetStatusDetails()
	if len(details) != 1 {
		t.Fatalf("details=%d", len(details))
	}
	if details[0].State != health.StateRecovering {
		t.Fatalf("state=%s", details[0].State)
	}
	if details[0].ProbeError == nil || *details[0].ProbeError == "" {
		t.Fatal("expected probe_error")
	}
	if details[0].RetryCountdownMs == nil {
		t.Fatal("expected retry countdown")
	}
}
