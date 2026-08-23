package middleware

import (
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/gofiber/fiber/v2"
)

// rateLimiter is a fixed-window per-minute counter guarding the single
// gateway key against runaway clients. Personal deployments run one process,
// so in-memory windowing is sufficient and dependency-free.
type rateLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	limit     int
	count     int
	windowEnd time.Time
}

func (r *rateLimiter) allow(now time.Time) (bool, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.After(r.windowEnd) {
		r.windowEnd = now.Add(r.window)
		r.count = 0
	}
	if r.count >= r.limit {
		return false, r.windowEnd.Sub(now)
	}
	r.count++
	return true, 0
}

// RateLimit returns middleware enforcing a global requests-per-minute ceiling
// on every endpoint except the operational /health probe. A zero or negative
// limit disables enforcement even when enabled is set.
func RateLimit(enabled bool, requestsPerMinute int) fiber.Handler {
	if !enabled || requestsPerMinute <= 0 {
		return nil
	}
	rl := &rateLimiter{
		window: time.Minute,
		limit:  requestsPerMinute,
	}
	return func(c *fiber.Ctx) error {
		if c.Path() == "/health" {
			return c.Next()
		}
		ok, _ := rl.allow(time.Now())
		if ok {
			return c.Next()
		}
		c.Set("Retry-After", "60")
		return c.Status(fiber.StatusTooManyRequests).JSON(apitypes.ErrorResponse{
			Error: apitypes.ErrorDetail{
				Message: "rate limit exceeded; slow down or raise rate_limit.global.requests_per_minute",
				Type:    "rate_limit_error",
				Code:    "rate_limited",
			},
		})
	}
}
