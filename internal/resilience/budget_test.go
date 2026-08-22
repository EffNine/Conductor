package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/provider"
)

// blockingOp records the ctx it received and blocks until that ctx is done,
// returning the cancellation error (simulates an in-flight provider call
// aborted by a firing deadline).
func blockingOp(captured *context.Context) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		*captured = ctx
		<-ctx.Done()
		return ctx.Err()
	}
}

func TestBudgetDisabledParity(t *testing.T) {
	calls := [3]int{}
	sink := &recordingSink{}

	for _, b := range []Budget{
		{}, // zero value
		{Enabled: false, TotalDeadline: time.Nanosecond, MaxTotalAttempts: 1, MaxEstimatedTokens: 1},
	} {
		calls = [3]int{}
		res := ExecutePlan(context.Background(), Plan{
			Budget: b,
			Candidates: []Candidate{
				{ProviderName: "a", Op: failOp(&calls[0], errors.New("e1"))},
				{ProviderName: "b", Op: failOp(&calls[1], errors.New("e2"))},
				{ProviderName: "c", Op: failOp(&calls[2], errors.New("e3"))},
			},
			Sink: sink,
		})
		if res.WinnerIndex != -1 || calls != [3]int{1, 1, 1} {
			t.Fatalf("budget %+v changed traversal: res=%+v calls=%v", b, res, calls)
		}
		if len(sink.skipped) != 0 {
			t.Fatalf("disabled budget produced skips: %v", sink.skipNames)
		}
		sink.skipped = nil
		sink.skipNames = nil
	}
}

func TestBudgetDeadlineExhaustion(t *testing.T) {
	b := breaker.New(breaker.Config{FailureThreshold: 100, RecoveryTimeout: time.Minute, SuccessThreshold: 1})
	var captured context.Context
	healthyCalls := 0
	sink := &recordingSink{}

	start := time.Now()
	res := ExecutePlan(context.Background(), Plan{
		Budget: Budget{Enabled: true, TotalDeadline: 40 * time.Millisecond},
		Candidates: []Candidate{
			{Index: 0, ProviderName: "slow", Breaker: b, Op: blockingOp(&captured)},
			{Index: 1, ProviderName: "healthy", Op: okOp(&healthyCalls)},
		},
		Sink: sink,
	})
	elapsed := time.Since(start)

	if res.WinnerIndex != -1 || !res.AttemptedAny {
		t.Fatalf("result = %+v", res)
	}
	if elapsed < 35*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("deadline not enforced: %v", elapsed)
	}
	if healthyCalls != 0 {
		t.Fatalf("post-deadline candidate was executed")
	}
	if len(sink.skipped) != 1 || sink.skipNames[0] != SkipDeadlineExceeded {
		t.Fatalf("skip reason wrong: %v", sink.skipNames)
	}
	// Budget-caused cancellation is policy, not provider health.
	if stats := b.Stats(); stats.TotalFailures != 0 {
		t.Fatalf("deadline abort penalized the provider: %+v", stats)
	}
}

func TestBudgetAttemptCap(t *testing.T) {
	primaryAttempts, fallbackCalls := 0, 0
	sink := &recordingSink{}
	serverErr := provider.NewProviderError("a", 500, provider.ErrorTypeServerError, "down", nil)

	res := ExecutePlan(context.Background(), Plan{
		Budget: Budget{Enabled: true, MaxTotalAttempts: 3},
		Candidates: []Candidate{
			{Index: 0, ProviderName: "a", Op: failOp(&primaryAttempts, serverErr)},
			{Index: 1, ProviderName: "b", Op: failOp(&fallbackCalls, errBoom)},
		},
		Retry: RetryPolicy{Enabled: true, MaxRetries: 5, InitialBackoff: 1, MaxBackoff: 1, BackoffMultiplier: 2},
		Sink:  sink,
	})

	// Primary consumes the entire global cap (1 + shrunken retries).
	if primaryAttempts != 3 {
		t.Fatalf("primary attempts = %d, want 3 (cap-shrunk)", primaryAttempts)
	}
	if fallbackCalls != 0 {
		t.Fatalf("fallback ran after attempt cap")
	}
	if len(sink.skipped) != 1 || sink.skipNames[0] != SkipAttemptBudgetExhausted {
		t.Fatalf("skip reason wrong: %v", sink.skipNames)
	}
	if res.LastError != serverErr || res.WinnerIndex != -1 {
		t.Fatalf("deterministic result violated: %+v", res)
	}
}

func TestBudgetTokenCapLimitsAmplificationOnly(t *testing.T) {
	const est = int64(100_000)
	primaryAttempts, fallbackCalls := 0, 0
	sink := &recordingSink{}

	res := ExecutePlan(context.Background(), Plan{
		Budget:          Budget{Enabled: true, MaxEstimatedTokens: 150_000},
		EstimatedTokens: est,
		Candidates: []Candidate{
			{Index: 0, ProviderName: "a", Op: failOp(&primaryAttempts, errBoom)},
			{Index: 1, ProviderName: "b", Op: okOp(&fallbackCalls)},
		},
		Retry: RetryPolicy{Enabled: true, MaxRetries: 5, InitialBackoff: 1, MaxBackoff: 1, BackoffMultiplier: 2},
		Sink:  sink,
	})

	// allowedTotal = floor(150k/100k) = 1 → primary runs once, no retries.
	if primaryAttempts != 1 {
		t.Fatalf("primary attempts = %d, want 1 (token-capped)", primaryAttempts)
	}
	if fallbackCalls != 0 {
		t.Fatalf("fallback amplified beyond token cap")
	}
	if len(sink.skipped) != 1 || sink.skipNames[0] != SkipTokenBudgetExhausted {
		t.Fatalf("skip reason wrong: %v", sink.skipNames)
	}
	if res.LastError != errBoom {
		t.Fatalf("result = %+v", res)
	}
}

func TestBudgetTokenCapNeverRejectsOriginalRequest(t *testing.T) {
	primaryAttempts := 0
	res := ExecutePlan(context.Background(), Plan{
		Budget:          Budget{Enabled: true, MaxEstimatedTokens: 100},
		EstimatedTokens: 500_000, // far above the ceiling
		Candidates: []Candidate{
			{Index: 0, ProviderName: "a", Op: okOp(&primaryAttempts)},
		},
	})
	if res.WinnerIndex != 0 || primaryAttempts != 1 {
		t.Fatalf("original request rejected by size: res=%+v attempts=%d", res, primaryAttempts)
	}
}

func TestStreamingDetachesAfterAcquisition(t *testing.T) {
	var acquiredCtx context.Context
	detached := ExecutePlan(context.Background(), Plan{
		Budget:             Budget{Enabled: true, TotalDeadline: 40 * time.Millisecond},
		DetachAfterSuccess: true,
		Candidates: []Candidate{
			{Index: 0, ProviderName: "streaming", DeferSuccess: true, Op: ctxCapturingSuccess(&acquiredCtx)},
		},
	})
	if detached.WinnerIndex != 0 {
		t.Fatalf("detached plan failed: %+v", detached)
	}
	time.Sleep(80 * time.Millisecond) // past the deadline
	if err := acquiredCtx.Err(); err != nil {
		t.Fatalf("acquired stream context was cancelled by the budget: %v", err)
	}

	// Without detach, the derived context is released on exit as before.
	var attachedCtx context.Context
	ExecutePlan(context.Background(), Plan{
		Budget: Budget{Enabled: true, TotalDeadline: 40 * time.Millisecond},
		Candidates: []Candidate{
			{Index: 0, ProviderName: "plain", DeferSuccess: false, Op: ctxCapturingSuccess(&attachedCtx)},
		},
	})
	time.Sleep(80 * time.Millisecond)
	if attachedCtx.Err() == nil {
		t.Fatalf("non-detached plan leaked its deadline context")
	}
}

func ctxCapturingSuccess(captured *context.Context) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		*captured = ctx
		return nil
	}
}

var _ = provider.NewProviderError // anchor if cases evolve
