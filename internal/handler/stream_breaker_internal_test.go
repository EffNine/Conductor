package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/metrics"
	"github.com/EffNine/conductor/internal/provider"
)

func TestRecordStreamBreakerOutcomeMatrix(t *testing.T) {
	rateLimited := provider.NewProviderError("p", http.StatusTooManyRequests, provider.ErrorTypeRateLimit, "slow down", nil)
	serverErr := provider.NewProviderError("p", http.StatusInternalServerError, provider.ErrorTypeServerError, "boom", nil)

	cases := []struct {
		name               string
		outcome            metrics.StreamOutcome
		clientDisconnected bool
		streamErr          error
		wantSuccesses      int64
		wantFailures       int64
		wantThrottles      int64
	}{
		{
			name:          "completed stream records success",
			outcome:       metrics.StreamCompleted,
			wantSuccesses: 1,
		},
		{
			name:          "mid-stream rate limit records throttle only",
			outcome:       metrics.StreamError,
			streamErr:     rateLimited,
			wantThrottles: 1,
		},
		{
			name:         "mid-stream server error counts as failure",
			outcome:      metrics.StreamError,
			streamErr:    serverErr,
			wantFailures: 1,
		},
		{
			name:         "disconnect without done counts as network failure",
			outcome:      metrics.StreamError,
			streamErr:    nil, // channel closed without [DONE]
			wantFailures: 1,
		},
		{
			name:         "idle timeout counts as failure",
			outcome:      metrics.StreamTimeout,
			wantFailures: 1,
		},
		{
			name:               "client disconnect is ignored",
			outcome:            metrics.StreamCancelled,
			clientDisconnected: true,
		},
		{
			name:      "server shutdown cancel is ignored",
			outcome:   metrics.StreamCancelled,
			streamErr: context.Canceled,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := breaker.New(breaker.Config{FailureThreshold: 100, RecoveryTimeout: 1, SuccessThreshold: 1})
			recordStreamBreakerOutcome(b, tc.outcome, tc.clientDisconnected, tc.streamErr)

			stats := b.Stats()
			if stats.TotalSuccesses != tc.wantSuccesses ||
				stats.TotalFailures != tc.wantFailures ||
				stats.TotalThrottles != tc.wantThrottles {
				t.Fatalf("stats = (succ=%d fail=%d thr=%d), want (%d/%d/%d)",
					stats.TotalSuccesses, stats.TotalFailures, stats.TotalThrottles,
					tc.wantSuccesses, tc.wantFailures, tc.wantThrottles)
			}
		})
	}
}

func TestRecordStreamBreakerOutcomeNilBreakerIsNoop(t *testing.T) {
	recordStreamBreakerOutcome(nil, metrics.StreamCompleted, false, nil)
	recordStreamBreakerOutcome(nil, metrics.StreamError, false, context.Canceled)
}
