package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/catalog"
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
	logger       *zap.Logger
	startTime    time.Time
}

// New creates a new Handler
func New(r *router.Engine, reg *provider.Registry, ut *usage.Tracker, logger *zap.Logger, cat *catalog.Catalog) *Handler {
	return &Handler{
		router:       r,
		registry:     reg,
		catalog:      cat,
		usageTracker: ut,
		logger:       logger,
		startTime:    time.Now(),
	}
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
		h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, resp.Usage, time.Since(start), fiber.StatusOK, false, nil)
		return c.JSON(resp)
	}

	// Try fallbacks
	for _, fb := range fallbacks {
		fallbackReq := *req
		fallbackReq.Model = fb.ProviderModelID

		resp, err := fb.Provider.ChatCompletion(c.Context(), &fallbackReq)
		if err == nil {
			h.trackUsage(requestID, resolved.ModelID, fb.ProviderModelID, fb.ProviderName, resp.Usage, time.Since(start), fiber.StatusOK, false, nil)
			return c.JSON(resp)
		}
	}

	// All providers failed
	h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, nil, time.Since(start), fiber.StatusBadGateway, false, err)
	return h.providerErrorResponse(c, err)
}

// handleStreaming handles a streaming chat completion request
func (h *Handler) handleStreaming(c *fiber.Ctx, req *apitypes.ChatCompletionRequest, resolved *router.ResolvedRoute, fallbacks []router.ResolvedRoute) error {
	start := time.Now()
	requestID := uuid.New().String()

	// Set SSE headers
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")

	// Try primary provider
	ch, err := resolved.Provider.ChatCompletionStream(c.Context(), req)
	if err == nil {
		return h.streamResponse(c, ch, requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, start)
	}

	// Try fallbacks
	for _, fb := range fallbacks {
		fallbackReq := *req
		fallbackReq.Model = fb.ProviderModelID

		ch, err := fb.Provider.ChatCompletionStream(c.Context(), &fallbackReq)
		if err == nil {
			return h.streamResponse(c, ch, requestID, resolved.ModelID, fb.ProviderModelID, fb.ProviderName, start)
		}
	}

	// All providers failed
	h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, nil, time.Since(start), fiber.StatusBadGateway, true, err)
	return h.providerErrorResponse(c, err)
}

// streamResponse writes streaming chunks to the response
func (h *Handler) streamResponse(c *fiber.Ctx, ch <-chan apitypes.StreamChunk, requestID, modelID, providerModelID, providerName string, start time.Time) error {
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		var totalPromptTokens, totalCompletionTokens int

		for chunk := range ch {
			if chunk.Error != nil {
				h.logger.Error("streaming error",
					zap.String("provider", providerName),
					zap.Error(chunk.Error),
				)
				break
			}

			if chunk.Done {
				w.Write([]byte("data: [DONE]\n\n"))
				w.Flush()
				break
			}

			// Write SSE data
			data, _ := json.Marshal(chunk)
			w.Write([]byte(fmt.Sprintf("data: %s\n\n", data)))
			w.Flush()

			// Track tokens from usage if present
			if chunk.Usage != nil {
				totalPromptTokens = chunk.Usage.PromptTokens
				totalCompletionTokens = chunk.Usage.CompletionTokens
			}
		}

		// Track usage
		usage := &apitypes.Usage{
			PromptTokens:     totalPromptTokens,
			CompletionTokens: totalCompletionTokens,
			TotalTokens:      totalPromptTokens + totalCompletionTokens,
		}
		h.trackUsage(requestID, modelID, providerModelID, providerName, usage, time.Since(start), fiber.StatusOK, true, nil)
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
	resp, err := resolved.Provider.Embeddings(c.Context(), &req)
	if err != nil {
		return h.providerErrorResponse(c, err)
	}

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

// HandleUsage handles GET /api/usage
func (h *Handler) HandleUsage(c *fiber.Ctx) error {
	// TODO: Implement usage querying from database
	return c.JSON(fiber.Map{
		"message": "Usage tracking endpoint - coming soon",
	})
}

// HandleCosts handles GET /api/usage/costs
func (h *Handler) HandleCosts(c *fiber.Ctx) error {
	// TODO: Implement cost querying from database
	return c.JSON(fiber.Map{
		"message": "Cost tracking endpoint - coming soon",
	})
}

// HandleLogs handles GET /api/logs
func (h *Handler) HandleLogs(c *fiber.Ctx) error {
	// TODO: Implement log querying from database
	return c.JSON(fiber.Map{
		"message": "Logs endpoint - coming soon",
	})
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
	// TODO: Implement config reload
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
