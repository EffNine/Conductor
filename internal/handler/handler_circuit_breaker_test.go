package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleCircuitBreakerStatusReturnsJSON(t *testing.T) {
	app := fiber.New()
	reg := provider.NewRegistry()
	engine := router.NewEngine(&config.Config{
		Circuit: config.CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 3,
			RecoveryTimeout:  30000000000,
			SuccessThreshold: 2,
		},
	}, reg)
	h := handler.New(engine, reg, nil, nil, nil, nil)
	h.Register(app)

	req := httptest.NewRequest(http.MethodGet, "/api/circuit-breaker", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandleCircuitBreakerStatusDisabled(t *testing.T) {
	app := fiber.New()
	reg := provider.NewRegistry()
	engine := router.NewEngine(&config.Config{
		Circuit: config.CircuitBreakerConfig{
			Enabled: false,
		},
	}, reg)
	h := handler.New(engine, reg, nil, nil, nil, nil)
	h.Register(app)

	req := httptest.NewRequest(http.MethodGet, "/api/circuit-breaker", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
