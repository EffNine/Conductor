package breaker

import (
	"sync"
	"testing"
	"time"
)

func TestBreaker_ClosedAllows(t *testing.T) {
	b := New(DefaultConfig())
	if b.Allow() != ResultAllowed {
		t.Fatal("expected allowed in closed state")
	}
	if b.State() != StateClosed {
		t.Fatalf("expected closed, got %s", b.State())
	}
}

func TestBreaker_OpenRejects(t *testing.T) {
	b := New(Config{
		FailureThreshold: 2,
		RecoveryTimeout:  10 * time.Second,
		SuccessThreshold: 1,
	})
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("expected open after %d failures, got %s", 2, b.State())
	}
	if b.Allow() != ResultRejected {
		t.Fatal("expected rejected in open state")
	}
	stats := b.Stats()
	if stats.TotalRejections != 1 {
		t.Fatalf("expected 1 rejection, got %d", stats.TotalRejections)
	}
}

func TestBreaker_HalfOpenAfterTimeout(t *testing.T) {
	b := New(Config{
		FailureThreshold: 2,
		RecoveryTimeout:  50 * time.Millisecond,
		SuccessThreshold: 1,
	})
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatal("expected open")
	}
	time.Sleep(60 * time.Millisecond)
	if b.Allow() != ResultAllowed {
		t.Fatal("expected allowed after recovery timeout")
	}
	if b.State() != StateHalfOpen {
		t.Fatalf("expected half-open, got %s", b.State())
	}
}

func TestBreaker_HalfOpenSuccessCloses(t *testing.T) {
	b := New(Config{
		FailureThreshold: 2,
		RecoveryTimeout:  50 * time.Millisecond,
		SuccessThreshold: 2,
	})
	b.RecordFailure()
	b.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	b.Allow() // transitions to half-open
	b.RecordSuccess()
	if b.State() != StateHalfOpen {
		t.Fatalf("expected half-open after 1 success, got %s", b.State())
	}
	b.RecordSuccess()
	if b.State() != StateClosed {
		t.Fatalf("expected closed after %d successes, got %s", 2, b.State())
	}
}

func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	b := New(Config{
		FailureThreshold: 2,
		RecoveryTimeout:  50 * time.Millisecond,
		SuccessThreshold: 1,
	})
	b.RecordFailure()
	b.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	b.Allow() // transitions to half-open
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("expected open after half-open failure, got %s", b.State())
	}
}

func TestBreaker_SuccessResetsConsecutiveFails(t *testing.T) {
	b := New(Config{
		FailureThreshold: 3,
		RecoveryTimeout:  1 * time.Hour,
		SuccessThreshold: 1,
	})
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateClosed {
		t.Fatal("expected still closed")
	}
	b.RecordSuccess()
	stats := b.Stats()
	if stats.ConsecutiveFails != 0 {
		t.Fatalf("expected 0 consecutive fails after success, got %d", stats.ConsecutiveFails)
	}
}

func TestBreaker_Concurrent(t *testing.T) {
	b := New(Config{
		FailureThreshold: 100,
		RecoveryTimeout:  1 * time.Hour,
		SuccessThreshold: 1,
	})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Allow()
			b.RecordSuccess()
		}()
	}
	wg.Wait()
	if b.State() != StateClosed {
		t.Fatalf("expected closed, got %s", b.State())
	}
}

func TestBreaker_StatsCounts(t *testing.T) {
	b := New(Config{
		FailureThreshold: 2,
		RecoveryTimeout:  1 * time.Hour,
		SuccessThreshold: 1,
	})
	b.RecordSuccess()
	b.RecordFailure()
	b.RecordFailure()
	stats := b.Stats()
	if stats.TotalSuccesses != 1 {
		t.Fatalf("expected 1 success, got %d", stats.TotalSuccesses)
	}
	if stats.TotalFailures != 2 {
		t.Fatalf("expected 2 failures, got %d", stats.TotalFailures)
	}
	if stats.TotalOpens != 1 {
		t.Fatalf("expected 1 open, got %d", stats.TotalOpens)
	}
}

func TestBreaker_StateString(t *testing.T) {
	if StateClosed.String() != "closed" {
		t.Fatal("expected 'closed'")
	}
	if StateOpen.String() != "open" {
		t.Fatal("expected 'open'")
	}
	if StateHalfOpen.String() != "half_open" {
		t.Fatal("expected 'half_open'")
	}
}
