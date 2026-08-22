package handler

import (
	"strconv"
	"time"

	"github.com/EffNine/conductor/internal/database"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// Failure analytics API (P4.5): read-only observability over persisted
// execution attempts. Same isolation contract as the routing trace query
// API: it never participates in routing, provider selection, breaker
// accounting, or cache behaviour — it only reads persisted rows.
//
// Endpoints inherit the gateway-key middleware that guards every /api route.

const (
	failuresDefaultLimit = 50
	failuresMaxLimit     = 200
	failuresMaxWindow    = 30 * 24 * time.Hour // sanity cap on requested windows
)

// SetAttemptStore wires the store backing the failure analytics API.
// A nil store makes the endpoints respond 503 (attempts persistence off).
func (h *Handler) SetAttemptStore(store *database.AttemptStore) {
	h.attemptStore = store
}

// HandleFailures handles GET /api/failures — a paginated, filterable list of
// non-success execution attempts, newest first.
//
// Query parameters:
//
//	class    exact match on failure_class (e.g. rate_limited)
//	provider exact match on provider name
//	model    exact match on virtual_model
//	window   duration (e.g. 1h, 24h); default unbounded, capped at 30d
//	limit    1..200, default 50
//	offset   >= 0, default 0
func (h *Handler) HandleFailures(c *fiber.Ctx) error {
	if h.attemptStore == nil {
		return attemptStoreUnavailable(c)
	}

	filter := database.FailureFilter{}
	filter.Class = c.Query("class")
	filter.Provider = c.Query("provider")
	filter.Model = c.Query("model")

	if q := c.Query("window"); q != "" {
		w, err := time.ParseDuration(q)
		if err != nil || w <= 0 {
			return failuresBadRequest(c, "window", "must be a positive duration such as 1h or 24h")
		}
		if w > failuresMaxWindow {
			w = failuresMaxWindow
		}
		filter.Since = time.Now().UTC().Add(-w)
	}
	if q := c.Query("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 {
			return failuresBadRequest(c, "limit", "must be a positive integer")
		}
		filter.Limit = n
	}
	if q := c.Query("offset"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 {
			return failuresBadRequest(c, "offset", "must be a non-negative integer")
		}
		filter.Offset = n
	}

	rows, total, err := h.attemptStore.ListFailures(c.Context(), filter)
	if err != nil {
		h.logger.Warn("failures api: list failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"message": "failed to query attempts", "type": "server_error"},
		})
	}
	return c.JSON(fiber.Map{
		"total":    total,
		"limit":    filter.Limit,
		"offset":   filter.Offset,
		"attempts": rows,
	})
}

// HandleFailuresSummary handles GET /api/failures/summary — failure counts,
// provider and class breakdowns, and fixed-width time buckets.
//
// Query parameters:
//
//	window duration (e.g. 1h, 24h); default 24h, capped at 30d
//	bucket duration; defaults to window/12 (min 1m)
func (h *Handler) HandleFailuresSummary(c *fiber.Ctx) error {
	if h.attemptStore == nil {
		return attemptStoreUnavailable(c)
	}

	window := 24 * time.Hour
	if q := c.Query("window"); q != "" {
		w, err := time.ParseDuration(q)
		if err != nil || w <= 0 {
			return failuresBadRequest(c, "window", "must be a positive duration such as 1h or 24h")
		}
		window = w
	}
	if window > failuresMaxWindow {
		window = failuresMaxWindow
	}
	bucket := window / 12
	if q := c.Query("bucket"); q != "" {
		b, err := time.ParseDuration(q)
		if err != nil || b <= 0 {
			return failuresBadRequest(c, "bucket", "must be a positive duration such as 5m or 1h")
		}
		bucket = b
	}

	since := time.Now().UTC().Add(-window)
	summary, err := h.attemptStore.FailureSummary(c.Context(), since, bucket)
	if err != nil {
		h.logger.Warn("failures api: summary failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{"message": "failed to aggregate attempts", "type": "server_error"},
		})
	}
	return c.JSON(fiber.Map{
		"window_seconds": int64(window / time.Second),
		"bucket_seconds": int64(bucket / time.Second),
		"total_failures": summary.Total,
		"by_provider":    summary.ByProvider,
		"by_class":       summary.ByClass,
		"buckets":        summary.Buckets,
	})
}

func failuresBadRequest(c *fiber.Ctx, param, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
		"error": fiber.Map{
			"message": msg,
			"type":    "invalid_request_error",
			"param":   param,
		},
	})
}

func attemptStoreUnavailable(c *fiber.Ctx) error {
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
		"error": fiber.Map{
			"message": "attempt persistence is disabled",
			"type":    "server_error",
			"code":    "attempts_unavailable",
		},
	})
}
