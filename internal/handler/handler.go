package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/catalog"
	"github.com/novexa/gateway/internal/database"
	"github.com/novexa/gateway/internal/health"
	"github.com/novexa/gateway/internal/provider"
	"github.com/novexa/gateway/internal/router"
	"github.com/novexa/gateway/internal/usage"
	"go.uber.org/zap"
)

// Handler holds HTTP handlers for the gateway
type Handler struct {
	router       *router.Engine
	registry     *provider.Registry
	catalog      *catalog.Catalog
	usageTracker *usage.Tracker
	db           *database.Database
	logger       *zap.Logger
	startTime    time.Time
	reloadFn     func() error
	modelProber  *health.ModelProber
	modelStatus  *health.ModelStatusStore
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
	app.Get("/api/health", h.HandleProviderHealth)
	app.Get("/api/providers", h.HandleListProviders)
	app.Get("/api/usage", h.HandleUsage)
	app.Get("/api/usage/costs", h.HandleCosts)
	app.Get("/api/logs", h.HandleLogs)
	app.Get("/api/config", h.HandleConfig)
	app.Put("/api/config/reload", h.HandleReloadConfig)
}

// HandleChatCompletion handles POST /v1/chat/completions
func (h *Handler) HandleChatCompletion(c *fiber.Ctx) error {
	var req apitypes.ChatCompletionRequest
	if err := c.BodyParser(&req); err != nil {
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
		return c.Status(fiber.StatusBadRequest).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: "messages is required",
				Type:    "invalid_request_error",
				Param:   "messages",
				Code:    "invalid_request",
			},
		})
	}

	// Resolve route
	resolved, fallbacks, err := h.router.ResolveWithFallback(req.Model)
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

	// Override model name if needed
	req.Model = resolved.ProviderModelID

	// Handle streaming
	if req.Stream {
		return h.handleStreaming(c, &req, resolved, fallbacks)
	}

	// Handle non-streaming
	return h.handleNonStreaming(c, &req, resolved, fallbacks)
}

// handleNonStreaming handles a non-streaming chat completion request
func (h *Handler) handleNonStreaming(c *fiber.Ctx, req *apitypes.ChatCompletionRequest, resolved *router.ResolvedRoute, fallbacks []router.ResolvedRoute) error {
	start := time.Now()
	requestID := uuid.New().String()

	// Try primary provider
	resp, err := resolved.Provider.ChatCompletion(c.Context(), req)
	if err == nil {
		h.recordModelResult(resolved, nil, time.Since(start).Milliseconds())
		h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, resp.Usage, time.Since(start), fiber.StatusOK, false, nil)
		return c.JSON(resp)
	}
	h.recordModelResult(resolved, err, time.Since(start).Milliseconds())

	// Try fallbacks
	for i := range fallbacks {
		fb := fallbacks[i]
		fallbackReq := *req
		fallbackReq.Model = fb.ProviderModelID

		fbStart := time.Now()
		fbResp, fbErr := fb.Provider.ChatCompletion(c.Context(), &fallbackReq)
		if fbErr == nil {
			h.recordModelResult(&fb, nil, time.Since(fbStart).Milliseconds())
			h.trackUsage(requestID, resolved.ModelID, fb.ProviderModelID, fb.ProviderName, fbResp.Usage, time.Since(start), fiber.StatusOK, false, nil)
			return c.JSON(fbResp)
		}
		h.recordModelResult(&fb, fbErr, time.Since(fbStart).Milliseconds())
	}

	// All providers failed
	h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, nil, time.Since(start), fiber.StatusBadGateway, false, err)
	return h.providerErrorResponse(c, err)
}

// handleStreaming handles a streaming chat completion request
func (h *Handler) handleStreaming(c *fiber.Ctx, req *apitypes.ChatCompletionRequest, resolved *router.ResolvedRoute, fallbacks []router.ResolvedRoute) error {
	start := time.Now()
	requestID := uuid.New().String()

	// Ask upstreams for a final usage chunk (many omit it unless requested).
	req.EnsureStreamUsage()

	// Set SSE headers
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	// Try primary provider
	ch, err := resolved.Provider.ChatCompletionStream(c.Context(), req)
	if err == nil {
		h.recordModelResult(resolved, nil, time.Since(start).Milliseconds())
		return h.streamResponse(c, ch, requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, start)
	}
	h.recordModelResult(resolved, err, time.Since(start).Milliseconds())

	// Try fallbacks
	for i := range fallbacks {
		fb := fallbacks[i]
		fallbackReq := *req
		fallbackReq.Model = fb.ProviderModelID

		fbStart := time.Now()
		fbCh, fbErr := fb.Provider.ChatCompletionStream(c.Context(), &fallbackReq)
		if fbErr == nil {
			h.recordModelResult(&fb, nil, time.Since(fbStart).Milliseconds())
			return h.streamResponse(c, fbCh, requestID, resolved.ModelID, fb.ProviderModelID, fb.ProviderName, start)
		}
		h.recordModelResult(&fb, fbErr, time.Since(fbStart).Milliseconds())
	}

	// All providers failed
	h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, nil, time.Since(start), fiber.StatusBadGateway, true, err)
	return h.providerErrorResponse(c, err)
}

// streamResponse writes streaming chunks to the response
func (h *Handler) streamResponse(c *fiber.Ctx, ch <-chan apitypes.StreamChunk, requestID, modelID, providerModelID, providerName string, start time.Time) error {
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		var usageData *apitypes.Usage
		var sawContent bool
		var reasoningBuf string
		var lastMeta apitypes.StreamChunk
		sentDone := false

		writeChunk := func(chunk apitypes.StreamChunk) {
			data, _ := json.Marshal(chunk)
			_, _ = w.Write([]byte(fmt.Sprintf("data: %s\n\n", data)))
			_ = w.Flush()
		}

		accumulateMessage := func(m *apitypes.Message) {
			if m == nil {
				return
			}
			if m.Content != "" {
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
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			_ = w.Flush()
			sentDone = true
		}

		for chunk := range ch {
			if chunk.Error != nil {
				h.logger.Error("streaming error",
					zap.String("provider", providerName),
					zap.Error(chunk.Error),
				)
				break
			}

			if chunk.Done {
				finishStream()
				break
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

			writeChunk(chunk)

			if chunk.Usage != nil {
				usageData = chunk.Usage
			}
		}

		// Upstream timeout or truncated body may close the channel without [DONE].
		finishStream()

		h.trackUsage(requestID, modelID, providerModelID, providerName, usageData, time.Since(start), fiber.StatusOK, true, nil)
	})

	return nil
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
		ModelID         string  `json:"model_id"`
		Provider        string  `json:"provider"`
		ProviderModelID string  `json:"provider_model_id"`
		OwnedBy         string  `json:"owned_by,omitempty"`
		Reachable       *bool   `json:"reachable,omitempty"`
		LatencyMs       *int64  `json:"latency_ms,omitempty"`
		LastError       *string `json:"last_error,omitempty"`
		CheckedAt       *string `json:"checked_at,omitempty"`
	}

	rows := make([]modelRow, 0, len(entries))
	for _, e := range entries {
		row := modelRow{
			ModelID:         e.ModelID,
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
					lat := st.LatencyMs
					row.LatencyMs = &lat
					if st.LastError != "" {
						errMsg := st.LastError
						row.LastError = &errMsg
					}
					checked := st.CheckedAt.UTC().Format(time.RFC3339)
					row.CheckedAt = &checked
				}
			}
		}
		rows = append(rows, row)
	}
	return c.JSON(fiber.Map{"models": rows})
}

// HandleModelStatus handles GET /api/models/status — cached probe results only.
func (h *Handler) HandleModelStatus(c *fiber.Ctx) error {
	if h.modelStatus == nil {
		return c.JSON(fiber.Map{"models": []health.ModelStatus{}})
	}
	return c.JSON(fiber.Map{"models": h.modelStatus.GetAll()})
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
	providers := h.registry.All()
	type providerInfo struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	info := make([]providerInfo, 0, len(providers))
	for _, p := range providers {
		info = append(info, providerInfo{Name: p.Name(), Enabled: true})
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
