// Package resilience hosts request-path reliability policies for Conductor.
//
// P4.2 provides the cause-aware same-provider retry engine. Retry decisions
// are driven entirely by the canonical failure taxonomy: an error is retried
// only when its failure.Class is marked retryable by failure.PolicyFor, and
// a server-advised wait (ProviderError.RetryAfter) overrides computed
// backoff when honored.
//
// The engine never touches breaker accounting, routing scores, or fallback
// ordering; those remain P4.3/P4.4 concerns.
package resilience

import (
	"context"
	"math"
	"math/rand"
	"time"

	"github.com/EffNine/conductor/internal/failure"
)

// Reasons attached to every Decision for observability.
const (
	ReasonDisabled          = "disabled"            // policy zero value or explicitly off
	ReasonMaxRetries        = "max_retries"         // attempt budget exhausted
	ReasonClassNotRetryable = "class_not_retryable" // failure class policy forbids retry
	ReasonBackoff           = "backoff"             // exponential backoff with jitter
	ReasonRetryAfterHint    = "retry_after_hint"    // honoring ProviderError.RetryAfter
)

// defaultMaxRetryAfterWait caps server-advised waits when the policy does
// not set one.
const defaultMaxRetryAfterWait = 30 * time.Second

// RetryPolicy configures same-provider retries.
//
// The zero value is DISABLED and performs no retries: code constructed
// without explicit configuration keeps legacy behavior exactly.
type RetryPolicy struct {
	Enabled           bool          // master switch; false means never retry
	MaxRetries        int           // additional attempts beyond the first
	InitialBackoff    time.Duration // base delay before the first retry; <=0 -> default
	MaxBackoff        time.Duration // ceiling for computed backoff; <=0 -> default
	BackoffMultiplier float64       // exponential factor per retry; <1 treated as 2
	HonorRetryAfter   bool          // let ProviderError.RetryAfter override backoff
	MaxRetryAfterWait time.Duration // ceiling for honored hints; <=0 -> default
}

// DefaultRetryPolicy returns conservative production defaults: a single
// same-provider retry with bounded exponential backoff and Retry-After
// compliance.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		Enabled:           true,
		MaxRetries:        1,
		InitialBackoff:    250 * time.Millisecond,
		MaxBackoff:        2 * time.Second,
		BackoffMultiplier: 2.0,
		HonorRetryAfter:   true,
		MaxRetryAfterWait: 30 * time.Second,
	}
}

func (p RetryPolicy) initialBackoff() time.Duration {
	if p.InitialBackoff > 0 {
		return p.InitialBackoff
	}
	return 250 * time.Millisecond
}

func (p RetryPolicy) maxBackoff() time.Duration {
	if p.MaxBackoff > 0 {
		return p.MaxBackoff
	}
	return 2 * time.Second
}

func (p RetryPolicy) multiplier() float64 {
	if p.BackoffMultiplier >= 1 {
		return p.BackoffMultiplier
	}
	return 2.0
}

func (p RetryPolicy) maxRetryAfterWait() time.Duration {
	if p.MaxRetryAfterWait > 0 {
		return p.MaxRetryAfterWait
	}
	return defaultMaxRetryAfterWait
}

// Decision is the verdict for one failed attempt, carrying the reason and
// the canonical failure class for structured logging.
type Decision struct {
	ShouldRetry bool
	Wait        time.Duration
	Reason      string
	Class       failure.Class
}

// Decide evaluates whether err should be retried after completedAttempts
// attempts have already been made (the failed attempt included).
//
// Verdict precedence: disabled → attempt budget exhausted → class not
// retryable (auth_failed, invalid_request, unknown are never retried) →
// retry with computed or server-advised wait.
func (p RetryPolicy) Decide(err error, completedAttempts int) Decision {
	if !p.Enabled {
		return Decision{Reason: ReasonDisabled}
	}
	if completedAttempts > p.MaxRetries {
		return Decision{Reason: ReasonMaxRetries}
	}
	class, hint := failure.Classify(err)
	if !failure.PolicyFor(class).Retryable {
		return Decision{Class: class, Reason: ReasonClassNotRetryable}
	}

	wait := applyJitter(baseBackoff(p, completedAttempts))
	reason := ReasonBackoff
	if p.HonorRetryAfter && hint > 0 {
		capped := hint
		if capped > p.maxRetryAfterWait() {
			capped = p.maxRetryAfterWait()
		}
		wait = capped
		reason = ReasonRetryAfterHint
	}
	return Decision{ShouldRetry: true, Wait: wait, Reason: reason, Class: class}
}

// baseBackoff computes the deterministic exponential wait before retry
// number n (1-based). It never applies jitter.
func baseBackoff(p RetryPolicy, n int) time.Duration {
	if n < 1 {
		n = 1
	}
	b := float64(p.initialBackoff()) * math.Pow(p.multiplier(), float64(n-1))
	if ceiling := float64(p.maxBackoff()); b > ceiling {
		b = ceiling
	}
	return time.Duration(b)
}

// applyJitter widens a wait by ±25% to avoid synchronized retry storms.
func applyJitter(base time.Duration) time.Duration {
	f := float64(base) + (rand.Float64()*2-1)*0.25*float64(base)
	if f < 0 {
		f = 0
	}
	return time.Duration(f)
}

// Attempt records one execution of an operation for observability.
type Attempt struct {
	Index        int // 0-based
	Duration     time.Duration
	FailureClass string        // "" when the attempt succeeded
	RetryWait    time.Duration // wait performed before this attempt; 0 on first
	HintHonored  bool          // RetryWait came from a Retry-After hint
}

// Execute runs fn up to 1+p.MaxRetries times while failures classify as
// retryable. On success it returns the result with the attempt records; on
// final failure it returns the last error untouched so downstream
// classification and error mapping stay truthful. Context cancellation
// during a backoff aborts immediately, returning the last provider error.
func Execute[T any](ctx context.Context, p RetryPolicy, fn func(context.Context) (T, error)) (T, []Attempt, error) {
	var zero T
	attempts := make([]Attempt, 0, p.MaxRetries+1)
	pendingWait := time.Duration(0)
	hintHonored := false

	for i := 0; ; i++ {
		start := time.Now()
		res, err := fn(ctx)
		rec := Attempt{
			Index:       i,
			Duration:    time.Since(start),
			RetryWait:   pendingWait,
			HintHonored: hintHonored,
		}
		if err == nil {
			attempts = append(attempts, rec)
			return res, attempts, nil
		}
		class, _ := failure.Classify(err)
		rec.FailureClass = string(class)
		attempts = append(attempts, rec)

		d := p.Decide(err, i+1)
		if !d.ShouldRetry {
			return zero, attempts, err
		}
		if werr := sleepCtx(ctx, d.Wait); werr != nil {
			return zero, attempts, err
		}
		pendingWait = d.Wait
		hintHonored = d.Reason == ReasonRetryAfterHint
	}
}

// sleepCtx waits for d, aborting early when ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
