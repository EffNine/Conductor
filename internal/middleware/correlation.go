package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const (
	correlationIDKey = "correlation_id"
	requestIDKey     = "request_id"
)

// GetCorrelationIDFromLocals returns the correlation ID stored in Fiber Locals.
func GetCorrelationIDFromLocals(c *fiber.Ctx) string {
	if v, ok := c.Locals(correlationIDKey).(string); ok {
		return v
	}
	return ""
}

// GetRequestIDFromLocals returns the request ID stored in Fiber Locals.
func GetRequestIDFromLocals(c *fiber.Ctx) string {
	if v, ok := c.Locals(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// CorrelationID returns middleware that generates or forwards a correlation ID
// per request. It reads the X-Correlation-ID header; if absent, it generates one.
// The ID is stored in Fiber Locals and echoed back in the response header.
func CorrelationID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Get("X-Correlation-ID", "")
		if id == "" {
			id = uuid.New().String()
		} else {
			id = strings.TrimSpace(id)
			if id == "" {
				id = uuid.New().String()
			}
		}

		c.Locals(correlationIDKey, id)
		c.Set("X-Correlation-ID", id)

		return c.Next()
	}
}

// RequestContextID returns middleware that generates a request ID and stores it
// in Fiber Locals. Uses the requestid middleware's output if available (which
// sets "requestid"), otherwise generates a UUID.
func RequestContextID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		existing := c.Locals("requestid")
		id := ""
		if s, ok := existing.(string); ok && s != "" {
			id = s
		}
		if id == "" {
			id = uuid.New().String()
		}

		c.Locals(requestIDKey, id)

		return c.Next()
	}
}
