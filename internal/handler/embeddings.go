package handler

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/resilience"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// embeddingsWinner carries the winning response out of the executor's
// candidate closures.
type embeddingsWinner struct {
	resp *apitypes.EmbeddingResponse
}

// HandleEmbeddings handles POST /v1/embeddings with the same failover
// guarantees as chat: primary route, then static fallbacks, then dynamic
// category-matched alternates. The request only fails when no candidate can
// serve it.
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

	// model="auto" is not supported for embeddings - embeddings require a concrete model.
	if req.Model == "auto" {
		return c.Status(fiber.StatusBadRequest).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: "model 'auto' is not supported for embeddings; specify a concrete embedding model",
				Type:    "invalid_request_error",
				Param:   "model",
				Code:    "model_not_supported",
			},
		})
	}

	resolved, fallbacks, err := h.router.ResolveWithFallback(req.Model)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: "Model '" + req.Model + "' not found",
				Type:    "invalid_request_error",
				Param:   "model",
				Code:    "model_not_found",
			},
		})
	}

	// Dynamic tail mirrors the chat path; embedding requests are always in
	// the default category (no vision/mode filters apply).
	routes := make([]*router.ResolvedRoute, 0, 1+len(fallbacks)+1)
	routes = append(routes, resolved)
	for i := range fallbacks {
		routes = append(routes, &fallbacks[i])
	}
	if dyn := h.dynamicFallbackRoutes(c.Context(), &apitypes.ChatCompletionRequest{Model: req.Model}, resolved, fallbacks); len(dyn) > 0 {
		for i := range dyn {
			routes = append(routes, &dyn[i])
		}
	}

	requestID := uuid.New().String()
	start := time.Now()
	win := &embeddingsWinner{}
	candidates := make([]resilience.Candidate, 0, len(routes))
	for _, route := range routes {
		route := route
		candidates = append(candidates, resilience.Candidate{
			Index:           len(candidates),
			ProviderName:    route.ProviderName,
			ModelID:         route.ModelID,
			ProviderModelID: route.ProviderModelID,
			Breaker:         route.Breaker,
			Op: func(ctx context.Context) error {
				attempt := req
				attempt.Model = route.ProviderModelID
				resp, err := route.Provider.Embeddings(ctx, &attempt)
				if err != nil {
					return err
				}
				win.resp = resp
				return nil
			},
		})
	}

	res := resilience.ExecutePlan(c.Context(), resilience.Plan{
		Candidates: candidates,
		Retry:      h.retryPolicy(),
	})

	if res.WinnerIndex >= 0 {
		winner := routes[res.WinnerIndex]
		usageData := &apitypes.Usage{}
		if win.resp.Usage != nil {
			usageData.PromptTokens = win.resp.Usage.PromptTokens
			usageData.CompletionTokens = win.resp.Usage.CompletionTokens
			usageData.TotalTokens = win.resp.Usage.TotalTokens
		}
		h.trackUsage(requestID, resolved.ModelID, winner.ProviderModelID, winner.ProviderName, usageData, time.Since(start), fiber.StatusOK, false, nil)
		return c.JSON(win.resp)
	}

	if res.FirstBlocked && !res.AttemptedAny {
		return c.Status(fiber.StatusServiceUnavailable).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: "provider '" + resolved.ProviderName + "' circuit breaker is open",
				Type:    "provider_unavailable",
				Code:    "circuit_breaker_open",
			},
		})
	}

	h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, nil, time.Since(start), fiber.StatusBadGateway, false, res.LastError)
	return h.providerErrorResponse(c, res.LastError)
}
