package middleware

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/novexa/gateway/internal/auth"
	"github.com/novexa/gateway/internal/config"
	"go.uber.org/zap"
)

// Register registers all middleware on the Fiber app
func Register(app *fiber.App, cfg *config.Config, authService *auth.Service, logger *zap.Logger) {
	// Recovery middleware
	app.Use(Recovery(logger))

	// Request ID
	app.Use(requestid.New())

	// CORS
	if cfg.Server.CORS.Enabled {
		app.Use(cors.New(cors.Config{
			AllowOrigins:     joinStrings(cfg.Server.CORS.Origins),
			AllowMethods:     joinStrings(cfg.Server.CORS.Methods),
			AllowHeaders:     joinStrings(cfg.Server.CORS.Headers),
			AllowCredentials: true,
		}))
	}

	// Logging
	app.Use(Logging(logger))

	// Auth
	app.Use(Auth(authService))
}

// Auth returns middleware that validates the API key
func Auth(authService *auth.Service) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Skip auth for health endpoint
		if c.Path() == "/health" {
			return c.Next()
		}

		apiKey := c.Get("Authorization")
		if apiKey == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"message": "Missing API key",
					"type":    "authentication_error",
					"code":    "invalid_api_key",
				},
			})
		}

		// Strip "Bearer " prefix
		if len(apiKey) > 7 && apiKey[:7] == "Bearer " {
			apiKey = apiKey[7:]
		}

		if err := authService.Authenticate(apiKey); err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": fiber.Map{
					"message": "Invalid API key",
					"type":    "authentication_error",
					"code":    "invalid_api_key",
				},
			})
		}

		return c.Next()
	}
}

// Logging returns middleware that logs requests
func Logging(logger *zap.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		duration := time.Since(start)

		logger.Info("request",
			zap.String("method", c.Method()),
			zap.String("path", c.Path()),
			zap.Int("status", c.Response().StatusCode()),
			zap.Duration("duration", duration),
			zap.String("request_id", c.Locals("requestid").(string)),
		)

		return err
	}
}

// Recovery returns middleware that recovers from panics
func Recovery(logger *zap.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered",
					zap.Any("panic", r),
					zap.String("path", c.Path()),
				)
				_ = c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": fiber.Map{
						"message": "Internal server error",
						"type":    "server_error",
						"code":    "internal_error",
					},
				})
			}
		}()
		return c.Next()
	}
}

// joinStrings joins a slice of strings with comma
func joinStrings(strs []string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += ", "
		}
		result += s
	}
	return result
}
