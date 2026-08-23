package handler

import (
	"strconv"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/buildinfo"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/metrics"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/gofiber/fiber/v2"
)

// HandleHealth handles GET /health
func (h *Handler) HandleHealth(c *fiber.Ctx) error {
	return c.JSON(apitypes.HealthResponse{Status: "ok", Version: buildinfo.Version})
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
	if h.cfg == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "configuration not available",
		})
	}
	return c.JSON(h.cfg.Redacted())
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
			"total_throttles":     s.TotalThrottles,
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
