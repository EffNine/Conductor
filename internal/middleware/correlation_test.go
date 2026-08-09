package middleware

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCorrelationIDGeneratesWhenMissing(t *testing.T) {
	app := fiber.New()
	app.Use(CorrelationID())
	app.Get("/test", func(c *fiber.Ctx) error {
		id := GetCorrelationIDFromLocals(c)
		return c.SendString(id)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	id := string(body)
	assert.NotEmpty(t, id)
	assert.Equal(t, 36, len(id)) // UUID format
}

func TestCorrelationIDForwardsHeader(t *testing.T) {
	app := fiber.New()
	app.Use(CorrelationID())
	app.Get("/test", func(c *fiber.Ctx) error {
		id := GetCorrelationIDFromLocals(c)
		return c.SendString(id)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Correlation-ID", "my-correlation-123")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	id := string(body)
	assert.Equal(t, "my-correlation-123", id)
}

func TestCorrelationIDResponseHeader(t *testing.T) {
	app := fiber.New()
	app.Use(CorrelationID())
	app.Get("/test", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	id := resp.Header.Get("X-Correlation-ID")
	assert.NotEmpty(t, id)
}

func TestRequestContextIDGenerates(t *testing.T) {
	app := fiber.New()
	app.Use(RequestContextID())
	app.Get("/test", func(c *fiber.Ctx) error {
		id := GetRequestIDFromLocals(c)
		return c.SendString(id)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	id := string(body)
	assert.NotEmpty(t, id)
}

func TestRequestContextIDUsesExistingRequestID(t *testing.T) {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("requestid", "existing-request-123")
		return c.Next()
	})
	app.Use(RequestContextID())
	app.Get("/test", func(c *fiber.Ctx) error {
		id := GetRequestIDFromLocals(c)
		return c.SendString(id)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	id := string(body)
	assert.Equal(t, "existing-request-123", id)
}

func TestGetCorrelationIDFromLocalsReturnsEmptyWhenMissing(t *testing.T) {
	app := fiber.New()
	app.Get("/test", func(c *fiber.Ctx) error {
		id := GetCorrelationIDFromLocals(c)
		return c.SendString(id)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	id := string(body)
	assert.Equal(t, "", id)
}
