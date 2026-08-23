package resilience

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/failure"
	"github.com/EffNine/conductor/internal/provider"
)

// recordingSink captures lifecycle notifications for assertions.
type recordingSink struct {
	skipped   []Candidate
	skipNames []SkipReason
	failed    []Candidate
	failErrs  []error
	succeeded []Candidate
}

func (r *recordingSink) CandidateSkipped(c Candidate, reason SkipReason) {
	r.skipped = append(r.skipped, c)
	r.skipNames = append(r.skipNames, reason)
}
func (r *recordingSink) CandidateFailed(c Candidate, err error, attempts []Attempt, d time.Duration) {
	r.failed = append(r.failed, c)
	r.failErrs = append(r.failErrs, err)
}
func (r *recordingSink) CandidateSucceeded(c Candidate, attempts []Attempt, d time.Duration) {
	r.succeeded = append(r.succeeded, c)
}
func (r *recordingSink) AttemptExecuted(c Candidate, a Attempt) {}

func okOp(counter *int) func(ctx context.Context) error {
	return func(ctx context.Context) error { *counter++; return nil }
}

func failOp(counter *int, err error) func(ctx context.Context) error {
	return func(ctx context.Context) error { *counter++; return err }
}

var errBoom = errors.New("boom")

func TestExecutePlanFirstCandidateSucceeds(t *testing.T) {
	calls := 0
	c1 := 0
	sink := &recordingSink{}
	p := Plan{
		Candidates: []Candidate{
			{Index: 0, ProviderName: "a", Op: okOp(&calls)},
			{ProviderName: "b", Op: failOp(&c1, errBoom)},
		},
		Retry: RetryPolicy{},
		Sink:  sink,
	}
	res := ExecutePlan(context.Background(), p)

	if res.WinnerIndex != 0 || !res.AttemptedAny || res.LastError != nil {
		t.Fatalf("result = %+v", res)
	}
	if calls != 1 || c1 != 0 {
		t.Fatalf("second candidate must not run: a=%d b=%d", calls, c1)
	}
	if len(sink.succeeded) != 1 || len(sink.failed) != 0 {
		t.Fatalf("sink = %+v", sink)
	}
}

func TestExecutePlanSecondCandidateSucceeds(t *testing.T) {
	aCalls, bCalls := 0, 0
	sink := &recordingSink{}
	res := ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{
			{Index: 0, ProviderName: "a", Op: failOp(&aCalls, errBoom)},
			{Index: 1, ProviderName: "b", Op: okOp(&bCalls)},
		},
		Sink: sink,
	})

	if res.WinnerIndex != 1 || res.LastError != errBoom {
		t.Fatalf("result = %+v", res)
	}
	if aCalls != 1 || bCalls != 1 {
		t.Fatalf("order violated: a=%d b=%d", aCalls, bCalls)
	}
	if len(sink.failed) != 1 || len(sink.succeeded) != 1 {
		t.Fatalf("sink notifications wrong: %+v", sink)
	}
}

func TestExecutePlanAllCandidatesFail(t *testing.T) {
	calls := [3]int{}
	res := ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{
			{Index: 0, ProviderName: "a", Op: failOp(&calls[0], errors.New("e1"))},
			{Index: 1, ProviderName: "b", Op: failOp(&calls[1], errors.New("e2"))},
			{Index: 2, ProviderName: "c", Op: failOp(&calls[2], errBoom)},
		},
	})
	if res.WinnerIndex != -1 || res.LastError != errBoom {
		t.Fatalf("result = %+v", res)
	}
	if !res.AttemptedAny || res.FirstBlocked {
		t.Fatalf("attempted/first flags wrong: %+v", res)
	}
	if calls != [3]int{1, 1, 1} {
		t.Fatalf("all candidates must attempt once each: %v", calls)
	}
}

func TestExecutePlanBreakerOpenPrimarySkips(t *testing.T) {
	b := breaker.New(breaker.Config{FailureThreshold: 1, RecoveryTimeout: time.Minute, SuccessThreshold: 1})
	b.RecordFailure() // open
	calls := 0
	sink := &recordingSink{}
	res := ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{
			{Index: 0, ProviderName: "blocked", Breaker: b, Op: okOp(&calls)},
		},
		Sink: sink,
	})

	if !res.FirstBlocked || res.AttemptedAny || res.WinnerIndex != -1 || res.LastError != nil {
		t.Fatalf("result = %+v", res)
	}
	if calls != 0 {
		t.Fatalf("blocked candidate was executed")
	}
	if len(sink.skipped) != 1 || sink.skipNames[0] != SkipCircuitBreakerOpen {
		t.Fatalf("skip notification wrong: %+v", sink)
	}
}

func TestExecutePlanOpenPrimaryFallsToHealthySecond(t *testing.T) {
	b := breaker.New(breaker.Config{FailureThreshold: 1, RecoveryTimeout: time.Minute, SuccessThreshold: 1})
	b.RecordFailure()
	calls := 0
	res := ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{
			{Index: 0, ProviderName: "blocked", Breaker: b, Op: okOp(&calls)},
			{Index: 1, ProviderName: "healthy", Op: okOp(&calls)},
		},
	})
	if res.WinnerIndex != 1 || !res.FirstBlocked || !res.AttemptedAny {
		t.Fatalf("result = %+v", res)
	}
	if calls != 1 { // only the healthy candidate executed
		t.Fatalf("calls = %d, want exactly the fallback execution", calls)
	}
}

func TestExecutePlanPreservesOrdering(t *testing.T) {
	var order []string
	mk := func(name string) Candidate {
		return Candidate{
			Index:        len(order),
			ProviderName: name,
			Op: func(ctx context.Context) error {
				order = append(order, name)
				return errBoom
			},
		}
	}
	ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{mk("first"), mk("second"), mk("third")},
	})
	want := []string{"first", "second", "third"}
	for i, n := range want {
		if order[i] != n {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestExecutePlanRunsRetryEnginePerCandidate(t *testing.T) {
	attempts := 0
	rateLimited := provider.NewProviderError("p", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil)
	flaky := func(ctx context.Context) error {
		attempts++
		if attempts == 1 {
			return rateLimited
		}
		return nil
	}
	b := breaker.New(breaker.Config{FailureThreshold: 100, RecoveryTimeout: time.Minute, SuccessThreshold: 1})
	policy := RetryPolicy{Enabled: true, MaxRetries: 2, InitialBackoff: 1, MaxBackoff: 1, BackoffMultiplier: 2, HonorRetryAfter: true}

	res := ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{{Index: 0, ProviderName: "flaky", Breaker: b, Op: flaky}},
		Retry:      policy,
	})
	if res.WinnerIndex != 0 {
		t.Fatalf("flaky candidate should win after retry; result = %+v", res)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (one retry)", attempts)
	}
	// Preserved P4.2/P4.3 semantic: ONE logical outcome per candidate.
	// Intermediate retry failures never touch the breaker — the eventual
	// success is the candidate's only accounting event.
	stats := b.Stats()
	if stats.TotalThrottles != 0 || stats.TotalSuccesses != 1 || stats.TotalFailures != 0 {
		t.Fatalf("breaker accounting wrong: %+v", stats)
	}
}

func TestExecutePlanClassifiesFailureForBreaker(t *testing.T) {
	b := breaker.New(breaker.Config{FailureThreshold: 2, RecoveryTimeout: 1, SuccessThreshold: 1})
	serverErr := provider.NewProviderError("p", 500, provider.ErrorTypeServerError, "boom", nil)
	authErr := provider.NewProviderError("p", 401, provider.ErrorTypeAuthentication, "bad key", nil)

	ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{{Index: 0, ProviderName: "p", Breaker: b, Op: failOp(new(int), serverErr)}},
	})
	if got := b.Stats().TotalFailures; got != 1 {
		t.Fatalf("server error must count as failure, got %d", got)
	}

	b2 := breaker.New(breaker.Config{FailureThreshold: 2, RecoveryTimeout: 1, SuccessThreshold: 1})
	ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{{Index: 0, ProviderName: "p", Breaker: b2, Op: failOp(new(int), authErr)}},
	})
	stats := b2.Stats()
	if stats.TotalFailures != 0 || stats.TotalThrottles != 0 {
		t.Fatalf("auth failures must be fully ignored: %+v", stats)
	}
}

func TestExecutePlanDeferSuccessGrantsNoImmediateCredit(t *testing.T) {
	b := breaker.New(breaker.Config{FailureThreshold: 1, RecoveryTimeout: 1, SuccessThreshold: 1})
	res := ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{{Index: 0, ProviderName: "streaming", Breaker: b, DeferSuccess: true, Op: okOp(new(int))}},
	})
	if res.WinnerIndex != 0 {
		t.Fatalf("winner expected")
	}
	stats := b.Stats()
	if stats.TotalSuccesses != 0 {
		t.Fatalf("deferred success granted immediate credit: %+v", stats)
	}
}

func TestExecutePlanNilSinkAndNilBreakersSafe(t *testing.T) {
	res := ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{{ProviderName: "bare", Op: okOp(new(int))}},
	})
	if res.WinnerIndex != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestCandidateIndexesAreAssignedByExecutor(t *testing.T) {
	sink := &recordingSink{}
	ExecutePlan(context.Background(), Plan{
		Candidates: []Candidate{
			{ProviderName: "a", Op: failOp(new(int), errBoom)},
			{ProviderName: "b", Op: failOp(new(int), errBoom)},
		},
		Sink: sink,
	})
	for i, c := range [][]Candidate{sink.failed} {
		for j, cand := range c {
			_ = i
			if cand.Index != j {
				t.Fatalf("candidate index = %d, want %d", cand.Index, j)
			}
		}
	}
}

var _ = failure.ClassUnknown // keep import anchored if cases evolve
