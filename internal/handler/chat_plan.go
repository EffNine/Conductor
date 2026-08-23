package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/failure"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/resilience"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

// SetAttemptEmitter wires the asynchronous execution-attempt publisher
// (P4.4.3). Nil (the default) disables attempt persistence entirely; the
// request path never depends on the emitter's behaviour.
func (h *Handler) SetAttemptEmitter(emit func(database.AttemptRecord)) {
	h.attemptEmitter = emit
}

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
//
// When an attempt emitter is wired (P4.4.3), each lifecycle notification is
// also published as a database.AttemptRecord for asynchronous persistence.
type chatPlanSink struct {
	h             *Handler
	isStream      bool
	requestID     string
	correlationID string
	mode          string
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

// emitAttempt publishes one attempt record when persistence is wired.
func (s *chatPlanSink) emitAttempt(c resilience.Candidate, outcome, skipReason string, err error, attempts []resilience.Attempt, duration time.Duration) {
	emit := s.h.attemptEmitter
	if emit == nil {
		return
	}
	rec := database.AttemptRecord{
		RequestID:       s.requestID,
		CorrelationID:   s.correlationID,
		VirtualModel:    s.usageModelID,
		Mode:            s.mode,
		Provider:        c.ProviderName,
		ProviderModelID: c.ProviderModelID,
		CandidateIndex:  c.Index,
		Outcome:         outcome,
		SkipReason:      skipReason,
		LatencyMS:       duration.Milliseconds(),
	}
	switch outcome {
	case database.AttemptOutcomeSuccess:
		rec.HTTPStatus = http.StatusOK
		rec.AttemptIndex = len(attempts) - 1
	case database.AttemptOutcomeFailed:
		class, _ := failure.Classify(err)
		rec.FailureClass = string(class)
		var pe *provider.ProviderError
		if errors.As(err, &pe) {
			rec.HTTPStatus = pe.StatusCode
		}
		rec.AttemptIndex = len(attempts) - 1
		for _, a := range attempts {
			rec.RetryWaitMS += a.RetryWait.Milliseconds()
			if a.HintHonored {
				rec.RetryAfterHonored = true
			}
		}
	}
	emit(rec)
}

func (s *chatPlanSink) CandidateSkipped(c resilience.Candidate, reason resilience.SkipReason) {
	s.emitAttempt(c, database.AttemptOutcomeSkipped, string(reason), nil, nil, 0)
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

// AttemptExecuted persists one non-terminal physical attempt so every
// upstream execution maps to exactly one request_attempts row.
func (s *chatPlanSink) AttemptExecuted(c resilience.Candidate, a resilience.Attempt) {
	rec := database.AttemptRecord{
		RequestID:         s.requestID,
		CorrelationID:     s.correlationID,
		VirtualModel:      s.usageModelID,
		Mode:              s.mode,
		Provider:          c.ProviderName,
		ProviderModelID:   c.ProviderModelID,
		CandidateIndex:    c.Index,
		AttemptIndex:      a.Index,
		Outcome:           database.AttemptOutcomeFailed,
		FailureClass:      a.FailureClass,
		LatencyMS:         a.Duration.Milliseconds(),
		RetryWaitMS:       a.RetryWait.Milliseconds(),
		RetryAfterHonored: a.HintHonored,
	}
	if s.h.attemptEmitter != nil {
		s.h.attemptEmitter(rec)
	}
}

func (s *chatPlanSink) CandidateFailed(c resilience.Candidate, err error, attempts []resilience.Attempt, duration time.Duration) {
	s.emitAttempt(c, database.AttemptOutcomeFailed, "", err, attempts, duration)
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
	if !c.DeferSuccess {
		// Non-streaming: terminal success row emitted now.
		s.emitAttempt(c, database.AttemptOutcomeSuccess, "", nil, attempts, duration)
	}
	// Streaming: the row is emitted at stream finalize with the true
	// terminal outcome (P4.3 deferred credit semantics).
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
