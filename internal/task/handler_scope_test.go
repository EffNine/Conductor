package task

import (
	"net/http/httptest"
	"testing"

	"github.com/EffNine/conductor/internal/auth"
	"github.com/EffNine/conductor/internal/middleware"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// TestTaskRoutesRequireAuthentication pins that the task API is registered
// behind Auth. Before 2026-10-28 the whole /api/tasks group was unscoped, so a
// read-only service token could create and execute tasks (S13).
func TestTaskRoutesRequireAuthentication(t *testing.T) {
	app := fiber.New()
	app.Use(middleware.Auth(auth.NewService("test-key")))
	h := NewHandler(nil, nil, zap.NewNop())
	h.Register(app)

	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/tasks"},
		{"GET", "/api/tasks"},
		{"GET", "/api/tasks/x"},
		{"GET", "/api/tasks/x/tree"},
		{"POST", "/api/tasks/x/cancel"},
		{"POST", "/api/tasks/x/retry"},
		{"POST", "/api/tasks/x/resume"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("%s %s: want 401 without credentials, got %d", tc.method, tc.path, resp.StatusCode)
		}
	}
}
