package handler_test

import (
	"net/http"
	"testing"

	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func routerNewEngineForTest(cfg *config.Config, reg *provider.Registry) (*router.Engine, error) {
	return router.NewEngine(cfg, reg)
}

// TestExecutorFallbackOrderingPreserved verifies that the shared resilience
// executor attempts candidates strictly in configured order across both
// dispatch modes (P4.4.1 refactor invariant).
func TestExecutorFallbackOrderingPreserved(t *testing.T) {
	reg := provider.NewRegistry()
	failing := &p43FailingProvider{
		name: "primary",
		err:  provider.NewProviderError("primary", 500, provider.ErrorTypeServerError, "down", nil),
	}
	firstFallback := &p43OKProvider{name: "first-fb", model: "m1"}
	secondFallback := &p43OKProvider{name: "second-fb", model: "m2"}
	reg.Register(failing)
	reg.Register(firstFallback)
	reg.Register(secondFallback)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "primary"}},
		Fallbacks: map[string][]config.FallbackConfig{
			"gpt-4o": {{Provider: "first-fb"}, {Provider: "second-fb"}},
		},
		Circuit: config.CircuitBreakerConfig{Enabled: false},
	}
	engine, err := routerNewEngineForTest(cfg, reg)
	require.NoError(t, err)

	h := handler.New(engine, reg, nil, zap.NewNop(), catalog.New(reg, nil), openTestDB(t))
	app := fiber.New()
	h.Register(app)

	for _, stream := range []bool{false, true} {
		body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
		if stream {
			body = `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
		}
		resp, respBody := p43Post(t, app, body)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "stream=%v", stream)
		assert.Contains(t, respBody, "resp-first-fb", "stream=%v: first fallback must win", stream)
		assert.NotContains(t, respBody, "resp-second-fb", "stream=%v: second fallback must never run", stream)
	}

	assert.Equal(t, 2, failing.callCount) // once per dispatch mode
	assert.Equal(t, 2, firstFallback.callCount)
	assert.Equal(t, 0, secondFallback.callCount)
}
