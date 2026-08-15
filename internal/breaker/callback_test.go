package breaker_test

import (
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/breaker"
)

// ── State change callback tests ─────────────────────────────────────────────

func TestBreakerCallbackFiredOnOpen(t *testing.T) {
	b := breaker.New(breaker.Config{FailureThreshold: 2, RecoveryTimeout: 30 * time.Second})
	var states []breaker.State
	b.OnStateChange(func(s breaker.State) { states = append(states, s) })

	b.RecordFailure()
	b.RecordFailure() // triggers open

	if len(states) != 1 {
		t.Fatalf("expected 1 callback, got %d", len(states))
	}
	if states[0] != breaker.StateOpen {
		t.Errorf("state = %v, want open", states[0])
	}
}

func TestBreakerCallbackFiredOnHalfOpen(t *testing.T) {
	// RecoveryTimeout must be > 0 (New() defaults 0 to 30s). Use 50ms.
	b := breaker.New(breaker.Config{FailureThreshold: 1, RecoveryTimeout: 50 * time.Millisecond})
	var states []breaker.State
	b.OnStateChange(func(s breaker.State) { states = append(states, s) })

	b.RecordFailure() // opens
	if len(states) != 1 {
		t.Fatalf("expected 1 callback after open, got %d", len(states))
	}
	if states[0] != breaker.StateOpen {
		t.Errorf("state = %v, want open", states[0])
	}

	// Wait for recovery timeout so Allow transitions to half-open.
	time.Sleep(60 * time.Millisecond)
	b.Allow()
	if len(states) != 2 {
		t.Fatalf("expected 2 callbacks after allow, got %d", len(states))
	}
	if states[1] != breaker.StateHalfOpen {
		t.Errorf("state = %v, want half_open", states[1])
	}
}

func TestBreakerCallbackFiredOnClose(t *testing.T) {
	// RecoveryTimeout must be > 0 (New() defaults 0 to 30s). Use 50ms.
	b := breaker.New(breaker.Config{FailureThreshold: 1, RecoveryTimeout: 50 * time.Millisecond, SuccessThreshold: 2})
	var states []breaker.State
	b.OnStateChange(func(s breaker.State) { states = append(states, s) })

	b.RecordFailure() // opens
	time.Sleep(60 * time.Millisecond)
	b.Allow()         // half-open
	b.RecordSuccess() // still half-open (need 2 successes)
	b.RecordSuccess() // closes

	if len(states) != 3 {
		t.Fatalf("expected 3 callbacks, got %d", len(states))
	}
	if states[0] != breaker.StateOpen {
		t.Errorf("state[0] = %v, want open", states[0])
	}
	if states[1] != breaker.StateHalfOpen {
		t.Errorf("state[1] = %v, want half_open", states[1])
	}
	if states[2] != breaker.StateClosed {
		t.Errorf("state[2] = %v, want closed", states[2])
	}
}

func TestBreakerNoCallbackOnUnchangedState(t *testing.T) {
	b := breaker.New(breaker.Config{FailureThreshold: 5, RecoveryTimeout: 30 * time.Second})
	var count int
	b.OnStateChange(func(s breaker.State) { _ = s; count++ })

	// Multiple successes while closed should not trigger callbacks.
	for i := 0; i < 10; i++ {
		b.RecordSuccess()
	}
	if count != 0 {
		t.Errorf("expected 0 callbacks, got %d", count)
	}
}

func TestBreakerConcurrentCallbacks(t *testing.T) {
	b := breaker.New(breaker.Config{FailureThreshold: 100, RecoveryTimeout: 30 * time.Second})
	var mu sync.Mutex
	var states []breaker.State
	b.OnStateChange(func(s breaker.State) {
		mu.Lock()
		states = append(states, s)
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.RecordSuccess()
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(states) != 0 {
		t.Errorf("expected 0 callbacks under concurrent success, got %d", len(states))
	}
}

func TestBreakerCallbackNotCalledOnSameState(t *testing.T) {
	b := breaker.New(breaker.Config{FailureThreshold: 5, RecoveryTimeout: 30 * time.Second})
	var count int
	b.OnStateChange(func(s breaker.State) { count++ })

	// RecordFailure below threshold — no state change.
	b.RecordFailure()
	if count != 0 {
		t.Errorf("expected 0 callbacks, got %d", count)
	}
}
