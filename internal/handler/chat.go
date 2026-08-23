package handler

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/middleware"
	"github.com/EffNine/conductor/internal/resilience"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// HandleAutoStatus reports the runtime auto model selection status.
func (h *Handler) HandleAutoStatus(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"enabled": h.router.HasAutoSelector(),
		"note":    "auto mode selects from all registered providers using health, cost, and latency scoring",
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

	// Validate explicit mode if provided.
	if req.Mode != "" {
		if _, err := router.ParseMode(req.Mode); err != nil {
			h.metrics.IncrementErrors()
			return c.Status(fiber.StatusBadRequest).JSON(apitypes.ErrorResponse{
				Error: apitypes.ErrorDetail{
					Message: err.Error(),
					Type:    "invalid_request_error",
					Param:   "mode",
					Code:    "invalid_request",
				},
			})
		}
	}

	// Handle virtual models (frontier, coding, reasoning, agentic, planning,
	// long_horizon, fast, light, vision, auto) with the catalog-backed virtual resolver.
	// These are first-class virtual models: they work regardless of routing.enabled
	// (the resolver is injected independently of the routing engine / DecisionPipeline).
	var resolved *router.ResolvedRoute
	var fallbacks []router.ResolvedRoute
	var err error

	if router.IsVirtualModel(req.Model) && h.virtualResolver != nil {
		vm, parseErr := router.ParseVirtualModel(req.Model)
		if parseErr != nil {
			h.metrics.IncrementErrors()
			return c.Status(fiber.StatusBadRequest).JSON(apitypes.ErrorResponse{
				Error: apitypes.ErrorDetail{
					Message: parseErr.Error(),
					Type:    "invalid_request_error",
					Param:   "model",
					Code:    "invalid_virtual_model",
				},
			})
		}
		selection, selErr := h.virtualResolver.Resolve(c.Context(), vm, &req)
		if selErr != nil {
			h.metrics.IncrementErrors()
			h.logger.Warn("virtual model selection failed",
				zap.String("correlation_id", middleware.GetCorrelationIDFromLocals(c)),
				zap.String("virtual_model", req.Model),
				zap.Error(selErr),
			)
			return c.Status(fiber.StatusNotFound).JSON(apitypes.ErrorResponse{
				Error: apitypes.ErrorDetail{
					Message: selErr.Error(),
					Type:    "invalid_request_error",
					Param:   "model",
					Code:    "virtual_selection_failed",
				},
			})
		}
		if selection == nil || selection.Candidate == nil {
			h.metrics.IncrementErrors()
			return c.Status(fiber.StatusNotFound).JSON(apitypes.ErrorResponse{
				Error: apitypes.ErrorDetail{
					Message: fmt.Sprintf("no eligible model for virtual model '%s'", req.Model),
					Type:    "invalid_request_error",
					Param:   "model",
					Code:    "no_model_available",
				},
			})
		}
		p, found := h.registry.Get(selection.Candidate.ProviderName)
		if !found {
			h.metrics.IncrementErrors()
			return c.Status(fiber.StatusInternalServerError).JSON(apitypes.ErrorResponse{
				Error: apitypes.ErrorDetail{
					Message: fmt.Sprintf("selected provider '%s' not registered", selection.Candidate.ProviderName),
					Type:    "server_error",
					Code:    "provider_not_found",
				},
			})
		}
		resolved = &router.ResolvedRoute{
			Provider:        p,
			ProviderName:    selection.Candidate.ProviderName,
			ProviderModelID: selection.Candidate.ProviderModelID,
			ModelID:         req.Model, // Preserve virtual model ID for traceability
			Breaker:         h.router.BreakerPool().Get(selection.Candidate.ProviderName),
		}
		fallbacks = nil
	} else {
		// Legacy Engine normalization: model/alias/route resolution and candidate construction.
		resolved, fallbacks, err = h.router.ResolveWithFallbackAndContext(c.Context(), req.Model, req.Messages)
		if err != nil {
			// Legacy engine could not resolve. If a decision pipeline or routing engine
			// is available, fall back to auto-selection across all registered providers.
			if h.decisionPipeline != nil || h.routingEngine != nil {
				var selection *router.SelectionResult
				if h.decisionPipeline != nil {
					cfgSnap := h.buildConfigSnapshot()
					env := router.Environment{
						CircuitBreakerEnabled: h.router.BreakerPool() != nil,
					}
					result, pplineErr := h.decisionPipeline.Execute(c.Context(), &req, env, cfgSnap, nil)
					if pplineErr == nil && result != nil && result.Candidate != nil {
						selection = result
					}
				}
				if selection == nil && h.routingEngine != nil {
					sel, selErr := h.routingEngine.SelectBestProvider(c.Context(), req.Model, &req)
					if selErr == nil && sel != nil {
						selection = sel
					}
				}
				if selection != nil && selection.Candidate != nil {
					p, found := h.registry.Get(selection.Candidate.ProviderName)
					if found {
						resolved = &router.ResolvedRoute{
							Provider:        p,
							ProviderName:    selection.Candidate.ProviderName,
							ProviderModelID: selection.Candidate.ProviderModelID,
							ModelID:         req.Model,
							Breaker:         h.router.BreakerPool().Get(selection.Candidate.ProviderName),
						}
						fallbacks = nil
					}
				}
			}
			if resolved == nil {
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
		}
	}

	// Append dynamic, category-matched alternates after the primary route
	// and static fallback chain (no-op when disabled or unavailable).
	staticRoutes := 1 + len(fallbacks)
	if dyn := h.dynamicFallbackRoutes(c.Context(), &req, resolved, fallbacks); len(dyn) > 0 {
		fallbacks = append(fallbacks, dyn...)
	}

	// Build candidate set from primary route + configured fallbacks.
	candidates := make([]router.ResolvedRoute, 0, 1+len(fallbacks))
	candidates = append(candidates, *resolved)
	candidates = append(candidates, fallbacks...)

	// DecisionPipeline: intent → capability → candidate → RouterEngine selection.
	if h.decisionPipeline != nil {
		cfgSnap := h.buildConfigSnapshot()
		env := router.Environment{
			CircuitBreakerEnabled: h.router.BreakerPool() != nil,
		}
		result, pplineErr := h.decisionPipeline.Execute(c.Context(), &req, env, cfgSnap, candidates)
		if pplineErr != nil {
			h.logger.Warn("decision pipeline failed, using legacy resolution",
				zap.String("correlation_id", middleware.GetCorrelationIDFromLocals(c)),
				zap.String("model", req.Model),
				zap.Error(pplineErr),
			)
		} else if result != nil && result.Candidate != nil {
			// Map the pipeline selection back to a ResolvedRoute in the candidate set.
			for i := range candidates {
				if candidates[i].ProviderName == result.Candidate.ProviderName &&
					candidates[i].ProviderModelID == result.Candidate.ProviderModelID {
					resolved = &candidates[i]
					break
				}
			}
			// Rebuild fallbacks: keep remaining candidates in their original order.
			// Identity is (provider, model): same-provider alternates from the
			// dynamic tail must survive so single-provider setups still fail over.
			newFallbacks := make([]router.ResolvedRoute, 0, len(fallbacks))
			for _, fb := range fallbacks {
				if fb.ProviderName != resolved.ProviderName ||
					fb.ProviderModelID != resolved.ProviderModelID {
					newFallbacks = append(newFallbacks, fb)
				}
			}
			fallbacks = newFallbacks
		}
	} else if h.routingEngine != nil {
		// Legacy path: RouterEngine selects from pre-resolved candidates.
		selection, selErr := h.routingEngine.SelectFromRoutes(c.Context(), candidates, &req)
		if selErr == nil && selection != nil && selection.Candidate != nil {
			for i := range candidates {
				if candidates[i].ProviderName == selection.Candidate.ProviderName &&
					candidates[i].ProviderModelID == selection.Candidate.ProviderModelID {
					resolved = &candidates[i]
					break
				}
			}
			// Identity is (provider, model): same-provider alternates from the
			// dynamic tail must survive so single-provider setups still fail over.
			newFallbacks := make([]router.ResolvedRoute, 0, len(fallbacks))
			for _, fb := range fallbacks {
				if fb.ProviderName != resolved.ProviderName ||
					fb.ProviderModelID != resolved.ProviderModelID {
					newFallbacks = append(newFallbacks, fb)
				}
			}
			fallbacks = newFallbacks
		}
	}

	// Log routing decision if intelligent routing is active.
	if h.decisionPipeline != nil || h.routingEngine != nil {
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
		return h.handleStreaming(c, &req, resolved, fallbacks, staticRoutes)
	}

	// Handle non-streaming
	return h.handleNonStreaming(c, &req, resolved, fallbacks, staticRoutes)
}

// buildConfigSnapshot exports the legacy engine's routing config for the pipeline.
func (h *Handler) buildConfigSnapshot() router.ConfigSnapshot {
	return h.router.BuildConfigSnapshot()
}

// handleNonStreaming handles a non-streaming chat completion request
func (h *Handler) handleNonStreaming(c *fiber.Ctx, req *apitypes.ChatCompletionRequest, resolved *router.ResolvedRoute, fallbacks []router.ResolvedRoute, staticRoutes int) error {
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
	// P4.4.1: the candidate chain (primary + configured fallbacks) is
	// orchestrated by the shared resilience executor. Ordering, breaker
	// gating, retry semantics and outcome accounting are preserved exactly;
	// only post-winner handling stays here (cache + JSON response).
	policy := h.retryPolicy()

	routes := make([]*router.ResolvedRoute, 0, 1+len(fallbacks))
	routes = append(routes, resolved)
	for i := range fallbacks {
		routes = append(routes, &fallbacks[i])
	}

	win := &chatWinner{}
	sink := &chatPlanSink{
		h:             h,
		requestID:     requestID,
		correlationID: correlationID,
		mode:          req.Mode,
		start:         start,
		routes:        routes,
		staticRoutes:  staticRoutes,
		usageModelID:  resolved.ModelID,
	}
	plan := resilience.Plan{
		Candidates:      buildChatCandidates(req, routes, win),
		Retry:           policy,
		Sink:            sink,
		Budget:          h.executionBudget(),
		EstimatedTokens: int64(router.EstimateRequestTokens(req)),
	}
	res := resilience.ExecutePlan(c.Context(), plan)

	// Winner handling: usage, completion log, cache store (primary only,
	// as before), then the JSON response.
	if res.WinnerIndex >= 0 {
		winner := routes[res.WinnerIndex]
		resp := win.resp
		h.trackUsage(requestID, sink.usageModelID, winner.ProviderModelID, winner.ProviderName, resp.Usage, time.Since(start), fiber.StatusOK, false, nil)
		h.logRequestComplete(correlationID, requestID, winner, sink.lastDurationMs, fiber.StatusOK, false, nil)
		if res.WinnerIndex == 0 && h.cacheEngine != nil && h.cacheEngine.IsEnabled() {
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
					zap.String("provider", winner.ProviderName),
					zap.String("model", req.Model),
					zap.String("cache_key", truncateHash(cacheKey, 8)),
				)
			}
		}
		return c.JSON(resp)
	}

	// Legacy open-primary contract: the primary was breaker-blocked and no
	// eligible candidate could serve.
	if res.FirstBlocked && !res.AttemptedAny {
		return c.Status(fiber.StatusServiceUnavailable).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: fmt.Sprintf("provider '%s' circuit breaker is open", resolved.ProviderName),
				Type:    "provider_unavailable",
				Code:    "circuit_breaker_open",
			},
		})
	}

	// All providers failed
	h.trackUsage(requestID, resolved.ModelID, resolved.ProviderModelID, resolved.ProviderName, nil, time.Since(start), fiber.StatusBadGateway, false, res.LastError)
	h.logRequestComplete(correlationID, requestID, resolved, time.Since(start).Milliseconds(), fiber.StatusBadGateway, false, res.LastError)
	return h.providerErrorResponse(c, res.LastError)
}

// HandleEmbeddings handles POST /v1/embeddings
