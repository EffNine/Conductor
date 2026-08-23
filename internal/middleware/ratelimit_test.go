package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestRateLimitBlocksAfterCeiling(t *testing.T) {
	app := fiber.New()
	app.Use(RateLimit(true, 3))
	app.Get("/v1/models", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	app.Get("/health", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	for i := 0; i < 3; i++ {
		resp, _ := app.Test(httptest.NewRequest("GET", "/v1/models", nil))
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: expected 200 before ceiling, got %d", i+1, resp.StatusCode)
		}
	}

	blocked, _ := app.Test(httptest.NewRequest("GET", "/v1/models", nil))
	if blocked.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("expected 429 at ceiling, got %d", blocked.StatusCode)
	}
	if blocked.Header.Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
	body := make([]byte, 512)
	n, _ := blocked.Body.Read(body)
	if string(body[:n]) == "" {
		t.Fatal("429 must carry a JSON error body")
	}

	// /health stays exempt for liveness probes.
	hc, _ := app.Test(httptest.NewRequest("GET", "/health", nil))
	if hc.StatusCode != 200 {
		t.Fatalf("/health must bypass rate limit, got %d", hc.StatusCode)
	}
}

func TestRateLimitDisabledOrZero(t *testing.T) {
	if rl := RateLimit(false, 1); rl != nil {
		t.Fatal("disabled limiter must return nil middleware")
	}
	if rl := RateLimit(true, 0); rl != nil {
		t.Fatal("zero limit must return nil middleware")
	}
}
