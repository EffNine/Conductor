package resilience

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/failure"
)

// Candidate is one execution concern in a fallback plan. It carries only
// what the executor needs to traverse, gate, and report — never routing
// scores, capability metadata, or router state.
type Candidate struct {
	// Index is the candidate's position in the plan; 0 is the primary.
	Index int

	ProviderName    string
	ModelID         string // user-facing/virtual model for observability
	ProviderModelID string // upstream slug

	// Breaker gates execution and receives outcome accounting. Nil disables
	// gating and accounting for this candidate.
	Breaker *breaker.Breaker

	// DeferSuccess marks streaming-style operations whose success credit is
	// granted later (on stream completion) rather than at acquisition. The
	// executor will still notify the sink and classify failures normally.
	DeferSuccess bool

	// Op performs ONE execution attempt of this candidate. Results travel
	// via the closure that created the candidate; the executor consumes
	// only the error.
	Op func(ctx context.Context) error
}

// Plan is an ordered candidate chain with its retry configuration and
// optional execution budgets. The executor deep-copies the candidate slice;
// callers may retain theirs.
type Plan struct {
	Candidates []Candidate
	Retry      RetryPolicy

	// Sink receives lifecycle notifications for observability. May be nil.
	Sink Sink

	// Budget bounds the chain (P4.4.2). Zero value = disabled: behaviour
	// matches the pre-budget executor exactly.
	Budget Budget

	// EstimatedTokens is the per-attempt prompt-token estimate consumed by
	// the token guard. Computed once per request by the caller. 0 disables
	// the guard even when MaxEstimatedTokens is set.
	EstimatedTokens int64

	// DetachAfterSuccess marks streaming plans: once a candidate wins, the
	// deadline timer is disarmed and the derived context is intentionally
	// left un-cancelled so the acquired stream keeps running under its own
	// lifecycle (idle timeout, client disconnect, server shutdown).
	DetachAfterSuccess bool
}

// Sink observes plan execution. Implementations must be cheap and
// non-blocking; they never influence traversal decisions.
type Sink interface {
	// CandidateSkipped reports a candidate that was never attempted.
	CandidateSkipped(c Candidate, reason SkipReason)
	// CandidateFailed reports a candidate whose attempts were exhausted.
	CandidateFailed(c Candidate, err error, attempts []Attempt, duration time.Duration)
	// CandidateSucceeded reports a winning candidate. For DeferSuccess
	// candidates the actual breaker credit lands later, handler-side.
	CandidateSucceeded(c Candidate, attempts []Attempt, duration time.Duration)
}

// Result summarizes a plan execution. It lets the caller know which
// candidate won, whether anything was attempted, whether the first
// candidate was skipped, and the final error.
type Result struct {
	WinnerIndex  int   // -1 when no candidate succeeded
	AttemptedAny bool  // at least one candidate was executed (not merely gated)
	FirstBlocked bool  // the primary (index 0) was skipped by its breaker
	LastError    error // last genuine provider error; nil when nothing failed
}

// ExecutePlan traverses candidates in configured order, gates each on its
// breaker, runs the P4.2 retry engine per candidate, applies P4.3 outcome
// accounting, enforces the plan's budget (P4.4.2), and stops at the first
// success.
//
// Behavioural contract (must match the pre-P4.4.1 handler loops):
//   - candidates are attempted strictly in slice order
//   - open-breaker candidates are skipped without execution
//   - each candidate gets the full retry budget from Plan.Retry, shrunk
//     only by remaining global budget (P4.4.2)
//   - failures are classified via failure.Classify for breaker impact;
//     budget-deadline cancellations are policy decisions and are exempted
//   - DeferSuccess candidates do not receive immediate success credit
func ExecutePlan(ctx context.Context, p Plan) Result {
	res := Result{WinnerIndex: -1}

	candidates := make([]Candidate, len(p.Candidates))
	copy(candidates, p.Candidates)

	// P4.4.2: request-scoped execution deadline. The timer cancels the
	// derived context when it fires, aborting in-flight attempts.
	var deadlineFired atomic.Bool
	if p.Budget.TotalDeadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		timer := time.AfterFunc(p.Budget.TotalDeadline, func() {
			deadlineFired.Store(true)
			cancel()
		})
		defer func() {
			timer.Stop()
			// A detached streaming success keeps the derived context alive:
			// the provider goroutine behind the acquired channel still uses
			// it. Every other exit path releases the context immediately.
			if !(p.DetachAfterSuccess && res.WinnerIndex != -1) {
				cancel()
			}
		}()
	}

	totalAttempts := 0

	for i := range candidates {
		c := candidates[i]
		c.Index = i

		// P4.3 breaker eligibility gate.
		if c.Breaker != nil && c.Breaker.Allow() != breaker.ResultAllowed {
			if c.Index == 0 {
				res.FirstBlocked = true
			}
			if p.Sink != nil {
				p.Sink.CandidateSkipped(c, SkipCircuitBreakerOpen)
			}
			continue
		}

		// P4.4.2 budget gates.
		if skip := budgetSkip(p.Budget, ctx, &deadlineFired, p.EstimatedTokens, totalAttempts, c.Index); skip != "" {
			if p.Sink != nil {
				p.Sink.CandidateSkipped(c, skip)
			}
			continue
		}

		start := time.Now()
		effRetry := effectiveRetry(p.Retry, p.Budget, p.EstimatedTokens, totalAttempts)
		var attempts []Attempt
		var err error
		_, attempts, err = Execute[struct{}](ctx, effRetry, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, c.Op(ctx)
		})
		duration := time.Since(start)
		totalAttempts += len(attempts)
		res.AttemptedAny = true

		if err == nil {
			if !c.DeferSuccess && c.Breaker != nil {
				c.Breaker.RecordSuccess()
			}
			res.WinnerIndex = c.Index
			if p.Sink != nil {
				p.Sink.CandidateSucceeded(c, attempts, duration)
			}
			return res
		}

		// A budget-deadline firing is our policy decision, not provider
		// health: never record it against the breaker. All other failures
		// classify exactly as in P4.3.
		if c.Breaker != nil && !deadlineFired.Load() {
			class, _ := failure.Classify(err)
			c.Breaker.RecordOutcome(class)
		}
		res.LastError = err
		if p.Sink != nil {
			p.Sink.CandidateFailed(c, err, attempts, duration)
		}
	}

	return res
}

// budgetSkip evaluates the pre-execution gates for one candidate.
func budgetSkip(b Budget, ctx context.Context, fired *atomic.Bool, estimatedTokens int64, totalAttempts, index int) SkipReason {
	if !b.enabled() {
		return ""
	}
	if b.TotalDeadline > 0 && fired.Load() {
		return SkipDeadlineExceeded
	}
	if b.MaxTotalAttempts > 0 && totalAttempts >= b.MaxTotalAttempts {
		return SkipAttemptBudgetExhausted
	}
	// Token guard limits only ADDITIONAL attempts: the primary always runs
	// at least once regardless of size.
	if index > 0 && totalAttempts > 0 && b.MaxEstimatedTokens > 0 && estimatedTokens > 0 &&
		int64(totalAttempts+1)*estimatedTokens > b.MaxEstimatedTokens {
		return SkipTokenBudgetExhausted
	}
	return ""
}

// effectiveRetry shrinks a candidate's retry budget so the chain respects
// the global attempt cap and the token amplification ceiling. The P4.2 math
// is preserved: only MaxRetries is reduced, never below zero, and the first
// attempt of any gated candidate is always permitted.
func effectiveRetry(base RetryPolicy, b Budget, estimatedTokens int64, totalAttempts int) RetryPolicy {
	eff := base
	if !b.enabled() {
		return eff
	}
	if b.MaxTotalAttempts > 0 {
		// The gate guarantees totalAttempts < MaxTotalAttempts here, so this
		// candidate always gets at least one attempt.
		maxRetries := (b.MaxTotalAttempts - totalAttempts) - 1
		if eff.MaxRetries > maxRetries {
			eff.MaxRetries = maxRetries
		}
		if eff.MaxRetries < 0 {
			eff.MaxRetries = 0
		}
	}
	if b.MaxEstimatedTokens > 0 && estimatedTokens > 0 && totalAttempts > 0 {
		headroom := int(b.MaxEstimatedTokens/estimatedTokens) - totalAttempts - 1
		if eff.MaxRetries > headroom {
			eff.MaxRetries = headroom
		}
		if eff.MaxRetries < 0 {
			eff.MaxRetries = 0
		}
	}
	return eff
}
