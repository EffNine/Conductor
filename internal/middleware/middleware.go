package middleware

import (
	"errors"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/auth"
	"github.com/EffNine/conductor/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"go.uber.org/zap"
)

const (
	// authSchemeBearer is the only supported Authorization scheme.
	authSchemeBearer = "Bearer"
)

// Register registers all middleware on the Fiber app
func Register(app *fiber.App, cfg *config.Config, authService *auth.Service, logger *zap.Logger) {
	// Recovery middleware
	app.Use(Recovery(logger))

	// Request ID
	app.Use(requestid.New())

	// Correlation ID (for external tracing)
	app.Use(CorrelationID())

	// Request context ID (for internal tracing, reuses requestid if present)
	app.Use(RequestContextID())

	// CORS
	if cfg.Server.CORS.Enabled {
		allowOrigins := joinStrings(cfg.Server.CORS.Origins)
		allowCredentials := true
		for _, origin := range cfg.Server.CORS.Origins {
			if origin == "*" {
				allowCredentials = false
				logger.Warn("CORS wildcard origin configured; disabling AllowCredentials")
				break
			}
		}
		app.Use(cors.New(cors.Config{
			AllowOrigins:     allowOrigins,
			AllowMethods:     joinStrings(cfg.Server.CORS.Methods),
			AllowHeaders:     joinStrings(cfg.Server.CORS.Headers),
			AllowCredentials: allowCredentials,
		}))
	}

	// Logging
	app.Use(Logging(logger))

	// Auth
	app.Use(Auth(authService))
}

// errMissingCredentials indicates no Authorization header was supplied.
var errMissingCredentials = errors.New("missing authorization header")

// errMalformedCredentials indicates an Authorization header that is not a
// well-formed Bearer credential.
var errMalformedCredentials = errors.New("malformed authorization header")

// Auth returns middleware that validates the API key for every endpoint except
// the operational liveness probe GET /health.
//
// Credentials must be presented as "Authorization: Bearer <key>" (scheme is
// matched case-insensitively per RFC 7235). Missing, malformed, or invalid
// credentials all fail closed with HTTP 401 and a WWW-Authenticate: Bearer
// challenge. The middleware never logs the Authorization header or the API key,
// and the challenge response never echoes the supplied credential.
func Auth(authService *auth.Service) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Operational liveness/readiness probes (e.g. Fly.io checks) must not
		// require credentials; everything else is protected.
		if c.Path() == "/health" {
			return c.Next()
		}

		token, err := bearerToken(c.Get(fiber.HeaderAuthorization))
		if err != nil {
			if errors.Is(err, errMissingCredentials) {
				return unauthorized(c, "Missing API key", "missing_api_key")
			}
			return unauthorized(c, "Invalid API key", "invalid_api_key")
		}

		if err := authService.Authenticate(token); err != nil {
			return unauthorized(c, "Invalid API key", "invalid_api_key")
		}

		return c.Next()
	}
}

// bearerToken extracts the Bearer credential from an Authorization header.
// The scheme is case-insensitive; anything that is not exactly a single
// "Bearer <token>" pair is rejected.
func bearerToken(header string) (string, error) {
	if strings.TrimSpace(header) == "" {
		return "", errMissingCredentials
	}

	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], authSchemeBearer) {
		return "", errMalformedCredentials
	}

	return parts[1], nil
}

// unauthorized writes the standard 401 authentication error envelope with a
// Bearer challenge. Neither the message nor the code embeds the credential.
func unauthorized(c *fiber.Ctx, message, code string) error {
	c.Set(fiber.HeaderWWWAuthenticate, authSchemeBearer)
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": fiber.Map{
			"message": message,
			"type":    "authentication_error",
			"code":    code,
		},
	})
}

// Logging returns middleware that logs requests
func Logging(logger *zap.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		duration := time.Since(start)

		requestID, _ := c.Locals("request_id").(string)
		correlationID, _ := c.Locals("correlation_id").(string)
		logger.Info("request",
			zap.String("method", c.Method()),
			zap.String("path", c.Path()),
			zap.Int("status", c.Response().StatusCode()),
			zap.Duration("duration", duration),
			zap.String("request_id", requestID),
			zap.String("correlation_id", correlationID),
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
