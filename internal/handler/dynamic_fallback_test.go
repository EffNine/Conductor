package handler_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// setupDynamicFallback wires a gateway whose route for gpt-4o points at a
// failing primary provider, with no static fallback chain configured. The
// only path to success is the dynamic fallback tail.
func setupDynamicFallback(t *testing.T, enabled bool) (*handler.Handler, *fiber.App, *p43FailingProvider, *p43OKProvider) {
	t.Helper()
	reg := provider.NewRegistry()
	primary := &p43FailingProvider{name: "openai", err: provider.ErrNotImplemented}
	alternate := &p43OKProvider{name: "groq", model: "llama-3.1-8b-instruct"}
	reg.Register(primary)
	reg.Register(alternate)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
		Circuit: config.CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 50,
			RecoveryTimeout:  time.Minute,
			SuccessThreshold: 2,
		},
	}
	cfg.Routing.DynamicFallback.Enabled = enabled
	cfg.Routing.DynamicFallback.MaxCandidates = 3

	engine, err := router.NewEngine(cfg, reg)
	require.NoError(t, err)

	cat := catalog.New(reg, nil)
	autoRes := router.NewAutoResolver(router.AutoResolverConfig{
		Registry:    reg,
		Catalog:     cat,
		BreakerPool: engine.BreakerPool(),
		Weights:     config.DefaultRoutingWeights(),
	})

	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)
	h.SetConfig(cfg)
	h.SetAutoModelResolver(autoRes)

	app := fiber.New()
	h.Register(app)
	return h, app, primary, alternate
}

func dfPost(t *testing.T, app *fiber.App, body string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	return resp, string(respBody)
}

func TestDynamicFallbackServesWhenPrimaryFailsWithoutStaticChain(t *testing.T) {
	_, app, primary, alternate := setupDynamicFallback(t, true)

	for _, stream := range []bool{false, true} {
		body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
		if stream {
			body = `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
		}
		resp, respBody := dfPost(t, app, body)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "stream=%v body=%s", stream, respBody)
		if stream {
			assert.Contains(t, respBody, "data: [DONE]")
			assert.Contains(t, respBody, "resp-groq")
		} else {
			assert.Contains(t, respBody, "resp-groq")
		}
		assert.GreaterOrEqual(t, primary.callCount, 1, "primary attempted first")
		assert.GreaterOrEqual(t, alternate.callCount, 1, "dynamic fallback served the request")
	}
}

func TestDynamicFallbackDisabledSurfacesPrimaryFailure(t *testing.T) {
	_, app, primary, alternate := setupDynamicFallback(t, false)

	resp, respBody := dfPost(t, app, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, "expected the failure to surface, got: %s", respBody)
	assert.GreaterOrEqual(t, primary.callCount, 1)
	assert.Equal(t, 0, alternate.callCount, "no fallback may be attempted when disabled")
}

func TestDynamicFallbackStaysWithinCategoryForVisionRequests(t *testing.T) {
	// groq/llama-3.1-8b-instruct has no vision capability by default caps, so
	// an image request must NOT fall back to it even though it is healthy.
	_, app, _, alternate := setupDynamicFallback(t, true)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"what is in this image?"},` +
		`{"type":"image_url","image_url":{"url":"https://example.com/img.png"}}]}]}`

	resp, respBody := dfPost(t, app, body)
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, "vision request must not be served by a non-vision model: %s", respBody)
	assert.NotContains(t, respBody, "resp-groq")
	assert.Equal(t, 0, alternate.callCount)
}
