package resilience

import "time"

// SkipReason explains why a candidate was never attempted.
type SkipReason string

const ( //nolint:gosec // G101 false positive: these are observability labels, not credentials.
	// SkipCircuitBreakerOpen: the candidate's breaker refused permission.
	SkipCircuitBreakerOpen SkipReason = "circuit_breaker_open"
	// SkipDeadlineExceeded: the plan's total execution deadline elapsed.
	SkipDeadlineExceeded SkipReason = "deadline_exceeded"
	// SkipAttemptBudgetExhausted: the global attempt cap was consumed.
	SkipAttemptBudgetExhausted SkipReason = "attempt_budget_exhausted"
	// SkipTokenBudgetExhausted: another attempt would exceed the token
	// amplification ceiling. The original request is never rejected for
	// size — only additional attempts are limited.
	//
	// #nosec G101 -- observability label, not a credential.
	SkipTokenBudgetExhausted SkipReason = "token_budget_exhausted"
)

// Budget bounds a candidate chain. It is request-scoped and enforced inside
// ExecutePlan; it never influences router decisions.
//
// The zero value is disabled: behaviour matches the pre-budget executor
// exactly (P4.4.1 parity).
type Budget struct {
	Enabled bool

	// TotalDeadline bounds wall-clock time for the whole chain. For
	// streaming plans whose owner sets DetachAfterSuccess, the timer is
	// disarmed once a stream is acquired so live streams are never killed
	// by the budget; until then every acquisition attempt is bounded.
	TotalDeadline time.Duration

	// MaxTotalAttempts caps executions across ALL candidates, retries
	// included. 0 = unlimited.
	MaxTotalAttempts int

	// MaxEstimatedTokens caps retry/fallback amplification measured in
	// estimated prompt tokens per attempt. The primary request always runs
	// at least once regardless of size. 0 = disabled.
	MaxEstimatedTokens int64
}

// enabled reports whether any dimension of the budget is active.
func (b Budget) enabled() bool {
	return b.Enabled && (b.TotalDeadline > 0 || b.MaxTotalAttempts > 0 || b.MaxEstimatedTokens > 0)
}
