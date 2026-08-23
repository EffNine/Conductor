package handler

import (
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
)

// HandleListModels handles GET /v1/models
func (h *Handler) HandleListModels(c *fiber.Ctx) error {
	modelList := make([]apitypes.ModelInfo, 0, len(router.AllVirtualModels()))

	// Add all virtual models as the primary catalog entries.
	for _, vm := range router.AllVirtualModels() {
		modelList = append(modelList, apitypes.ModelInfo{
			ID:      string(vm),
			Object:  "model",
			Created: h.startTime.Unix(),
			OwnedBy: "conductor",
			Name:    capitalize(string(vm)),
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
	rows := make([]modelRow, 0, len(entries)+len(router.AllVirtualModels()))

	// Add all virtual models as the primary catalog entries.
	for _, vm := range router.AllVirtualModels() {
		rows = append(rows, modelRow{
			ModelID:         string(vm),
			Name:            capitalize(string(vm)),
			Provider:        "conductor",
			ProviderModelID: "",
			OwnedBy:         "conductor",
			Reachable:       nil,
			State:           "virtual",
		})
	}

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

// HandleResetModelStatus handles POST /api/models/reset-status — clears health cache.
func (h *Handler) HandleResetModelStatus(c *fiber.Ctx) error {
	if h.modelStatus == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "model status store not available",
		})
	}
	h.modelStatus.Reset()
	return c.JSON(fiber.Map{"status": "ok", "message": "Model health status reset"})
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
