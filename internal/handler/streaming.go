package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/failure"
	"github.com/EffNine/conductor/internal/metrics"
	"github.com/EffNine/conductor/internal/middleware"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/resilience"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// handleStreaming handles a streaming chat completion request
func (h *Handler) handleStreaming(c *fiber.Ctx, req *apitypes.ChatCompletionRequest, resolved *router.ResolvedRoute, fallbacks []router.ResolvedRoute) error {
	start := time.Now()
	requestID := uuid.New().String()
	correlationID := middleware.GetCorrelationIDFromLocals(c)
	policy := h.retryPolicy()

	h.metrics.IncrementRequests()
	h.metrics.IncrementStreams()

	h.logger.Info("request:start",
		zap.String("correlation_id", correlationID),
		zap.String("request_id", requestID),
		zap.String("model", req.Model),
		zap.String("provider", resolved.ProviderName),
		zap.String("provider_model", resolved.ProviderModelID),
		zap.Bool("stream", true),
	)

	// Ask upstreams for a final usage chunk (many omit it unless requested).
	req.EnsureStreamUsage()

	// Set SSE headers
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	// Cancellable context so a terminating stream can stop the provider
	// goroutine promptly. The stream writer owns cancellation (see
	// streamResponse); every other exit path cancels explicitly.
	//
	// It derives from context.Background() (not the fasthttp request
	// context): fasthttp resets the RequestCtx's userData when the request
	// completes, which races with context.Cause() reading Value() while the
	// body-stream goroutine is still running. Client disconnects are
	// detected via write errors and server shutdown via the captured
	// requestDone channel, so nothing depends on the parent context firing.
	streamCtx, cancel := context.WithCancel(context.Background())

	// P4.4.1: candidate traversal (primary + configured fallbacks) runs
	// through the shared resilience executor. Streaming ops acquire the
	// channel only; same-provider retries still apply exclusively to
	// synchronous acquisition failures, and breaker credit stays deferred to
	// stream finalize (P4.3).
	routes := make([]*router.ResolvedRoute, 0, 1+len(fallbacks))
	routes = append(routes, resolved)
	for i := range fallbacks {
		routes = append(routes, &fallbacks[i])
	}
	sink := &chatPlanSink{
		h:             h,
		isStream:      true,
		requestID:     requestID,
		correlationID: correlationID,
		mode:          req.Mode,
		start:         start,
		routes:        routes,
		usageModelID:  resolved.ModelID,
	}
	win := &chatWinner{}
	plan := resilience.Plan{
		Candidates:         buildChatCandidates(req, routes, win),
		Retry:              policy,
		Sink:               sink,
		Budget:             h.executionBudget(),
		EstimatedTokens:    int64(router.EstimateRequestTokens(req)),
		DetachAfterSuccess: true, // acquired streams outlive the budget deadline
	}
	res := resilience.ExecutePlan(streamCtx, plan)

	if res.WinnerIndex >= 0 {
		winner := routes[res.WinnerIndex]
		winnerCand := plan.Candidates[res.WinnerIndex]
		attemptFinalize := func(outcome metrics.StreamOutcome, clientDisconnected bool, streamErr error) {
			if h.attemptEmitter == nil {
				return
			}
			rec := database.AttemptRecord{
				RequestID:       requestID,
				CorrelationID:   correlationID,
				VirtualModel:    resolved.ModelID,
				Mode:            req.Mode,
				Provider:        winner.ProviderName,
				ProviderModelID: winner.ProviderModelID,
				CandidateIndex:  winnerCand.Index,
			}
			switch outcome {
			case metrics.StreamCompleted:
				rec.Outcome = database.AttemptOutcomeSuccess
				rec.HTTPStatus = http.StatusOK
			case metrics.StreamTimeout:
				rec.Outcome = database.AttemptOutcomeFailed
				rec.FailureClass = "timeout"
			case metrics.StreamError:
				rec.Outcome = database.AttemptOutcomeFailed
				if streamErr != nil {
					class, _ := failure.Classify(streamErr)
					rec.FailureClass = string(class)
					var pe *provider.ProviderError
					if errors.As(streamErr, &pe) {
						rec.HTTPStatus = pe.StatusCode
					}
				} else {
					rec.FailureClass = string(failure.ClassNetworkError)
				}
			default:
				// Client disconnect / server shutdown: not a provider failure.
				rec.Outcome = database.AttemptOutcomeSkipped
				rec.SkipReason = "client_disconnected"
			}
			h.attemptEmitter(rec)
		}
		return h.streamResponse(c, win.ch, &streamSession{
			requestID:       requestID,
			correlationID:   correlationID,
			providerName:    winner.ProviderName,
			modelID:         winner.ModelID,
			providerModelID: winner.ProviderModelID,
			start:           start,
			cancel:          cancel,
			breaker:         winner.Breaker,
			attemptFinalize: attemptFinalize,
		})
	}

	// Legacy open-primary contract: blocked primary and nothing could serve.
	if res.FirstBlocked && !res.AttemptedAny {
		cancel()
		return c.Status(fiber.StatusServiceUnavailable).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: fmt.Sprintf("provider '%s' circuit breaker is open", resolved.ProviderName),
				Type:    "provider_unavailable",
				Code:    "circuit_breaker_open",
			},
		})
	}

	// All providers failed
	cancel()
	h.metrics.IncrementStreamErrors()
	h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, nil, time.Since(start), fiber.StatusBadGateway, true, res.LastError)
	h.logRequestComplete(correlationID, requestID, resolved, time.Since(start).Milliseconds(), fiber.StatusBadGateway, true, res.LastError)
	return h.providerErrorResponse(c, res.LastError)
}

// streamSession bundles per-stream context shared by the handler and the
// response writer so every exit path cleans up exactly once.
type streamSession struct {
	requestID       string
	correlationID   string
	providerName    string
	modelID         string // user-facing route model
	providerModelID string // upstream model slug
	start           time.Time
	cancel          context.CancelFunc

	// breaker receives the classification-aware outcome once the stream
	// reaches a terminal state (P4.3). Nil disables accounting.
	breaker *breaker.Breaker

	// attemptFinalize closes the persisted-attempt row that maps to this
	// stream with the true terminal outcome (P4.4.3). Nil = no row.
	attemptFinalize func(outcome metrics.StreamOutcome, clientDisconnected bool, streamErr error)
}

// streamResponse streams chunks to the client with full lifecycle
// accounting: metrics, structured logs and deterministic cleanup of the
// provider goroutine, the provider channel and the idle timer.
// streamResponse streams chunks to the client with full lifecycle
// accounting: metrics, structured logs and deterministic cleanup of the
// provider goroutine, the provider channel and the idle timer.
func (h *Handler) streamResponse(c *fiber.Ctx, ch <-chan apitypes.StreamChunk, s *streamSession) error {
	h.metrics.RecordStreamStarted(s.providerName)
	h.metrics.IncrementActiveStreams()
	h.logger.Info("stream_start", s.logFields(0, 0, 0)...)

	// Capture the request-cancellation channel in the handler goroutine.
	// fasthttp resets the RequestCtx after the handler returns, so reading
	// it inside the body-stream goroutine would race with that reset. The
	// captured channel fires only when the server shuts down; client
	// disconnects are detected via write errors on the response pipe.
	requestDone := c.Context().Done()

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		h.writeStream(ch, w, s, requestDone)
	})
	return nil
}

// writeStream writes SSE frames to the client until the stream terminates,
// then performs once-only cleanup: cancel the provider context, drain the
// provider channel, decrement the active gauge and record the outcome.
//
// Client disconnects surface as write/flush errors on the response pipe;
// they are detected here and turned into a cancelled outcome. Provider
// stall, truncation and error chunks each map to a distinct outcome.
func (h *Handler) writeStream(ch <-chan apitypes.StreamChunk, w *bufio.Writer, s *streamSession, requestDone <-chan struct{}) {
	var usageData *apitypes.Usage
	var sawContent bool
	var reasoningBuf string
	var lastMeta apitypes.StreamChunk
	sentDone := false

	outcome := metrics.StreamCompleted
	var streamErr error
	clientDisconnected := false
	var chunkCount int64
	var bytesSent int64

	var finalizeOnce sync.Once
	finalize := func() {
		finalizeOnce.Do(func() {
			// Stop the provider goroutine and make sure its channel is never
			// left blocking on a send (drain terminates once the provider
			// observes the cancelled context and closes the channel).
			s.cancel()
			go drainStream(ch)

			h.metrics.DecrementActiveStreams()
			h.metrics.RecordStreamOutcome(s.providerName, outcome, int(chunkCount), int(bytesSent), time.Since(s.start).Milliseconds())

			h.logStreamOutcome(s, outcome, clientDisconnected, streamErr, chunkCount, bytesSent)

			// P4.3: breaker credit is granted on stream completion, not on
			// channel acquisition; provider-side terminations are classified.
			recordStreamBreakerOutcome(s.breaker, outcome, clientDisconnected, streamErr)
			// P4.4.3: close the attempt row with the true terminal outcome.
			if s.attemptFinalize != nil {
				s.attemptFinalize(outcome, clientDisconnected, streamErr)
			}

			h.trackUsage(s.requestID, s.modelID, s.providerModelID, s.providerName, usageData, time.Since(s.start), fiber.StatusOK, true, streamErr)
			h.logRequestComplete(s.correlationID, s.requestID, &router.ResolvedRoute{
				ProviderName:    s.providerName,
				ProviderModelID: s.providerModelID,
				ModelID:         s.modelID,
			}, time.Since(s.start).Milliseconds(), fiber.StatusOK, true, streamErr)
		})
	}
	// Every exit path of this function must finalize exactly once. `cancel`
	// is idempotent, so the bare deferred call is a leak-proof safety net.
	defer finalize()
	defer s.cancel()

	writeChunk := func(chunk apitypes.StreamChunk) bool {
		data, err := json.Marshal(chunk)
		if err != nil {
			h.logStreamWriteError(s, err, chunkCount, bytesSent)
			return false
		}
		frame := []byte("data: " + string(data) + "\n\n")
		if _, err := w.Write(frame); err != nil {
			h.logStreamWriteError(s, err, chunkCount, bytesSent)
			return false
		}
		if err := w.Flush(); err != nil {
			h.logStreamWriteError(s, err, chunkCount, bytesSent)
			return false
		}
		chunkCount++
		bytesSent += int64(len(frame))
		h.metrics.RecordStreamChunk(1)
		h.metrics.RecordStreamBytes(len(frame))
		return true
	}

	accumulateMessage := func(m *apitypes.Message) {
		if m == nil {
			return
		}
		if m.ContentString() != "" {
			sawContent = true
		}
		if m.Reasoning != "" {
			reasoningBuf += m.Reasoning
		}
		if m.ReasoningContent != "" {
			reasoningBuf += m.ReasoningContent
		}
	}

	// Reasoning-only streams (e.g. Seed-OSS, big-pickle) emit text in
	// reasoning/reasoning_content with empty content. Chat apps that only
	// read delta.content need a synthetic content chunk. Emit it before
	// finish_reason so clients that finalize on stop still see a reply.
	flushReasoningAsContent := func() {
		if sawContent || reasoningBuf == "" {
			return
		}
		writeChunk(apitypes.StreamChunk{
			ID:      lastMeta.ID,
			Object:  "chat.completion.chunk",
			Created: lastMeta.Created,
			Model:   lastMeta.Model,
			Choices: []apitypes.Choice{{
				Index: 0,
				Delta: &apitypes.Message{
					Role:    "assistant",
					Content: reasoningBuf,
				},
			}},
		})
		sawContent = true
	}

	finishStream := func() {
		if sentDone {
			return
		}
		flushReasoningAsContent()
		frame := []byte("data: [DONE]\n\n")
		if _, err := w.Write(frame); err != nil {
			h.logStreamWriteError(s, err, chunkCount, bytesSent)
			return
		}
		if err := w.Flush(); err != nil {
			h.logStreamWriteError(s, err, chunkCount, bytesSent)
			return
		}
		sentDone = true
	}

	// Idle timeout: end the stream if the provider goes silent. Reset on
	// every chunk so slow-but-alive providers are never cut off.
	var idle *time.Timer
	var idleC <-chan time.Time
	if h.streamIdleTimeout > 0 {
		idle = time.NewTimer(h.streamIdleTimeout)
		defer idle.Stop()
		idleC = idle.C
	}

loop:
	for {
		select {
		case <-requestDone:
			// Request torn down (server shutdown). Treat as cancelled so the
			// provider goroutine is released and the active gauge drains.
			outcome = metrics.StreamCancelled
			break loop
		case <-idleC:
			outcome = metrics.StreamTimeout
			break loop
		case chunk, ok := <-ch:
			if !ok {
				// Provider closed the channel without [DONE] (truncated body).
				if !sentDone {
					outcome = metrics.StreamError
				}
				break loop
			}
			if idle != nil {
				idle.Reset(h.streamIdleTimeout)
			}

			if chunk.Error != nil {
				streamErr = chunk.Error
				outcome = metrics.StreamError
				break loop
			}

			if chunk.Done {
				finishStream()
				outcome = metrics.StreamCompleted
				break loop
			}

			// Drop zero-value chunks (upstream data: {}) so clients never see
			// empty model/id frames that wipe aggregated replies.
			if chunk.IsEmpty() {
				continue
			}

			for _, choice := range chunk.Choices {
				accumulateMessage(choice.Delta)
				accumulateMessage(choice.Message)
			}
			if chunk.ID != "" || chunk.Model != "" {
				lastMeta = chunk
			}

			for _, choice := range chunk.Choices {
				if choice.FinishReason != nil && *choice.FinishReason != "" {
					flushReasoningAsContent()
					break
				}
			}

			if !writeChunk(chunk) {
				// Write/flush failed: the client is gone. Stop streaming.
				clientDisconnected = true
				outcome = metrics.StreamCancelled
				break loop
			}

			if chunk.Usage != nil {
				usageData = chunk.Usage
			}

			h.logger.Debug("stream_chunk", s.logFields(time.Since(s.start).Milliseconds(), chunkCount, bytesSent)...)
		}
	}

	// Gracefully terminate for still-connected clients (provider timeout,
	// provider disconnect, partial failure). For client disconnects the
	// connection is gone; writing would fail and is pointless.
	if outcome != metrics.StreamCancelled {
		finishStream()
	}
}

// drainStream consumes a provider channel until it closes, preventing a
// provider goroutine from blocking forever on a send after the handler has
// stopped reading. It terminates once the provider observes cancellation.
func drainStream(ch <-chan apitypes.StreamChunk) {
	for range ch {
	}
}

// logFields builds the standard field set for stream lifecycle events.
func (s *streamSession) logFields(durationMs, chunkCount, bytesSent int64, extra ...zap.Field) []zap.Field {
	fields := []zap.Field{
		zap.String("correlation_id", s.correlationID),
		zap.String("request_id", s.requestID),
		zap.String("provider", s.providerName),
		zap.String("model", s.providerModelID),
		zap.Int64("stream_duration_ms", durationMs),
		zap.Int64("chunk_count", chunkCount),
		zap.Int64("bytes_sent", bytesSent),
	}
	return append(fields, extra...)
}

// logStreamOutcome emits the structured lifecycle event for a finished stream.
func (h *Handler) logStreamOutcome(s *streamSession, outcome metrics.StreamOutcome, clientDisconnected bool, streamErr error, chunkCount, bytesSent int64) {
	duration := time.Since(s.start).Milliseconds()
	fields := func(extra ...zap.Field) []zap.Field {
		return s.logFields(duration, chunkCount, bytesSent, extra...)
	}
	switch outcome {
	case metrics.StreamCompleted:
		h.logger.Info("stream_complete", fields()...)
	case metrics.StreamCancelled:
		if clientDisconnected {
			h.logger.Info("stream_client_disconnect", fields()...)
		} else {
			h.logger.Info("stream_cancel", fields()...)
		}
	case metrics.StreamTimeout:
		h.logger.Warn("stream_timeout", fields(zap.Duration("idle_timeout", h.streamIdleTimeout))...)
	case metrics.StreamError:
		if streamErr != nil {
			h.logger.Error("stream_error", fields(zap.Error(streamErr))...)
		} else {
			h.logger.Warn("stream_provider_disconnect", fields()...)
		}
	}
}

// logStreamWriteError logs a failed SSE write or flush.
func (h *Handler) logStreamWriteError(s *streamSession, err error, chunkCount, bytesSent int64) {
	h.logger.Warn("stream_write_error", s.logFields(time.Since(s.start).Milliseconds(), chunkCount, bytesSent, zap.Error(err))...)
}
