package resilience

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/failure"
	"github.com/EffNine/conductor/internal/provider"
)

// fastPolicy returns an enabled policy with tiny waits so Execute-based
// tests stay fast.
func fastPolicy(maxRetries int) RetryPolicy {
	return RetryPolicy{
		Enabled:           true,
		MaxRetries:        maxRetries,
		InitialBackoff:    time.Millisecond,
		MaxBackoff:        5 * time.Millisecond,
		BackoffMultiplier: 2.0,
		HonorRetryAfter:   true,
		MaxRetryAfterWait: 20 * time.Millisecond,
	}
}

func classFixture(t *testing.T, class failure.Class) error {
	t.Helper()
	switch class {
	case failure.ClassRateLimited:
		return provider.NewProviderError("p", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil)
	case failure.ClassTimeout:
		return provider.NewProviderError("p", http.StatusGatewayTimeout, provider.ErrorTypeProviderUnavailable, "gateway timeout", nil)
	case failure.ClassCapacity:
		return provider.NewProviderError("p", http.StatusServiceUnavailable, provider.ErrorTypeServerError, "full", nil)
	case failure.ClassUpstreamError:
		return provider.NewProviderError("p", http.StatusInternalServerError, provider.ErrorTypeServerError, "boom", nil)
	case failure.ClassNetworkError:
		return provider.NewProviderError("p", http.StatusBadGateway, provider.ErrorTypeProviderUnavailable, "refused", errors.New("connection refused"))
	case failure.ClassAuthFailed:
		return provider.NewProviderError("p", http.StatusUnauthorized, provider.ErrorTypeAuthentication, "bad key", nil)
	case failure.ClassInvalidRequest:
		return provider.NewProviderError("p", http.StatusBadRequest, provider.ErrorTypeInvalidRequest, "bad body", nil)
	default:
		return errors.New("mystery")
	}
}

func TestZeroValuePolicyDisablesRetries(t *testing.T) {
	var p RetryPolicy // zero value must be disabled for legacy-behavior safety

	d := p.Decide(errors.New("anything"), 1)
	if d.ShouldRetry || d.Reason != ReasonDisabled {
		t.Fatalf("zero-value Decide = %+v, want disabled", d)
	}

	calls := 0
	_, _, err := Execute(context.Background(), p, func(ctx context.Context) (int, error) {
		calls++
		return 0, context.DeadlineExceeded
	})
	if calls != 1 {
		t.Fatalf("zero-value policy made %d calls, want exactly 1", calls)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want original deadline error", err)
	}
}

func TestRetryMatrixByClass(t *testing.T) {
	retryable := map[failure.Class]bool{
		failure.ClassRateLimited:    true,
		failure.ClassTimeout:        true,
		failure.ClassCapacity:       true,
		failure.ClassUpstreamError:  true,
		failure.ClassNetworkError:   true,
		failure.ClassAuthFailed:     false,
		failure.ClassInvalidRequest: false,
		failure.ClassUnknown:        false,
	}

	for class, want := range retryable {
		t.Run(string(class), func(t *testing.T) {
			p := fastPolicy(3)
			err := classFixture(t, class)
			d := p.Decide(err, 1)

			if d.ShouldRetry != want {
				t.Fatalf("class %q: ShouldRetry = %v, want %v (reason=%s)", class, d.ShouldRetry, want, d.Reason)
			}
			if got := d.Class; string(got) != string(class) {
				t.Fatalf("fixture classified as %q, want %q", got, class)
			}
			if !want && d.Reason != ReasonClassNotRetryable {
				t.Fatalf("non-retryable reason = %q, want %q", d.Reason, ReasonClassNotRetryable)
			}
		})
	}
}

func TestDecideHonorsRetryAfterHintExactly(t *testing.T) {
	p := fastPolicy(3)
	err := provider.NewProviderError("p", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil).
		WithRetryAfter(7 * time.Millisecond)

	d := p.Decide(err, 1)
	if !d.ShouldRetry || d.Reason != ReasonRetryAfterHint {
		t.Fatalf("Decide = %+v, want retry via retry_after_hint", d)
	}
	if d.Wait != 7*time.Millisecond { // hint path applies no jitter
		t.Fatalf("Wait = %v, want exact hint 7ms", d.Wait)
	}
}

func TestDecideCapsRetryAfterHint(t *testing.T) {
	p := fastPolicy(3)
	err := provider.NewProviderError("p", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil).
		WithRetryAfter(time.Hour)

	d := p.Decide(err, 1)
	if d.Wait != 20*time.Millisecond { // capped by MaxRetryAfterWait
		t.Fatalf("Wait = %v, want cap of 20ms", d.Wait)
	}
}

func TestDecideIgnoresHintWhenNotHonored(t *testing.T) {
	p := fastPolicy(1)
	p.HonorRetryAfter = false
	err := provider.NewProviderError("p", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil).
		WithRetryAfter(500 * time.Millisecond)

	d := p.Decide(err, 1)
	if !d.ShouldRetry || d.Reason != ReasonBackoff {
		t.Fatalf("Decide = %+v, want backoff path", d)
	}
	if d.Wait >= 500*time.Millisecond {
		t.Fatalf("hint leaked past HonorRetryAfter=false: wait=%v", d.Wait)
	}
}

func TestBaseBackoffExponentialGrowthAndCap(t *testing.T) {
	p := DefaultRetryPolicy()
	want := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second, 2 * time.Second, 2 * time.Second}
	for n, w := range want {
		if got := baseBackoff(p, n+1); got != w {
			t.Fatalf("baseBackoff(n=%d) = %v, want %v", n+1, got, w)
		}
	}
}

func TestApplyJitterStaysWithinBounds(t *testing.T) {
	base := 100 * time.Millisecond
	for i := 0; i < 200; i++ {
		got := applyJitter(base)
		if got < 75*time.Millisecond || got > 125*time.Millisecond {
			t.Fatalf("jitter out of ±25%% bounds: %v", got)
		}
	}
}

func TestExecuteStopsAtMaxAttemptsAndReturnsLastErrorUntouched(t *testing.T) {
	p := fastPolicy(2)
	calls := 0
	provErr := provider.NewProviderError("p", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil)

	res, attempts, err := Execute(context.Background(), p, func(ctx context.Context) (string, error) {
		calls++
		return "", provErr
	})

	if res != "" || err == nil {
		t.Fatalf("expected empty result and error, got res=%q err=%v", res, err)
	}
	if calls != 3 || len(attempts) != 3 { // 1 + MaxRetries
		t.Fatalf("calls=%d attempts=%d, want 3/3", calls, len(attempts))
	}
	var pe *provider.ProviderError
	if !errors.As(err, &pe) || pe != provErr {
		t.Fatalf("final error was wrapped or replaced: %v", err)
	}
	for i, a := range attempts[:len(attempts)-1] {
		if a.FailureClass != string(failure.ClassRateLimited) {
			t.Fatalf("attempt %d class = %q, want rate_limited", i, a.FailureClass)
		}
	}
	if attempts[0].Index != 0 || attempts[2].Index != 2 {
		t.Fatalf("attempt indices wrong: %+v", attempts)
	}
	if attempts[1].RetryWait <= 0 {
		t.Fatalf("second attempt should record its preceding wait, got %+v", attempts[1])
	}
}

func TestExecuteSucceedsAfterFlakyFailure(t *testing.T) {
	p := fastPolicy(2)
	calls := 0

	res, attempts, err := Execute(context.Background(), p, func(ctx context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, provider.NewProviderError("p", http.StatusInternalServerError, provider.ErrorTypeServerError, "boom", nil)
		}
		return 42, nil
	})

	if err != nil || res != 42 {
		t.Fatalf("err=%v res=%d, want success after one retry", err, res)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if attempts[0].FailureClass != string(failure.ClassUpstreamError) || attempts[1].FailureClass != "" {
		t.Fatalf("attempt classes wrong: %+v", attempts)
	}
}

func TestExecuteNeverCallsAgainAfterAuthFailure(t *testing.T) {
	p := fastPolicy(5)
	calls := 0

	_, _, _ = Execute(context.Background(), p, func(ctx context.Context) (int, error) {
		calls++
		return 0, provider.NewProviderError("p", http.StatusUnauthorized, provider.ErrorTypeAuthentication, "bad key", nil)
	})

	if calls != 1 {
		t.Fatalf("auth failures must not be retried, got %d calls", calls)
	}
}

func TestExecuteAbortsWhenContextCancelsDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := RetryPolicy{
		Enabled:           true,
		MaxRetries:        5,
		InitialBackoff:    50 * time.Millisecond,
		MaxBackoff:        50 * time.Millisecond,
		BackoffMultiplier: 2.0,
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	calls := 0
	start := time.Now()
	_, _, err := Execute(ctx, p, func(c context.Context) (int, error) {
		calls++
		return 0, provider.NewProviderError("p", http.StatusServiceUnavailable, provider.ErrorTypeServerError, "full", nil)
	})
	elapsed := time.Since(start)

	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (aborted during first wait)", calls)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("cancellation did not abort the backoff wait: %v", elapsed)
	}
	var pe *provider.ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("want original provider error on abort, got %v", err)
	}
}
