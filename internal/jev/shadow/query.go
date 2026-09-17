package shadow

import (
	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// QueryHandler serves the read-only shadow-observation query API. It is a
// dashboard-only surface: it never influences routing and is safe to register
// only when the advisor is enabled.
type QueryHandler struct {
	store  Store
	logger *zap.Logger
}

// NewQueryHandler builds a query API handler.
func NewQueryHandler(store Store, logger *zap.Logger) *QueryHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &QueryHandler{store: store, logger: logger}
}

// Register mounts the read-only endpoint on the app with the same scope
// (conductor:read) used by the other dashboard endpoints. It returns the
// handler so the caller can register it under a specific path.
func (h *QueryHandler) Handle() fiber.Handler {
	if h.store == nil {
		return func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusServiceUnavailable).JSON(apitypes.ErrorResponse{
				Error: apitypes.ErrorDetail{
					Message: "jev shadow advisor not enabled",
					Type:    "server_error",
					Code:    "not_enabled",
				},
			})
		}
	}
	return h.HandleList
}

// HandleList returns recent shadow observations (most recent first).
func (h *QueryHandler) HandleList(c *fiber.Ctx) error {
	if h.store == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "store not configured",
		})
	}
	rows, err := h.store.List(c.Context(), 100)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "list failed",
		})
	}
	return c.JSON(fiber.Map{
		"data": rows,
	})
}
