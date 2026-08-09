package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/cache"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/metrics"
	"github.com/EffNine/conductor/internal/middleware"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler holds HTTP handlers for the gateway
type Handler struct {
	router            *router.Engine
	registry          *provider.Registry
	catalog           *catalog.Catalog
	usageTracker      *usage.Tracker
	db                *database.Database
	logger            *zap.Logger
	startTime         time.Time
	reloadFn          func() error
	modelProber       *health.ModelProber
	modelStatus       *health.ModelStatusStore
	metrics           *metrics.Collector
	routingEngine     *router.RouterEngine
	cacheEngine       *cache.Engine
	streamIdleTimeout time.Duration
}

// New creates a new Handler
func New(r *router.Engine, reg *provider.Registry, ut *usage.Tracker, logger *zap.Logger, cat *catalog.Catalog, db *database.Database) *Handler {
	return &Handler{
		router:       r,
		registry:     reg,
		catalog:      cat,
		usageTracker: ut,
		db:           db,
		logger:       logger,
		startTime:    time.Now(),
		metrics:      metrics.NewCollector(),
	}
}

// SetReloadFunc sets the optional config reload callback used by PUT /api/config/reload.
func (h *Handler) SetReloadFunc(fn func() error) {
	h.reloadFn = fn
}

// SetModelStatus wires per-model reachability tracking (probe + reactive updates).
func (h *Handler) SetModelStatus(store *health.ModelStatusStore, prober *health.ModelProber) {
	h.modelStatus = store
	h.modelProber = prober
}

// SetMetrics wires an external metrics collector (for tests or shared collectors).
func (h *Handler) SetMetrics(m *metrics.Collector) {
	if m != nil {
		h.metrics = m
	}
}

// Metrics returns the handler's metrics collector.
func (h *Handler) Metrics() *metrics.Collector {
	return h.metrics
}

// SetAutoSelector wires runtime automatic model selection into the router.
func (h *Handler) SetAutoSelector(s router.AutoSelector) {
	h.router.SetAutoSelector(s)
}

// SetRoutingEngine wires the intelligent routing engine.
func (h *Handler) SetRoutingEngine(re *router.RouterEngine) {
	h.routingEngine = re
}

// SetCacheEngine wires the response cache engine.
func (h *Handler) SetCacheEngine(e *cache.Engine) {
	h.cacheEngine = e
}

// SetStreamIdleTimeout configures the streaming idle timeout. Values <= 0
// disable the timeout entirely. Defaults to 5 minutes when unset.
func (h *Handler) SetStreamIdleTimeout(d time.Duration) {
	h.streamIdleTimeout = d
}

// Register registers all HTTP routes
func (h *Handler) Register(app *fiber.App) {
	// OpenAI-compatible endpoints
	app.Post("/v1/chat/completions", h.HandleChatCompletion)
	app.Get("/v1/models", h.HandleListModels)
	app.Post("/v1/embeddings", h.HandleEmbeddings)

	// Health endpoints
	app.Get("/health", h.HandleHealth)

	// Dashboard endpoints
	app.Get("/api/models", h.HandleDashboardModels)
	app.Get("/api/models/status", h.HandleModelStatus)
	app.Post("/api/models/force-probe", h.HandleForceProbe)
	app.Get("/api/auto/status", h.HandleAutoStatus)
	app.Get("/api/health", h.HandleProviderHealth)
	app.Get("/api/providers", h.HandleListProviders)
	app.Get("/api/usage", h.HandleUsage)
	app.Get("/api/usage/costs", h.HandleCosts)
	app.Get("/api/logs", h.HandleLogs)
	app.Get("/api/config", h.HandleConfig)
	app.Put("/api/config/reload", h.HandleReloadConfig)
	app.Get("/api/metrics", h.HandleMetrics)
	app.Get("/api/circuit-breaker", h.HandleCircuitBreakerStatus)
	app.Get("/api/routing", h.HandleRouting)
	app.Get("/api/cache", h.HandleCacheStatus)
	app.Get("/api/streams", h.HandleStreamStatus)
}

// HandleAutoStatus reports the runtime auto model selection status.
func (h *Handler) HandleAutoStatus(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"enabled":  h.router.HasAutoSelector(),
		"provider": "nvidia_nim",
		"note":     "auto mode currently selects from NVIDIA NIM catalog using health, cost, and latency",
	})
}

// HandleChatCompletion handles POST /v1/chat/completions
func (h *Handler) HandleChatCompletion(c *fiber.Ctx) error {
	var req apitypes.ChatCompletionRequest
	if err := c.BodyParser(&req); err != nil {
		h.metrics.IncrementErrors()
		return c.Status(fiber.StatusBadRequest).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: "Invalid request body",
				Type:    "invalid_request_error",
				Code:    "invalid_request",
			},
		})
	}

	// Validate required fields
	if req.Model == "" {
		h.metrics.IncrementErrors()
		return c.Status(fiber.StatusBadRequest).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: "model is required",
				Type:    "invalid_request_error",
				Param:   "model",
				Code:    "invalid_request",
			},
		})
	}

	if len(req.Messages) == 0 {
		h.metrics.IncrementErrors()
		return c.Status(fiber.StatusBadRequest).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: "messages is required",
				Type:    "invalid_request_error",
				Param:   "messages",
				Code:    "invalid_request",
			},
		})
	}

	// Resolve route (context-aware and task-aware for auto mode)
	resolved, fallbacks, err := h.router.ResolveWithFallbackAndContext(c.Context(), req.Model, req.Messages)
	if err != nil {
		h.metrics.IncrementErrors()
		h.logger.Warn("route resolution failed",
			zap.String("correlation_id", middleware.GetCorrelationIDFromLocals(c)),
			zap.String("model", req.Model),
			zap.Error(err),
		)
		return c.Status(fiber.StatusNotFound).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: fmt.Sprintf("Model '%s' not found", req.Model),
				Type:    "invalid_request_error",
				Param:   "model",
				Code:    "model_not_found",
			},
		})
	}

	// Log routing decision if intelligent routing is enabled.
	if h.routingEngine != nil {
		h.logRoutingDecision(req.Model, resolved.ProviderName)
	}

	// Override model name if needed
	req.Model = resolved.ProviderModelID

	// Handle streaming
	if req.Stream {
		h.logger.Info("cache_bypass",
			zap.String("provider", resolved.ProviderName),
			zap.String("model", req.Model),
			zap.String("reason", "streaming"),
		)
		return h.handleStreaming(c, &req, resolved, fallbacks)
	}

	// Handle non-streaming
	return h.handleNonStreaming(c, &req, resolved, fallbacks)
}

// handleNonStreaming handles a non-streaming chat completion request
func (h *Handler) handleNonStreaming(c *fiber.Ctx, req *apitypes.ChatCompletionRequest, resolved *router.ResolvedRoute, fallbacks []router.ResolvedRoute) error {
	start := time.Now()
	requestID := uuid.New().String()
	correlationID := middleware.GetCorrelationIDFromLocals(c)

	h.metrics.IncrementRequests()

	h.logger.Info("request:start",
		zap.String("correlation_id", correlationID),
		zap.String("request_id", requestID),
		zap.String("model", req.Model),
		zap.String("provider", resolved.ProviderName),
		zap.String("provider_model", resolved.ProviderModelID),
	)

	// Check response cache for non-streaming requests.
	cacheKey := h.buildCacheKey(req, resolved)
	if h.cacheEngine != nil && h.cacheEngine.IsEnabled() {
		if cached, ok := h.cacheEngine.Get(cacheKey); ok {
			h.logger.Info("cache_hit",
				zap.String("correlation_id", correlationID),
				zap.String("request_id", requestID),
				zap.String("provider", resolved.ProviderName),
				zap.String("model", req.Model),
				zap.String("cache_key", truncateHash(cacheKey, 8)),
			)
			var cachedResp apitypes.ChatCompletionResponse
			if err := json.Unmarshal(cached, &cachedResp); err != nil {
				h.logger.Warn("cache:invalid_entry",
					zap.String("correlation_id", correlationID),
					zap.String("request_id", requestID),
					zap.Error(err),
				)
				goto miss
			}
			latency := time.Since(start).Milliseconds()
			h.logRequestComplete(correlationID, requestID, resolved, latency, fiber.StatusOK, false, nil)
			h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, cachedResp.Usage, time.Since(start), fiber.StatusOK, false, nil)
			return c.JSON(&cachedResp)
		}
		h.logger.Info("cache_miss",
			zap.String("correlation_id", correlationID),
			zap.String("request_id", requestID),
			zap.String("provider", resolved.ProviderName),
			zap.String("model", req.Model),
			zap.String("cache_key", truncateHash(cacheKey, 8)),
		)
	}

miss:
	// Try primary provider
	if resolved.Breaker != nil && resolved.Breaker.Allow() != breaker.ResultAllowed {
		h.metrics.IncrementErrors()
		h.metrics.IncrementBreakerRejections()
		h.logger.Warn("request:breaker_rejected",
			zap.String("correlation_id", correlationID),
			zap.String("request_id", requestID),
			zap.String("provider", resolved.ProviderName),
		)
		return c.Status(fiber.StatusServiceUnavailable).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: fmt.Sprintf("provider '%s' circuit breaker is open", resolved.ProviderName),
				Type:    "provider_unavailable",
				Code:    "circuit_breaker_open",
			},
		})
	}
	resp, err := resolved.Provider.ChatCompletion(c.Context(), req)
	latency := time.Since(start).Milliseconds()
	if err == nil {
		if resolved.Breaker != nil {
			resolved.Breaker.RecordSuccess()
		}
		h.metrics.RecordProviderLatency(latency)
		h.metrics.RecordProviderLatencyForProvider(resolved.ProviderName, latency)
		h.recordModelResult(resolved, nil, latency)
		h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, resp.Usage, time.Since(start), fiber.StatusOK, false, nil)
		h.logRequestComplete(correlationID, requestID, resolved, latency, fiber.StatusOK, false, nil)
		if h.cacheEngine != nil && h.cacheEngine.IsEnabled() {
			if cacheErr := h.cacheEngine.CacheResponse(cacheKey, resp); cacheErr != nil {
				h.logger.Warn("cache:store_failed",
					zap.String("correlation_id", correlationID),
					zap.String("request_id", requestID),
					zap.Error(cacheErr),
				)
			} else {
				h.logger.Info("cache_store",
					zap.String("correlation_id", correlationID),
					zap.String("request_id", requestID),
					zap.String("provider", resolved.ProviderName),
					zap.String("model", req.Model),
					zap.String("cache_key", truncateHash(cacheKey, 8)),
				)
			}
		}
		return c.JSON(resp)
	}
	if resolved.Breaker != nil {
		resolved.Breaker.RecordFailure()
	}
	h.metrics.IncrementErrors()
	h.recordModelResult(resolved, err, latency)
	h.logger.Warn("request:provider_error",
		zap.String("correlation_id", correlationID),
		zap.String("request_id", requestID),
		zap.String("provider", resolved.ProviderName),
		zap.Int64("latency_ms", latency),
		zap.Error(err),
	)

	// Try fallbacks
	for i := range fallbacks {
		fb := fallbacks[i]
		fallbackReq := *req
		fallbackReq.Model = fb.ProviderModelID

		fbStart := time.Now()
		fbResp, fbErr := fb.Provider.ChatCompletion(c.Context(), &fallbackReq)
		fbLatency := time.Since(fbStart).Milliseconds()
		if fbErr == nil {
			if fb.Breaker != nil {
				fb.Breaker.RecordSuccess()
			}
			h.metrics.RecordProviderLatency(fbLatency)
			h.metrics.RecordProviderLatencyForProvider(fb.ProviderName, fbLatency)
			h.recordModelResult(&fb, nil, fbLatency)
			h.trackUsage(requestID, resolved.ModelID, fb.ProviderModelID, fb.ProviderName, fbResp.Usage, time.Since(start), fiber.StatusOK, false, nil)
			h.logRequestComplete(correlationID, requestID, &fb, fbLatency, fiber.StatusOK, false, nil)
			return c.JSON(fbResp)
		}
		if fb.Breaker != nil {
			fb.Breaker.RecordFailure()
		}
		h.metrics.IncrementRetries()
		h.recordModelResult(&fb, fbErr, fbLatency)
		h.logger.Warn("request:fallback_error",
			zap.String("correlation_id", correlationID),
			zap.String("request_id", requestID),
			zap.String("provider", fb.ProviderName),
			zap.Int64("latency_ms", fbLatency),
			zap.Error(fbErr),
		)
	}

	// All providers failed
	h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, nil, time.Since(start), fiber.StatusBadGateway, false, err)
	h.logRequestComplete(correlationID, requestID, resolved, time.Since(start).Milliseconds(), fiber.StatusBadGateway, false, err)
	return h.providerErrorResponse(c, err)
}

// handleStreaming handles a streaming chat completion request
func (h *Handler) handleStreaming(c *fiber.Ctx, req *apitypes.ChatCompletionRequest, resolved *router.ResolvedRoute, fallbacks []router.ResolvedRoute) error {
	start := time.Now()
	requestID := uuid.New().String()
	correlationID := middleware.GetCorrelationIDFromLocals(c)

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

	// Try primary provider
	if resolved.Breaker != nil && resolved.Breaker.Allow() != breaker.ResultAllowed {
		cancel()
		h.metrics.IncrementErrors()
		h.metrics.IncrementBreakerRejections()
		h.logger.Warn("request:breaker_rejected",
			zap.String("correlation_id", correlationID),
			zap.String("request_id", requestID),
			zap.String("provider", resolved.ProviderName),
		)
		return c.Status(fiber.StatusServiceUnavailable).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: fmt.Sprintf("provider '%s' circuit breaker is open", resolved.ProviderName),
				Type:    "provider_unavailable",
				Code:    "circuit_breaker_open",
			},
		})
	}
	ch, err := resolved.Provider.ChatCompletionStream(streamCtx, req)
	latency := time.Since(start).Milliseconds()
	if err == nil {
		if resolved.Breaker != nil {
			resolved.Breaker.RecordSuccess()
		}
		h.metrics.RecordProviderLatency(latency)
		h.metrics.RecordProviderLatencyForProvider(resolved.ProviderName, latency)
		h.recordModelResult(resolved, nil, latency)
		return h.streamResponse(c, ch, &streamSession{
			requestID:       requestID,
			correlationID:   correlationID,
			providerName:    resolved.ProviderName,
			modelID:         resolved.ModelID,
			providerModelID: resolved.ProviderModelID,
			start:           start,
			cancel:          cancel,
		})
	}
	if resolved.Breaker != nil {
		resolved.Breaker.RecordFailure()
	}
	h.metrics.IncrementErrors()
	h.recordModelResult(resolved, err, latency)
	h.logger.Warn("request:stream_provider_error",
		zap.String("correlation_id", correlationID),
		zap.String("request_id", requestID),
		zap.String("provider", resolved.ProviderName),
		zap.Int64("latency_ms", latency),
		zap.Error(err),
	)

	// Try fallbacks
	for i := range fallbacks {
		fb := fallbacks[i]
		fallbackReq := *req
		fallbackReq.Model = fb.ProviderModelID

		fbStart := time.Now()
		fbCh, fbErr := fb.Provider.ChatCompletionStream(streamCtx, &fallbackReq)
		fbLatency := time.Since(fbStart).Milliseconds()
		if fbErr == nil {
			if fb.Breaker != nil {
				fb.Breaker.RecordSuccess()
			}
			h.metrics.RecordProviderLatency(fbLatency)
			h.metrics.RecordProviderLatencyForProvider(fb.ProviderName, fbLatency)
			h.recordModelResult(&fb, nil, fbLatency)
			return h.streamResponse(c, fbCh, &streamSession{
				requestID:       requestID,
				correlationID:   correlationID,
				providerName:    fb.ProviderName,
				modelID:         fb.ModelID,
				providerModelID: fb.ProviderModelID,
				start:           start,
				cancel:          cancel,
			})
		}
		if fb.Breaker != nil {
			fb.Breaker.RecordFailure()
		}
		h.metrics.IncrementRetries()
		h.recordModelResult(&fb, fbErr, fbLatency)
		h.logger.Warn("request:stream_fallback_error",
			zap.String("correlation_id", correlationID),
			zap.String("request_id", requestID),
			zap.String("provider", fb.ProviderName),
			zap.Int64("latency_ms", fbLatency),
			zap.Error(fbErr),
		)
	}

	// All providers failed
	cancel()
	h.metrics.IncrementStreamErrors()
	h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, nil, time.Since(start), fiber.StatusBadGateway, true, err)
	h.logRequestComplete(correlationID, requestID, resolved, time.Since(start).Milliseconds(), fiber.StatusBadGateway, true, err)
	return h.providerErrorResponse(c, err)
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

// HandleListModels handles GET /v1/models
func (h *Handler) HandleListModels(c *fiber.Ctx) error {
	entries, err := h.catalog.List(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: "Failed to list models",
				Type:    "server_error",
				Code:    "catalog_error",
			},
		})
	}

	labels := h.catalog.DisplayLabels(entries)
	modelList := make([]apitypes.ModelInfo, 0, len(entries))
	for _, e := range entries {
		ownedBy := e.OwnedBy
		if ownedBy == "" {
			ownedBy = e.Provider
		}
		modelList = append(modelList, apitypes.ModelInfo{
			ID:      e.ModelID,
			Object:  "model",
			Created: h.startTime.Unix(),
			OwnedBy: ownedBy,
			Name:    labels[e.ModelID],
		})
	}

	return c.JSON(apitypes.ModelList{
		Object: "list",
		Data:   modelList,
	})
}

// HandleDashboardModels handles GET /api/models
// Query: include_unreachable=true returns the full catalog with reachability fields
// even when hide_unreachable would omit them from /v1/models.
func (h *Handler) HandleDashboardModels(c *fiber.Ctx) error {
	includeUnreachable := c.Query("include_unreachable") == "true" || c.Query("include_unreachable") == "1"

	var entries []catalog.Entry
	var err error
	if includeUnreachable {
		entries, err = h.catalog.ListAll(c.Context())
	} else {
		entries, err = h.catalog.List(c.Context())
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Failed to list models",
				"type":    "server_error",
				"code":    "catalog_error",
			},
		})
	}

	type modelRow struct {
		ModelID         string   `json:"model_id"`
		Name            string   `json:"name,omitempty"`
		Provider        string   `json:"provider"`
		ProviderModelID string   `json:"provider_model_id"`
		OwnedBy         string   `json:"owned_by,omitempty"`
		Reachable       *bool    `json:"reachable,omitempty"`
		State           string   `json:"state,omitempty"`
		LatencyMs       *int64   `json:"latency_ms,omitempty"`
		LastError       *string  `json:"last_error,omitempty"`
		CheckedAt       *string  `json:"checked_at,omitempty"`
		ErrorRate       *float64 `json:"error_rate,omitempty"`
		NextProbe       *string  `json:"next_probe,omitempty"`
	}

	labels := h.catalog.DisplayLabels(entries)
	rows := make([]modelRow, 0, len(entries))
	for _, e := range entries {
		row := modelRow{
			ModelID:         e.ModelID,
			Name:            labels[e.ModelID],
			Provider:        e.Provider,
			ProviderModelID: e.ProviderModelID,
			OwnedBy:         e.OwnedBy,
		}
		if h.modelStatus != nil {
			reachable, known := h.modelStatus.IsReachable(e.ModelID)
			r := reachable
			row.Reachable = &r
			if known {
				if st := h.modelStatus.Get(e.ModelID); st != nil {
					row.State = string(st.State)
					lat := st.LatencyMs
					row.LatencyMs = &lat
					if st.LastError != "" {
						errMsg := st.LastError
						row.LastError = &errMsg
					}
					checked := st.CheckedAt.UTC().Format(time.RFC3339)
					row.CheckedAt = &checked
					er := st.ErrorRate
					row.ErrorRate = &er
					if !st.NextProbeTime.IsZero() {
						np := st.NextProbeTime.UTC().Format(time.RFC3339)
						row.NextProbe = &np
					}
				}
			} else {
				row.State = string(health.StateUnknown)
			}
		}
		rows = append(rows, row)
	}
	return c.JSON(fiber.Map{"models": rows})
}

// HandleModelStatus handles GET /api/models/status — cached probe results only.
func (h *Handler) HandleModelStatus(c *fiber.Ctx) error {
	if h.modelStatus == nil {
		return c.JSON(fiber.Map{
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"models":    []health.StatusDetail{},
		})
	}
	return c.JSON(fiber.Map{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"models":    h.modelStatus.GetStatusDetails(),
	})
}

// HandleForceProbe handles POST /api/models/force-probe — admin re-probe of one model.
// Accepts model_id as query (?model_id=...) or JSON body {"model_id":"..."}.
func (h *Handler) HandleForceProbe(c *fiber.Ctx) error {
	if h.modelProber == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Model prober is not enabled",
				"type":    "server_error",
				"code":    "prober_unavailable",
			},
		})
	}

	modelID := c.Query("model_id")
	if modelID == "" {
		var body struct {
			ModelID string `json:"model_id"`
		}
		_ = c.BodyParser(&body)
		modelID = body.ModelID
	}
	if modelID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "model_id is required",
				"type":    "invalid_request_error",
				"code":    "invalid_request",
			},
		})
	}

	prev, next, err := h.modelProber.ForceProbe(modelID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fiber.Map{
				"message": err.Error(),
				"type":    "invalid_request_error",
				"code":    "force_probe_failed",
			},
		})
	}

	resp := fiber.Map{
		"model_id": modelID,
	}
	if prev != nil {
		resp["previous_state"] = prev.State
	} else {
		resp["previous_state"] = health.StateUnknown
	}
	if next != nil {
		resp["new_state"] = next.State
		resp["latency_ms"] = next.LatencyMs
		if next.LastError != "" {
			resp["error"] = next.LastError
		} else {
			resp["error"] = nil
		}
	}
	return c.JSON(resp)
}

// HandleEmbeddings handles POST /v1/embeddings
func (h *Handler) HandleEmbeddings(c *fiber.Ctx) error {
	var req apitypes.EmbeddingRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: "Invalid request body",
				Type:    "invalid_request_error",
				Code:    "invalid_request",
			},
		})
	}

	// Resolve route
	resolved, _, err := h.router.ResolveWithFallback(req.Model)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: fmt.Sprintf("Model '%s' not found", req.Model),
				Type:    "invalid_request_error",
				Param:   "model",
				Code:    "model_not_found",
			},
		})
	}

	req.Model = resolved.ProviderModelID
	start := time.Now()
	resp, err := resolved.Provider.Embeddings(c.Context(), &req)
	if err != nil {
		h.trackUsage(uuid.New().String(), resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, nil, time.Since(start), fiber.StatusBadGateway, false, err)
		return h.providerErrorResponse(c, err)
	}

	usageData := &apitypes.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	h.trackUsage(uuid.New().String(), resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, usageData, time.Since(start), fiber.StatusOK, false, nil)
	return c.JSON(resp)
}

// HandleHealth handles GET /health
func (h *Handler) HandleHealth(c *fiber.Ctx) error {
	return c.JSON(apitypes.HealthResponse{Status: "ok"})
}

// HandleProviderHealth handles GET /api/health
func (h *Handler) HandleProviderHealth(c *fiber.Ctx) error {
	providers := h.registry.All()
	healthStatuses := make([]apitypes.ProviderHealth, 0, len(providers))

	for _, p := range providers {
		status, err := p.HealthCheck(c.Context())
		ph := apitypes.ProviderHealth{
			Name:      p.Name(),
			CheckedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err == nil && status != nil {
			ph.Healthy = status.IsHealthy
			ph.LatencyMs = status.LatencyMs
			if status.LastError != "" {
				ph.LastError = &status.LastError
			}
		} else {
			ph.Healthy = false
			errMsg := err.Error()
			ph.LastError = &errMsg
		}
		healthStatuses = append(healthStatuses, ph)
	}

	return c.JSON(apitypes.ProviderHealthResponse{Providers: healthStatuses})
}

// HandleListProviders handles GET /api/providers
func (h *Handler) HandleListProviders(c *fiber.Ctx) error {
	type providerDetail struct {
		Name             string   `json:"name"`
		DisplayName      string   `json:"display_name"`
		Description      string   `json:"description,omitempty"`
		Enabled          bool     `json:"enabled"`
		Capabilities     []string `json:"capabilities"`
		Models           []string `json:"models,omitempty"`
		MaxContextLength int      `json:"max_context_length,omitempty"`
		BaseURL          string   `json:"base_url,omitempty"`
		RegistrationTime string   `json:"registration_time"`
	}

	providers := h.registry.All()
	info := make([]providerDetail, 0, len(providers))
	for _, p := range providers {
		meta, ok := h.registry.GetMetadata(p.Name())
		if !ok {
			meta = provider.GetMetadata(p)
		}
		regTime, _ := h.registry.GetRegistrationTime(p.Name())
		info = append(info, providerDetail{
			Name:             p.Name(),
			DisplayName:      meta.DisplayName,
			Description:      meta.Description,
			Enabled:          meta.Enabled,
			Capabilities:     meta.SupportedCapabilities(),
			Models:           meta.Models,
			MaxContextLength: meta.MaxContextLength,
			BaseURL:          meta.BaseURL,
			RegistrationTime: regTime.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"providers": info})
}

// UsageSummary is the response shape for GET /api/usage.
type UsageSummary struct {
	Total      usage.Bucket            `json:"total"`
	ByProvider map[string]usage.Bucket `json:"by_provider"`
	ByModel    map[string]usage.Bucket `json:"by_model"`
}

// HandleUsage handles GET /api/usage
func (h *Handler) HandleUsage(c *fiber.Ctx) error {
	if h.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Usage database not available",
				"type":    "server_error",
			},
		})
	}

	limit := defaultLimit(c.Query("limit"), 1000)
	var records []database.UsageRecord
	if err := h.db.DB.Order("created_at DESC").Limit(limit).Find(&records).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Failed to query usage",
				"type":    "server_error",
			},
		})
	}

	total, byProvider, byModel := usage.Aggregate(records)
	return c.JSON(UsageSummary{Total: total, ByProvider: byProvider, ByModel: byModel})
}

// HandleCosts handles GET /api/usage/costs
func (h *Handler) HandleCosts(c *fiber.Ctx) error {
	if h.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Usage database not available",
				"type":    "server_error",
			},
		})
	}

	limit := defaultLimit(c.Query("limit"), 1000)
	var records []database.UsageRecord
	if err := h.db.DB.Where("estimated_cost_usd IS NOT NULL").Order("created_at DESC").Limit(limit).Find(&records).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Failed to query costs",
				"type":    "server_error",
			},
		})
	}

	total, byProvider, byModel := usage.Aggregate(records)
	return c.JSON(fiber.Map{
		"total":       costBucket(total),
		"by_provider": costMap(byProvider),
		"by_model":    costMap(byModel),
	})
}

func costBucket(b usage.Bucket) fiber.Map {
	m := fiber.Map{
		"requests":          b.Requests,
		"prompt_tokens":     b.PromptTokens,
		"completion_tokens": b.CompletionTokens,
		"total_tokens":      b.TotalTokens,
	}
	if b.CostUSD != nil {
		m["cost_usd"] = *b.CostUSD
	} else {
		m["cost_usd"] = nil
	}
	return m
}

func costMap(m map[string]usage.Bucket) map[string]fiber.Map {
	out := make(map[string]fiber.Map, len(m))
	for k, v := range m {
		out[k] = costBucket(v)
	}
	return out
}

// HandleLogs handles GET /api/logs
func (h *Handler) HandleLogs(c *fiber.Ctx) error {
	if h.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Database not available",
				"type":    "server_error",
			},
		})
	}

	limit := defaultLimit(c.Query("limit"), 100)
	var logs []database.RequestLog
	if err := h.db.DB.Order("created_at DESC").Limit(limit).Find(&logs).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Failed to query logs",
				"type":    "server_error",
			},
		})
	}
	return c.JSON(fiber.Map{"logs": logs})
}

func defaultLimit(q string, fallback int) int {
	if q == "" {
		return fallback
	}
	n, err := strconv.Atoi(q)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// HandleConfig handles GET /api/config
func (h *Handler) HandleConfig(c *fiber.Ctx) error {
	// TODO: Return current config with secrets redacted
	return c.JSON(fiber.Map{
		"message": "Config endpoint - coming soon",
	})
}

// HandleReloadConfig handles PUT /api/config/reload
func (h *Handler) HandleReloadConfig(c *fiber.Ctx) error {
	if h.reloadFn == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Config reload is not configured",
				"type":    "server_error",
			},
		})
	}

	if err := h.reloadFn(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"message": err.Error(),
				"type":    "server_error",
			},
		})
	}

	return c.JSON(fiber.Map{
		"status":  "ok",
		"message": "Configuration reloaded successfully",
	})
}

// HandleMetrics handles GET /api/metrics — Prometheus-compatible metrics export.
func (h *Handler) HandleMetrics(c *fiber.Ctx) error {
	snap := h.metrics.Snapshot()
	output := metrics.ExportPrometheus(snap)
	c.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	return c.SendString(output)
}

// HandleCircuitBreakerStatus handles GET /api/circuit-breaker — per-provider breaker status.
func (h *Handler) HandleCircuitBreakerStatus(c *fiber.Ctx) error {
	pool := h.router.BreakerPool()
	if pool == nil {
		return c.JSON(fiber.Map{
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"breakers":  []fiber.Map{},
			"enabled":   false,
		})
	}
	stats := pool.Stats()
	rows := make([]fiber.Map, 0, len(stats))
	for name, s := range stats {
		row := fiber.Map{
			"provider":            name,
			"state":               s.State.String(),
			"consecutive_fails":   s.ConsecutiveFails,
			"consecutive_succ":    s.ConsecutiveSucc,
			"failure_threshold":   s.FailureThreshold,
			"success_threshold":   s.SuccessThreshold,
			"recovery_timeout_ms": s.RecoveryTimeout.Milliseconds(),
			"total_failures":      s.TotalFailures,
			"total_successes":     s.TotalSuccesses,
			"total_rejections":    s.TotalRejections,
			"total_opens":         s.TotalOpens,
		}
		if !s.OpenedAt.IsZero() {
			ts := s.OpenedAt.UTC().Format(time.RFC3339)
			row["opened_at"] = ts
		}
		rows = append(rows, row)
	}
	return c.JSON(fiber.Map{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"breakers":  rows,
		"enabled":   true,
	})
}

// trackUsage records usage data
func (h *Handler) trackUsage(requestID, modelID, providerModelID, provider string, usageData *apitypes.Usage, duration time.Duration, statusCode int, isStream bool, err error) {
	if h.usageTracker == nil {
		return
	}

	record := &usage.Record{
		RequestID:       requestID,
		ModelID:         modelID,
		ProviderModelID: providerModelID,
		Provider:        provider,
		Requests:        1,
		DurationMs:      duration.Milliseconds(),
		LatencyMs:       duration.Milliseconds(),
		StatusCode:      statusCode,
		IsStream:        isStream,
		CreatedAt:       time.Now(),
	}

	if usageData != nil {
		record.PromptTokens = usageData.PromptTokens
		record.CompletionTokens = usageData.CompletionTokens
		record.TotalTokens = usageData.TotalTokens
		h.metrics.RecordPromptTokens(usageData.PromptTokens)
		h.metrics.RecordCompletionTokens(usageData.CompletionTokens)
		h.metrics.RecordTotalTokens(usageData.TotalTokens)
	}

	if err != nil {
		errMsg := err.Error()
		record.ErrorMessage = &errMsg
	}

	h.usageTracker.Record(record)
}

// recordModelResult updates per-model reachability from live chat traffic.
func (h *Handler) recordModelResult(resolved *router.ResolvedRoute, err error, latencyMs int64) {
	if h.modelProber == nil || resolved == nil {
		return
	}
	catalogID := resolved.ProviderName + "/" + resolved.ProviderModelID
	h.modelProber.RecordLiveResult(catalogID, resolved.ProviderName, resolved.ProviderModelID, err, latencyMs)
}

// logRequestComplete logs the completion of a request with timing and outcome.
func (h *Handler) logRequestComplete(correlationID, requestID string, resolved *router.ResolvedRoute, latencyMs int64, statusCode int, isStream bool, err error) {
	fields := []zap.Field{
		zap.String("correlation_id", correlationID),
		zap.String("request_id", requestID),
		zap.Int("status_code", statusCode),
		zap.Int64("latency_ms", latencyMs),
		zap.Bool("stream", isStream),
	}
	if resolved != nil {
		fields = append(fields,
			zap.String("provider", resolved.ProviderName),
			zap.String("provider_model", resolved.ProviderModelID),
			zap.String("model", resolved.ModelID),
		)
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
		h.logger.Warn("request:complete", fields...)
	} else {
		h.logger.Info("request:complete", fields...)
	}
}

// providerErrorResponse returns a normalized error response
func (h *Handler) providerErrorResponse(c *fiber.Ctx, err error) error {
	if providerErr, ok := err.(*provider.ProviderError); ok {
		return c.Status(providerErr.StatusCode).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: providerErr.Message,
				Type:    providerErr.Type,
				Code:    providerErr.Type,
			},
		})
	}

	return c.Status(fiber.StatusBadGateway).JSON(apitypes.ErrorResponse{
		Error: apitypes.ErrorDetail{
			Message: "Provider returned an error",
			Type:    "provider_error",
			Code:    "provider_unavailable",
		},
	})
}

// logRoutingDecision logs a routing decision when intelligent routing is active.
func (h *Handler) logRoutingDecision(modelID, providerName string) {
	if h.routingEngine == nil {
		return
	}
	decision := h.routingEngine.GetDecision()
	if decision == nil {
		return
	}
	h.logger.Info("routing_decision",
		zap.String("model", modelID),
		zap.String("selected_provider", decision.SelectedProvider),
		zap.String("selected_model", decision.SelectedProviderID),
		zap.Int64("routing_duration_ms", decision.RoutingDurationMs),
	)
}

// HandleRouting handles GET /api/routing — intelligent routing scores and decisions.
func (h *Handler) HandleRouting(c *fiber.Ctx) error {
	if h.routingEngine == nil {
		return c.JSON(fiber.Map{
			"enabled": false,
			"message": "Intelligent routing is not enabled",
		})
	}
	capHint := router.CapabilityHint{}
	if modelID := c.Query("model"); modelID != "" {
		// Build a minimal request hint from the query model for capability scoring.
		capHint = router.ExtractCapabilityHint(&apitypes.ChatCompletionRequest{Model: modelID})
	}
	scores := h.routingEngine.GetProviderScores(capHint)
	weights := h.routingEngine.GetScorer().LoadWeights()
	return c.JSON(fiber.Map{
		"enabled": true,
		"weights": fiber.Map{
			"health":     weights.Health,
			"latency":    weights.Latency,
			"cost":       weights.Cost,
			"capability": weights.Capability,
		},
		"providers": scores,
	})
}

// buildCacheKey constructs a cache key from the request and resolved route.
func (h *Handler) buildCacheKey(req *apitypes.ChatCompletionRequest, resolved *router.ResolvedRoute) string {
	if h.cacheEngine == nil {
		return ""
	}
	messages := make([]interface{}, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = map[string]interface{}{
			"role":    m.Role,
			"content": m.Content,
		}
	}
	params := map[string]interface{}{
		"temperature":          req.Temperature,
		"top_p":                req.TopP,
		"max_tokens":           req.MaxTokens,
		"n":                    req.N,
		"stop":                 req.Stop,
		"presence_penalty":     req.PresencePenalty,
		"frequency_penalty":    req.FrequencyPenalty,
		"user":                 req.User,
		"seed":                 req.Seed,
		"response_format":      req.ResponseFormat,
		"reasoning":            req.Reasoning,
		"reasoning_effort":     req.ReasoningEffort,
		"include_reasoning":    req.IncludeReasoning,
		"thinking_budget":      req.ThinkingBudget,
		"chat_template_kwargs": req.ChatTemplateKwargs,
	}
	if len(req.Tools) > 0 {
		params["tools"] = req.Tools
	}
	if req.ToolChoice != nil {
		params["tool_choice"] = req.ToolChoice
	}
	return cache.BuildCacheKey(resolved.ProviderModelID, messages, params)
}

func truncateHash(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// HandleCacheStatus handles GET /api/cache — cache dashboard information.
func (h *Handler) HandleCacheStatus(c *fiber.Ctx) error {
	resp := fiber.Map{
		"enabled": false,
		"stats": fiber.Map{
			"hits":   0,
			"misses": 0,
		},
	}
	if h.cacheEngine == nil {
		return c.JSON(resp)
	}
	resp["enabled"] = h.cacheEngine.IsEnabled()
	s := h.cacheEngine.Stats()
	hitRate := 0.0
	missRate := 0.0
	if total := s.Hits + s.Misses; total > 0 {
		hitRate = float64(s.Hits) / float64(total)
		missRate = float64(s.Misses) / float64(total)
	}
	resp["stats"] = fiber.Map{
		"hits":            s.Hits,
		"misses":          s.Misses,
		"evictions":       s.Evictions,
		"current_entries": s.CurrentEntries,
		"hit_rate":        hitRate,
		"miss_rate":       missRate,
	}
	if h.metrics != nil {
		snap := h.metrics.Snapshot()
		resp["latency_avg_ms"] = h.cacheEngine.AverageLookupLatency().Milliseconds()
		lookupCount := snap.CacheLookupLatency.Count
		resp["lookup_latency_ms"] = fiber.Map{
			"avg": func() float64 {
				if lookupCount == 0 {
					return 0
				}
				return snap.CacheLookupLatency.Sum / float64(lookupCount)
			}(),
			"min": snap.CacheLookupLatency.Min,
			"max": snap.CacheLookupLatency.Max,
		}
		storeCount := snap.CacheStoreLatency.Count
		resp["store_latency_ms"] = fiber.Map{
			"avg": func() float64 {
				if storeCount == 0 {
					return 0
				}
				return snap.CacheStoreLatency.Sum / float64(storeCount)
			}(),
			"min": snap.CacheStoreLatency.Min,
			"max": snap.CacheStoreLatency.Max,
		}
	}
	return c.JSON(resp)
}

// HandleStreamStatus handles GET /api/streams — the streaming dashboard.
func (h *Handler) HandleStreamStatus(c *fiber.Ctx) error {
	snap := h.metrics.Snapshot()
	avg := func(hist metrics.MetricHistogram) float64 {
		if hist.Count == 0 {
			return 0
		}
		return hist.Sum / float64(hist.Count)
	}

	type providerRow struct {
		StreamsStarted    int64   `json:"streams_started"`
		StreamsCompleted  int64   `json:"streams_completed"`
		StreamsCancelled  int64   `json:"streams_cancelled"`
		StreamsTimeout    int64   `json:"streams_timeout"`
		StreamsErrors     int64   `json:"streams_errors"`
		ChunksTotal       int64   `json:"chunks_total"`
		BytesTotal        int64   `json:"bytes_total"`
		AverageDurationMs float64 `json:"average_duration_ms"`
		AverageChunks     float64 `json:"average_chunks"`
		AverageBytes      float64 `json:"average_bytes"`
	}

	providers := make(map[string]providerRow, len(snap.StreamStatsByProvider))
	for name, s := range snap.StreamStatsByProvider {
		providers[name] = providerRow{
			StreamsStarted:    s.Started,
			StreamsCompleted:  s.Completed,
			StreamsCancelled:  s.Cancelled,
			StreamsTimeout:    s.Timeout,
			StreamsErrors:     s.Errors,
			ChunksTotal:       s.Chunks,
			BytesTotal:        s.Bytes,
			AverageDurationMs: avg(s.Duration),
			AverageChunks:     avg(s.ChunksPerStream),
			AverageBytes:      avg(s.BytesPerStream),
		}
	}

	return c.JSON(fiber.Map{
		"timestamp":           time.Now().UTC().Format(time.RFC3339),
		"active_streams":      snap.ActiveStreams,
		"streams_started":     snap.StreamStarted,
		"streams_completed":   snap.StreamCompleted,
		"streams_cancelled":   snap.StreamCancelled,
		"streams_timeout":     snap.StreamTimeout,
		"streams_errors":      snap.StreamErrorsTotal,
		"chunks_total":        snap.StreamChunksTotal,
		"bytes_total":         snap.StreamBytesTotal,
		"average_duration_ms": avg(snap.StreamDurationMs),
		"average_chunks":      avg(snap.StreamChunks),
		"average_bytes":       avg(snap.StreamBytes),
		"providers":           providers,
	})
}
