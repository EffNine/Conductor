package handler

import (
	"time"
	"unicode"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/cache"
	"github.com/EffNine/conductor/internal/metrics"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
)

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
//
// Cache identity contract (P3.12):
//   - Model dimension: the RESOLVED provider model ID (upstream slug), not the
//     request alias — two aliases resolving to the same provider/model share a
//     key, while different resolved models never collide.
//   - Provider dimension: the resolved provider name participates in the key.
//     Two requests that can legitimately execute against different providers
//     (explicit routes, provider prefixes, aliases resolving to different
//     providers, fallback selection changes, auto mode) must never share a
//     cache entry, because provider-specific responses can differ.
//   - Mode dimension: the CANONICAL mode (NormalizeMode) participates in the
//     key so equivalent spellings ("coding" vs "Coding") share a key while
//     different routing identities do not. Omitted mode (empty) stays distinct
//     from explicit "auto" — they are distinct inputs at the API boundary.
//   - Request dimensions: routing-relevant parameters (tools, reasoning,
//     response_format, multimodal content, sampling params) participate via
//     the params map.
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
		"mode":                 router.NormalizeMode(req.Mode),
	}
	if resolved != nil {
		params["provider"] = resolved.ProviderName
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

// HandleRuntime handles GET /api/runtime — returns provider runtime state.
func (h *Handler) HandleRuntime(c *fiber.Ctx) error {
	if h.runtimeManager == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "runtime subsystem not initialized",
		})
	}
	snap := h.runtimeManager.Snapshot(c.Context())
	providers := make([]fiber.Map, 0, len(snap.Providers))
	for name, ps := range snap.Providers {
		providers = append(providers, fiber.Map{
			"name":              name,
			"state":             string(ps.State),
			"latency_ms":        ps.LatencyMs,
			"error_rate":        ps.ErrorRate,
			"capacity":          ps.Capacity,
			"last_health_check": ps.LastHealthCheck,
		})
	}
	return c.JSON(fiber.Map{
		"timestamp": snap.Timestamp.Format(time.RFC3339),
		"global": fiber.Map{
			"total_providers":     snap.GlobalState.TotalProviders,
			"healthy_providers":   snap.GlobalState.HealthyProviders,
			"degraded_providers":  snap.GlobalState.DegradedProviders,
			"unhealthy_providers": snap.GlobalState.UnhealthyProviders,
			"avg_latency_ms":      snap.GlobalState.AvgLatencyMs,
		},
		"providers": providers,
	})
}

// capitalize returns s with the first rune upper-cased.
func capitalize(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return s
	}
	return string(append([]rune{unicode.ToUpper(runes[0])}, runes[1:]...))
}
