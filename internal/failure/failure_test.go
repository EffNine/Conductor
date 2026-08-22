package failure

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/provider"
)

func TestClassifyProviderErrorTypes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Class
	}{
		{"rate limit", provider.NewProviderError("p", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil), ClassRateLimited},
		{"auth", provider.NewProviderError("p", http.StatusUnauthorized, provider.ErrorTypeAuthentication, "bad key", nil), ClassAuthFailed},
		{"invalid request", provider.NewProviderError("p", http.StatusBadRequest, provider.ErrorTypeInvalidRequest, "bad body", nil), ClassInvalidRequest},
		{"context length", provider.NewProviderError("p", http.StatusBadRequest, provider.ErrorTypeContextLength, "too many tokens", nil), ClassInvalidRequest},
		{"server error", provider.NewProviderError("p", http.StatusInternalServerError, provider.ErrorTypeServerError, "boom", nil), ClassUpstreamError},
		{"bad gateway", provider.NewProviderError("p", http.StatusBadGateway, provider.ErrorTypeServerError, "upstream broke", nil), ClassUpstreamError},
		{"service unavailable", provider.NewProviderError("p", http.StatusServiceUnavailable, provider.ErrorTypeServerError, "no capacity", nil), ClassCapacity},
		{"overloaded 529", provider.NewProviderError("p", 529, provider.ErrorTypeServerError, "overloaded", nil), ClassCapacity},
		{"transport refused", provider.NewProviderError("p", http.StatusBadGateway, provider.ErrorTypeProviderUnavailable, "connect refused", errors.New("connection refused")), ClassNetworkError},
		{
			"transport deadline",
			provider.NewProviderError("p", http.StatusBadGateway, provider.ErrorTypeProviderUnavailable,
				"provider request failed", &url.Error{Op: "Post", URL: "http://up", Err: context.DeadlineExceeded}),
			ClassTimeout,
		},
		{
			"transport net timeout",
			provider.NewProviderError("p", http.StatusBadGateway, provider.ErrorTypeProviderUnavailable,
				"provider request failed", &url.Error{Op: "Post", URL: "http://up", Err: timeoutErr{}}),
			ClassTimeout,
		},
		// Unknown Type strings fall back to status-code heuristics so every
		// current adapter path still produces a Class.
		{"unknown type 429", provider.NewProviderError("p", http.StatusTooManyRequests, "weird_type", "throttled", nil), ClassRateLimited},
		{"unknown type 403", provider.NewProviderError("p", http.StatusForbidden, "weird_type", "denied", nil), ClassAuthFailed},
		{"unknown type 422", provider.NewProviderError("p", http.StatusUnprocessableEntity, "weird_type", "nope", nil), ClassInvalidRequest},
		{"unknown type 503", provider.NewProviderError("p", http.StatusServiceUnavailable, "weird_type", "full", nil), ClassCapacity},
		{"unknown type 5xx", provider.NewProviderError("p", 599, "weird_type", "gone", nil), ClassUpstreamError},
		{"unknown type zero status", provider.NewProviderError("p", 0, "weird_type", "?", nil), ClassUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, hint := Classify(tt.err)
			if got != tt.want {
				t.Fatalf("Classify(%v) = %q, want %q", tt.err, got, tt.want)
			}
			var pe *provider.ProviderError
			if !errors.As(tt.err, &pe) || pe.RetryAfter == 0 {
				if hint != 0 {
					t.Fatalf("hint = %v, want 0", hint)
				}
			}
		})
	}
}

func TestClassifyRawErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Class
	}{
		{"nil", nil, ClassUnknown},
		{"deadline exceeded", context.DeadlineExceeded, ClassTimeout},
		{"wrapped deadline", fmt.Errorf("outer: %w", context.DeadlineExceeded), ClassTimeout},
		{"net timeout", timeoutErr{}, ClassTimeout},
		{"plain error", errors.New("mystery"), ClassUnknown},
		{"canceled", context.Canceled, ClassUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _ := Classify(tt.err); got != tt.want {
				t.Fatalf("Classify(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyPropagatesRetryAfterHint(t *testing.T) {
	hint := 30 * time.Second
	pe := provider.NewProviderError("p", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil)
	pe.RetryAfter = hint

	got, backoff := Classify(pe)
	if got != ClassRateLimited {
		t.Fatalf("class = %q, want %q", got, ClassRateLimited)
	}
	if backoff != hint {
		t.Fatalf("backoff = %v, want %v", backoff, hint)
	}
}

func TestClassifyWrappedProviderError(t *testing.T) {
	pe := provider.NewProviderError("p", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil)
	wrapped := fmt.Errorf("attempt failed: %w", pe)

	got, _ := Classify(wrapped)
	if got != ClassRateLimited {
		t.Fatalf("wrapped classify = %q, want %q", got, ClassRateLimited)
	}
}

func TestPolicyTable(t *testing.T) {
	tests := map[Class]Policy{
		ClassRateLimited:    {Retryable: true, BreakerImpact: BreakerImpactThrottleOnly, SuggestedBackoffSupported: true},
		ClassTimeout:        {Retryable: true, BreakerImpact: BreakerImpactCount, SuggestedBackoffSupported: false},
		ClassAuthFailed:     {Retryable: false, BreakerImpact: BreakerImpactNone, SuggestedBackoffSupported: false},
		ClassCapacity:       {Retryable: true, BreakerImpact: BreakerImpactCount, SuggestedBackoffSupported: true},
		ClassUpstreamError:  {Retryable: true, BreakerImpact: BreakerImpactCount, SuggestedBackoffSupported: false},
		ClassNetworkError:   {Retryable: true, BreakerImpact: BreakerImpactCount, SuggestedBackoffSupported: false},
		ClassInvalidRequest: {Retryable: false, BreakerImpact: BreakerImpactNone, SuggestedBackoffSupported: false},
		ClassUnknown:        {Retryable: false, BreakerImpact: BreakerImpactCount, SuggestedBackoffSupported: false},
	}

	for class, want := range tests {
		t.Run(string(class), func(t *testing.T) {
			got := PolicyFor(class)
			if got != want {
				t.Fatalf("PolicyFor(%q) = %+v, want %+v", class, got, want)
			}
		})
	}
}

// timeoutErr implements net.Error reporting a timeout without being a real
// network failure.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}
