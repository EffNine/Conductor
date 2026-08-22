package breaker

import (
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/failure"
)

func newTestBreaker(threshold int) *Breaker {
	return New(Config{
		FailureThreshold: threshold,
		RecoveryTimeout:  50 * time.Millisecond,
		SuccessThreshold: 2,
	})
}

func TestRateLimitNeverTripsBreaker(t *testing.T) {
	b := newTestBreaker(3)
	for i := 0; i < 20; i++ {
		b.RecordOutcome(failure.ClassRateLimited)
	}
	if b.State() != StateClosed {
		t.Fatalf("state = %v, want closed after 429s", b.State())
	}
	stats := b.Stats()
	if stats.TotalThrottles != 20 {
		t.Fatalf("TotalThrottles = %d, want 20", stats.TotalThrottles)
	}
	if stats.TotalFailures != 0 {
		t.Fatalf("TotalFailures = %d, want 0", stats.TotalFailures)
	}
}

func TestServerErrorStillCountsTowardOpen(t *testing.T) {
	b := newTestBreaker(3)
	for i := 0; i < 2; i++ {
		b.RecordOutcome(failure.ClassUpstreamError)
	}
	if b.State() != StateClosed {
		t.Fatalf("opened before threshold")
	}
	b.RecordOutcome(failure.ClassCapacity) // capacity counts too
	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open at threshold", b.State())
	}
}

func TestAuthAndInvalidRequestAreIgnored(t *testing.T) {
	b := newTestBreaker(2)
	for i := 0; i < 10; i++ {
		b.RecordOutcome(failure.ClassAuthFailed)
		b.RecordOutcome(failure.ClassInvalidRequest)
	}
	stats := b.Stats()
	if b.State() != StateClosed || stats.TotalFailures != 0 || stats.TotalThrottles != 0 {
		t.Fatalf("client-side faults leaked into breaker accounting: %+v", stats)
	}
}

func TestCountingClassesAllConsumeFailureBudget(t *testing.T) {
	counting := []failure.Class{
		failure.ClassTimeout,
		failure.ClassNetworkError,
		failure.ClassUnknown,
		failure.ClassUpstreamError,
		failure.ClassCapacity,
	}
	b := newTestBreaker(len(counting))
	for _, c := range counting {
		b.RecordOutcome(c)
	}
	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open after %d distinct counting failures", b.State(), len(counting))
	}
}

func TestThrottleDoesNotResetFailureStreak(t *testing.T) {
	b := newTestBreaker(3)
	b.RecordOutcome(failure.ClassTimeout)
	b.RecordOutcome(failure.ClassTimeout)
	if b.State() != StateClosed {
		t.Fatalf("opened below threshold")
	}
	b.RecordOutcome(failure.ClassRateLimited) // neutral: neither counts nor heals
	if b.State() != StateClosed {
		t.Fatalf("throttle must be health-neutral")
	}
	b.RecordOutcome(failure.ClassTimeout) // third real failure
	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open on third real failure", b.State())
	}
	stats := b.Stats()
	if stats.TotalThrottles != 1 || stats.TotalFailures != 3 {
		t.Fatalf("counters wrong: %+v", stats)
	}
}

func TestHalfOpenThrottleGrantsNoRecoveryCredit(t *testing.T) {
	b := newTestBreaker(1)
	b.RecordOutcome(failure.ClassUpstreamError) // open
	time.Sleep(60 * time.Millisecond)           // past recovery timeout

	if got := b.Allow(); got != ResultAllowed {
		t.Fatalf("expected half-open probe allowed, got %v", got)
	}
	if b.State() != StateHalfOpen {
		t.Fatalf("state = %v, want half_open", b.State())
	}
	b.RecordOutcome(failure.ClassRateLimited) // throttled probe
	if b.State() != StateHalfOpen {
		t.Fatalf("throttle must not close or reopen; state = %v", b.State())
	}

	b.RecordSuccess()
	b.RecordSuccess()
	if b.State() != StateClosed {
		t.Fatalf("successes in half-open must close; state = %v", b.State())
	}
}

func TestRecordSuccessResetsStreak(t *testing.T) {
	b := newTestBreaker(2)
	b.RecordOutcome(failure.ClassUpstreamError)
	b.RecordSuccess()
	b.RecordOutcome(failure.ClassUpstreamError)
	if b.State() != StateClosed {
		t.Fatalf("success should have reset the streak; state = %v", b.State())
	}
}

func TestLegacyRecordFailureStillWorks(t *testing.T) {
	b := newTestBreaker(1)
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("legacy RecordFailure must still open; state = %v", b.State())
	}
}
