package middleware

import (
	"github.com/gofiber/fiber/v3"
)

// SecurityHeaders adds HSTS and other hardening headers to every response.
func SecurityHeaders() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("Referrer-Policy", "no-referrer")
		c.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Set("Cross-Origin-Opener-Policy", "same-origin")
		// Don't cache sensitive responses by default; handler can override for public GETs
		c.Set("Cache-Control", "no-store")
		return c.Next()
	}
}
