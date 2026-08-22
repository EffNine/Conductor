package handler

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/resilience"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

// chatWinner captures the winning operation's result across the executor
// boundary. Exactly one field is populated when the plan succeeds.
type chatWinner struct {
	resp *apitypes.ChatCompletionResponse
	ch   <-chan apitypes.StreamChunk
}

// buildChatCandidates creates the executor candidates for a chat request.
// routes[0] must be the resolved primary; the rest are configured fallbacks
// in configured order. Each op is a single execution attempt: the P4.2 retry
// engine wraps it inside ExecutePlan.
//
// The request model is overridden per candidate so upstreams always receive
// concrete provider models (identical to the pre-executor loops).
func buildChatCandidates(req *apitypes.ChatCompletionRequest, routes []*router.ResolvedRoute, win *chatWinner) []resilience.Candidate {
	cands := make([]resilience.Candidate, 0, len(routes))
	for i, r := range routes {
		rr := r
		idx := i
		reqCopy := *req
		reqCopy.Model = rr.ProviderModelID

		cand := resilience.Candidate{
			Index:           idx,
			ProviderName:    rr.ProviderName,
			ModelID:         rr.ModelID,
			ProviderModelID: rr.ProviderModelID,
			Breaker:         rr.Breaker,
		}
		if req.Stream {
			cand.DeferSuccess = true // credit lands in stream finalize (P4.3)
			cand.Op = func(ctx context.Context) error {
				ch, err := rr.Provider.ChatCompletionStream(ctx, &reqCopy)
				if err != nil {
					return err
				}
				win.ch = ch
				return nil
			}
		} else {
			cand.Op = func(ctx context.Context) error {
				resp, err := rr.Provider.ChatCompletion(ctx, &reqCopy)
				if err != nil {
					return err
				}
				win.resp = resp
				return nil
			}
		}
		cands = append(cands, cand)
	}
	return cands
}

// chatPlanSink preserves every log line, metric counter, and telemetry hook
// of the pre-P4.4.1 candidate loops at identical call timing. It mirrors the
// old branches' primary-vs-fallback distinctions via Candidate.Index.
type chatPlanSink struct {
	h             *Handler
	isStream      bool
	requestID     string
	correlationID string
	start         time.Time

	routes []*router.ResolvedRoute // index-aligned with candidates; [0] = primary
	// usageModelID is the virtual/route model recorded on usage rows: the
	// legacy loops always used resolved.ModelID even for fallback successes.
	usageModelID string

	lastDurationMs int64 // duration of the most recent completed candidate
}

func (s *chatPlanSink) route(c resilience.Candidate) *router.ResolvedRoute {
	return s.routes[c.Index]
}

func (s *chatPlanSink) CandidateSkipped(c resilience.Candidate, reason resilience.SkipReason) {
	s.h.metrics.IncrementBreakerRejections()
	if c.Index == 0 {
		s.h.metrics.IncrementErrors()
		s.h.logger.Warn("request:primary_breaker_open",
			zap.String("correlation_id", s.correlationID),
			zap.String("request_id", s.requestID),
			zap.String("provider", c.ProviderName),
		)
	}
}

func (s *chatPlanSink) CandidateFailed(c resilience.Candidate, err error, attempts []resilience.Attempt, duration time.Duration) {
	s.h.logRetries(c.ProviderName, len(attempts))

	latencyMs := duration.Milliseconds()
	if c.Index == 0 {
		// The legacy primary path measured failures from request start.
		latencyMs = time.Since(s.start).Milliseconds()
		s.h.metrics.IncrementErrors()
	} else {
		s.h.metrics.IncrementRetries()
	}

	s.h.recordModelResult(s.route(c), err, latencyMs)
	// Legacy indices: primary literal 0; fallback i+1 == candidate index.
	s.h.recordExecutionTelemetry(c.ProviderName, c.ProviderModelID, false, c.Index)

	logName := "request:fallback_error"
	if s.isStream {
		logName = "request:stream_fallback_error"
	}
	if c.Index == 0 {
		logName = "request:provider_error"
		if s.isStream {
			logName = "request:stream_provider_error"
		}
	}
	s.h.logger.Warn(logName,
		zap.String("correlation_id", s.correlationID),
		zap.String("request_id", s.requestID),
		zap.String("provider", c.ProviderName),
		zap.Int64("latency_ms", latencyMs),
		classField(err),
		zap.Error(err),
	)
}

func (s *chatPlanSink) CandidateSucceeded(c resilience.Candidate, attempts []resilience.Attempt, duration time.Duration) {
	s.h.logRetries(c.ProviderName, len(attempts))

	latencyMs := duration.Milliseconds()
	if c.Index == 0 {
		latencyMs = time.Since(s.start).Milliseconds()
	}
	s.lastDurationMs = latencyMs

	s.h.metrics.RecordProviderLatency(latencyMs)
	s.h.metrics.RecordProviderLatencyForProvider(c.ProviderName, latencyMs)
	s.h.recordModelResult(s.route(c), nil, latencyMs)
	// Legacy indices: primary len(attempts)-1; fallback i+len(attempts).
	s.h.recordExecutionTelemetry(c.ProviderName, c.ProviderModelID, true, len(attempts)-1+c.Index)
}
