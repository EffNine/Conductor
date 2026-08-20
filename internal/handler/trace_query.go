package handler

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// Routing trace query API (P3.16): read-only observability over persisted
// routing decision traces.
//
// Isolation contract: this API NEVER participates in routing, provider
// selection, provider execution, runtime state updates, telemetry collection,
// or cache behavior. It reads persisted traces through the router.TraceStore
// abstraction only — no SQL, no tables, no database types reach the handlers.

const (
	traceQueryDefaultLimit = 50
	traceQueryMaxLimit     = 200
)

// TraceSummaryResponse is the compact list view of a persisted routing trace.
// It deliberately excludes the canonical payload (candidate score breakdowns,
// stage results, events) so list queries stay small; the full DecisionTrace is
// available via GET /api/routing/traces/:id.
type TraceSummaryResponse struct {
	DecisionID       string  `json:"decision_id"`
	Timestamp        string  `json:"timestamp"`
	SchemaVersion    int64   `json:"schema_version"`
	RequestedMode    string  `json:"requested_mode"`
	ResolvedMode     string  `json:"resolved_mode"`
	ModeSource       string  `json:"mode_source"`
	TaskType         string  `json:"task_type"`
	SelectedProvider string  `json:"selected_provider"`
	SelectedModel    string  `json:"selected_model"`
	RequestedModel   string  `json:"requested_model"` // original virtual/model ID before resolution
	RuntimeHash      string  `json:"runtime_hash"`
	SelectedScore    float64 `json:"selected_score"`
	CandidateCount   int     `json:"candidate_count"`
	Outcome          string  `json:"outcome"`
	CreatedAt        string  `json:"created_at"`
}

// SetTraceStore wires the TraceStore backing the routing trace query API.
// Reuse the SAME store instance that backs trace persistence so no second
// database handle is created. A nil store makes the endpoints respond 503.
func (h *Handler) SetTraceStore(store router.TraceStore) {
	h.traceStore = store
}

// HandleRoutingTraces handles GET /api/routing/traces — a compact, paginated,
// filterable list of persisted routing decisions, most recent first.
//
// Query parameters map directly to router.TraceFilter:
//
//	mode         canonical filter on resolved_mode (normalized via ParseMode)
//	provider     exact match on selected_provider
//	model        exact match on selected_model
//	runtime_hash exact match on runtime_hash
//	outcome      exact match on outcome (selected|rejected|failed)
//	from         RFC3339, inclusive lower bound on decision timestamp
//	to           RFC3339, inclusive upper bound on decision timestamp
//	limit        1..200, default 50
//	offset       >= 0, default 0
func (h *Handler) HandleRoutingTraces(c *fiber.Ctx) error {
	if h.traceStore == nil {
		return traceStoreUnavailable(c)
	}

	filter := router.TraceFilter{}

	if q := c.Query("mode"); q != "" {
		m, err := router.ParseMode(q)
		if err != nil {
			return traceQueryBadRequest(c, "mode", err.Error())
		}
		// mode filters resolved_mode: the canonical mode recorded on the
		// trace, never the raw requested_mode string.
		filter.Mode = string(m)
	}
	filter.Provider = c.Query("provider")
	filter.Model = c.Query("model")
	filter.RequestedModel = c.Query("requested_model")
	filter.RuntimeHash = c.Query("runtime_hash")
	filter.Outcome = c.Query("outcome")

	limit, err := parseTraceLimit(c.Query("limit"))
	if err != nil {
		return traceQueryBadRequest(c, "limit", err.Error())
	}
	offset, err := parseTraceOffset(c.Query("offset"))
	if err != nil {
		return traceQueryBadRequest(c, "offset", err.Error())
	}
	from, err := parseTraceTime(c.Query("from"))
	if err != nil {
		return traceQueryBadRequest(c, "from", err.Error())
	}
	to, err := parseTraceTime(c.Query("to"))
	if err != nil {
		return traceQueryBadRequest(c, "to", err.Error())
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return traceQueryBadRequest(c, "from", "from must not be after to")
	}

	filter.Limit = limit
	filter.Offset = offset
	filter.From = from
	filter.To = to

	summaries, err := h.traceStore.List(c.Context(), filter)
	if err != nil {
		h.logger.Warn("routing traces query failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Failed to query routing traces",
				"type":    "server_error",
				"code":    "trace_query_failed",
			},
		})
	}

	data := make([]TraceSummaryResponse, 0, len(summaries))
	for _, s := range summaries {
		data = append(data, TraceSummaryResponse{
			DecisionID:       string(s.DecisionID),
			Timestamp:        s.Timestamp.UTC().Format(time.RFC3339),
			SchemaVersion:    s.SchemaVersion,
			RequestedMode:    s.RequestedMode,
			ResolvedMode:     s.ResolvedMode,
			ModeSource:       s.ModeSource,
			TaskType:         s.TaskType,
			SelectedProvider: s.SelectedProvider,
			SelectedModel:    s.SelectedModel,
			RequestedModel:   s.RequestedModel,
			RuntimeHash:      s.RuntimeHash,
			SelectedScore:    s.SelectedScore,
			CandidateCount:   s.CandidateCount,
			Outcome:          s.Outcome,
			CreatedAt:        s.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	return c.JSON(fiber.Map{
		"data": data,
		"pagination": fiber.Map{
			"limit":  limit,
			"offset": offset,
			"count":  len(data),
		},
	})
}

// HandleRoutingTraceByID handles GET /api/routing/traces/:id — the complete,
// canonical persisted DecisionTrace for one decision. Unknown IDs return 404.
func (h *Handler) HandleRoutingTraceByID(c *fiber.Ctx) error {
	if h.traceStore == nil {
		return traceStoreUnavailable(c)
	}

	id := router.DecisionID(c.Params("id"))
	trace, err := h.traceStore.Get(c.Context(), id)
	if err != nil {
		if errors.Is(err, router.ErrTraceNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": fiber.Map{
					"message": fmt.Sprintf("Routing trace '%s' not found", string(id)),
					"type":    "invalid_request_error",
					"code":    "trace_not_found",
				},
			})
		}
		h.logger.Warn("routing trace lookup failed",
			zap.String("decision_id", string(id)),
			zap.Error(err),
		)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fiber.Map{
				"message": "Failed to load routing trace",
				"type":    "server_error",
				"code":    "trace_query_failed",
			},
		})
	}

	return c.JSON(trace)
}

// traceStoreUnavailable is the shared 503 response when trace persistence is
// not enabled (e.g. routing disabled, no store wired). Matches the existing
// dashboard convention for unavailable subsystems (HandleUsage, HandleRuntime).
func traceStoreUnavailable(c *fiber.Ctx) error {
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
		"error": fiber.Map{
			"message": "Routing trace store not available",
			"type":    "server_error",
			"code":    "trace_store_unavailable",
		},
	})
}

// traceQueryBadRequest returns a 400 in the established dashboard error
// envelope with the offending parameter named.
func traceQueryBadRequest(c *fiber.Ctx, param, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
		"error": fiber.Map{
			"message": msg,
			"type":    "invalid_request_error",
			"param":   param,
			"code":    "invalid_request",
		},
	})
}

// parseTraceLimit validates the limit query parameter: default 50, range
// 1..200. Pathological values are rejected, never silently accepted.
func parseTraceLimit(q string) (int, error) {
	if q == "" {
		return traceQueryDefaultLimit, nil
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 1 || n > traceQueryMaxLimit {
		return 0, fmt.Errorf("limit must be an integer between 1 and %d", traceQueryMaxLimit)
	}
	return n, nil
}

// parseTraceOffset validates the offset query parameter: default 0, >= 0.
func parseTraceOffset(q string) (int, error) {
	if q == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("offset must be a non-negative integer")
	}
	return n, nil
}

// parseTraceTime validates a from/to timestamp: RFC3339, converted to UTC.
func parseTraceTime(q string) (time.Time, error) {
	if q == "" {
		return time.Time{}, nil
	}
	ts, err := time.Parse(time.RFC3339, q)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp %q: must be RFC3339 (e.g. 2026-08-19T00:00:00Z)", q)
	}
	return ts.UTC(), nil
}
