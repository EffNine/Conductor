// Package admin exposes user and API-key management. Every route requires
// conductor:admin; the acting principal is logged with each mutation.
package admin

import (
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/auth"
	"github.com/EffNine/conductor/internal/middleware"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// Handler serves the admin API.
type Handler struct {
	keys   *auth.KeyStore
	logger *zap.Logger
}

// New creates the handler.
func New(keys *auth.KeyStore, logger *zap.Logger) *Handler {
	return &Handler{keys: keys, logger: logger}
}

// Register mounts the admin routes; all require conductor:admin.
func (h *Handler) Register(app *fiber.App) {
	group := app.Group("/api", middleware.RequireScope("conductor:admin"))
	group.Get("/users", h.listUsers)
	group.Post("/users", h.createUser)
	group.Patch("/users/:id", h.updateUser)
	group.Get("/api-keys", h.listKeys)
	group.Post("/users/:id/keys", h.createKey)
	group.Delete("/api-keys/:id", h.revokeKey)
}

type createUserRequest struct {
	ID                   string   `json:"id"`
	Label                string   `json:"label"`
	Role                 string   `json:"role"`
	MaxRequestsPerMinute *int     `json:"max_requests_per_minute"`
	MaxTokensPerMonth    *int64   `json:"max_tokens_per_month"`
	MaxSpendUSDPerMonth  *float64 `json:"max_spend_usd_per_month"`
	ModelAllowlist       []string `json:"model_allowlist"`
}

func (h *Handler) listUsers(c *fiber.Ctx) error {
	users, err := h.keys.ListUsers()
	if err != nil {
		return serverError(c, "failed to list users")
	}
	return c.JSON(fiber.Map{"users": users})
}

func (h *Handler) createUser(c *fiber.Ctx) error {
	var req createUserRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	user := auth.User{
		ID:                   strings.TrimSpace(req.ID),
		Label:                strings.TrimSpace(req.Label),
		Role:                 req.Role,
		MaxRequestsPerMinute: req.MaxRequestsPerMinute,
		MaxTokensPerMonth:    req.MaxTokensPerMonth,
		MaxSpendUSDPerMonth:  req.MaxSpendUSDPerMonth,
		ModelAllowlist:       req.ModelAllowlist,
	}
	if err := h.keys.CreateUser(user); err != nil {
		return badRequest(c, err.Error())
	}
	h.audit(c, "user.created", zap.String("user_id", user.ID), zap.String("role", user.Role))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"user": fiber.Map{"id": user.ID, "label": user.Label, "role": user.Role},
	})
}

func (h *Handler) updateUser(c *fiber.Ctx) error {
	var req struct {
		Disabled *bool `json:"disabled"`
	}
	if err := c.BodyParser(&req); err != nil || req.Disabled == nil {
		return badRequest(c, "body must contain {\"disabled\": true|false}")
	}
	id := c.Params("id")
	if err := h.keys.SetUserDisabled(id, *req.Disabled); err != nil {
		return badRequest(c, err.Error())
	}
	h.audit(c, "user.updated", zap.String("user_id", id), zap.Bool("disabled", *req.Disabled))
	return c.JSON(fiber.Map{"id": id, "disabled": *req.Disabled})
}

func (h *Handler) listKeys(c *fiber.Ctx) error {
	keys, err := h.keys.ListKeys(c.Query("user_id"))
	if err != nil {
		return serverError(c, "failed to list keys")
	}
	return c.JSON(fiber.Map{"keys": keys})
}

func (h *Handler) createKey(c *fiber.Ctx) error {
	var req struct {
		Scopes   []string `json:"scopes"`
		TTLHours int      `json:"ttl_hours"`
	}
	_ = c.BodyParser(&req) // empty body = default scopes, no expiry

	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = []string{"conductor:read", "conductor:execute"}
	}
	for _, scope := range scopes {
		switch scope {
		case "conductor:read", "conductor:execute", "conductor:admin":
		default:
			return badRequest(c, "unknown scope "+scope)
		}
	}

	var ttl *time.Duration
	if req.TTLHours > 0 {
		d := time.Duration(req.TTLHours) * time.Hour
		ttl = &d
	}

	userID := c.Params("id")
	raw, key, err := h.keys.CreateKey(userID, scopes, ttl)
	if err != nil {
		return badRequest(c, err.Error())
	}
	h.audit(c, "api_key.created",
		zap.String("user_id", userID), zap.String("key_id", key.ID),
		zap.Strings("scopes", scopes))
	// The raw key is returned exactly once and never stored.
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"key":     raw,
		"id":      key.ID,
		"user_id": key.UserID,
		"prefix":  key.Prefix,
		"scopes":  key.Scopes,
		"note":    "store this key now; it is not recoverable",
	})
}

func (h *Handler) revokeKey(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.keys.RevokeKey(id); err != nil {
		return serverError(c, "failed to revoke key")
	}
	h.audit(c, "api_key.revoked", zap.String("key_id", id))
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) audit(c *fiber.Ctx, event string, fields ...zap.Field) {
	if h.logger == nil {
		return
	}
	if actor, ok := middleware.PrincipalFrom(c); ok {
		fields = append(fields, zap.String("actor", actor.UserID))
	}
	h.logger.Info("admin: "+event, fields...)
}

func badRequest(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": message})
}

func serverError(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": message})
}
