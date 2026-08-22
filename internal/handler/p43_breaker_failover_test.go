package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
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

// p43FailingProvider always fails chat calls with the configured error.
type p43FailingProvider struct {
	name      string
	err       error
	callCount int
}

func (s *p43FailingProvider) Name() string                 { return s.name }
func (s *p43FailingProvider) SupportsModel(id string) bool { return true }
func (s *p43FailingProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	s.callCount++
	return nil, s.err
}
func (s *p43FailingProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	s.callCount++
	return nil, s.err
}
func (s *p43FailingProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, s.err
}
func (s *p43FailingProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (s *p43FailingProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *p43FailingProvider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *p43FailingProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}

// p43OKProvider returns a fixed successful chat response.
type p43OKProvider struct {
	name      string
	model     string
	callCount int
}

func (s *p43OKProvider) Name() string                 { return s.name }
func (s *p43OKProvider) SupportsModel(id string) bool { return true }
func (s *p43OKProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	s.callCount++
	return &apitypes.ChatCompletionResponse{
		ID:    "resp-" + s.name,
		Model: s.model,
		Choices: []apitypes.Choice{{
			Index: 0,
			Message: &apitypes.Message{
				Role:    "assistant",
				Content: "ok from " + s.name,
			},
		}},
	}, nil
}

func (s *p43OKProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	s.callCount++
	ch := make(chan apitypes.StreamChunk, 2)
	ch <- apitypes.StreamChunk{
		ID:    "resp-" + s.name,
		Model: s.model,
		Choices: []apitypes.Choice{{
			Index: 0,
			Delta: &apitypes.Message{Role: "assistant", Content: "ok"},
		}},
	}
	ch <- apitypes.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func (s *p43OKProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, nil
}
func (s *p43OKProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: s.model, ModelID: s.model, OwnedBy: s.name}}, nil
}
func (s *p43OKProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *p43OKProvider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *p43OKProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}

func setupP43(t *testing.T, reg *provider.Registry, primary string, fallback string) (*handler.Handler, *router.Engine) {
	t.Helper()
	cfg := &config.Config{
		Routes:    map[string]config.RouteConfig{"gpt-4o": {Provider: primary}},
		Fallbacks: map[string][]config.FallbackConfig{"gpt-4o": {{Provider: fallback}}},
		Circuit: config.CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 2,
			RecoveryTimeout:  time.Minute, // stay open for the duration of a test
			SuccessThreshold: 2,
		},
	}
	engine, err := router.NewEngine(cfg, reg)
	require.NoError(t, err)
	require.NotNil(t, engine.BreakerPool(), "circuit config must produce a breaker pool")

	cat := catalog.New(reg, nil)
	db := openTestDB(t)
	h := handler.New(engine, reg, nil, zap.NewNop(), cat, db)
	return h, engine
}

func p43Post(t *testing.T, app *fiber.App, body string) (*http.Response, string) {
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

func p43ForceOpen(t *testing.T, b *breaker.Breaker) {
	t.Helper()
	require.NotNil(t, b)
	b.RecordFailure()
	b.RecordFailure()
	assert.Equal(t, breaker.StateOpen, b.State(), "breaker should be forced open")
}

func TestOpenPrimaryBreakerFallsOverToFallback(t *testing.T) {
	reg := provider.NewRegistry()
	primary := &p43OKProvider{name: "openai", model: "gpt-4o"}
	fallback := &p43OKProvider{name: "groq", model: "llama-3.1-8b-instruct"}
	reg.Register(primary)
	reg.Register(fallback)

	h, engine := setupP43(t, reg, "openai", "groq")
	p43ForceOpen(t, engine.BreakerPool().Get("openai"))

	app := fiber.New()
	h.Register(app)

	resp, body := p43Post(t, app, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "resp-groq")
	assert.Equal(t, 0, primary.callCount, "blocked primary must not be attempted")
	assert.Equal(t, 1, fallback.callCount, "healthy fallback must serve the request")
}

func TestStreamingOpenPrimaryBreakerFallsOverToFallback(t *testing.T) {
	reg := provider.NewRegistry()
	primary := &p43OKProvider{name: "openai", model: "gpt-4o"}
	fallback := &p43OKProvider{name: "groq", model: "llama-3.1-8b-instruct"}
	reg.Register(primary)
	reg.Register(fallback)

	h, engine := setupP43(t, reg, "openai", "groq")
	p43ForceOpen(t, engine.BreakerPool().Get("openai"))

	app := fiber.New()
	h.Register(app)

	resp, body := p43Post(t, app, `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "data: [DONE]")
	assert.Equal(t, 0, primary.callCount, "blocked primary must not be attempted")
	assert.Equal(t, 1, fallback.callCount, "healthy fallback must serve the request")
}

func TestAllCandidatesOpenReturnsLegacyCircuitBreakerOpen(t *testing.T) {
	reg := provider.NewRegistry()
	primary := &p43OKProvider{name: "openai", model: "gpt-4o"}
	fallback := &p43OKProvider{name: "groq", model: "llama-3.1-8b-instruct"}
	reg.Register(primary)
	reg.Register(fallback)

	h, engine := setupP43(t, reg, "openai", "groq")
	p43ForceOpen(t, engine.BreakerPool().Get("openai"))
	p43ForceOpen(t, engine.BreakerPool().Get("groq"))

	app := fiber.New()
	h.Register(app)

	for _, stream := range []bool{false, true} {
		body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
		if stream {
			body = `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
		}
		resp, respBody := p43Post(t, app, body)
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "stream=%v", stream)
		assert.Contains(t, respBody, "circuit_breaker_open", "stream=%v", stream)
	}
	assert.Equal(t, 0, primary.callCount)
	assert.Equal(t, 0, fallback.callCount)
}

func TestRateLimitedPrimaryDoesNotTripBreakerAndStillFailsOver(t *testing.T) {
	reg := provider.NewRegistry()
	rateErr := provider.NewProviderError("openai", 429, provider.ErrorTypeRateLimit, "slow down", nil)
	primary := &p43FailingProvider{name: "openai", err: rateErr}
	fallback := &p43OKProvider{name: "groq", model: "llama-3.1-8b-instruct"}
	reg.Register(primary)
	reg.Register(fallback)

	// Threshold 1: any counted failure would trip the breaker immediately.
	cfgCircuit := config.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 1,
		RecoveryTimeout:  time.Minute,
		SuccessThreshold: 1,
	}
	cfg := &config.Config{
		Routes:    map[string]config.RouteConfig{"gpt-4o": {Provider: "openai"}},
		Fallbacks: map[string][]config.FallbackConfig{"gpt-4o": {{Provider: "groq"}}},
		Circuit:   cfgCircuit,
	}
	engine, err := router.NewEngine(cfg, reg)
	require.NoError(t, err)
	h := handler.New(engine, reg, nil, zap.NewNop(), catalog.New(reg, nil), openTestDB(t))

	app := fiber.New()
	h.Register(app)

	for i := 0; i < 5; i++ {
		resp, body := p43Post(t, app, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "attempt %d", i)
		assert.Contains(t, body, "resp-groq", "attempt %d", i)
	}

	pb := engine.BreakerPool().Get("openai")
	assert.Equal(t, breaker.StateClosed, pb.State(), "429 storms must never open the breaker")
	stats := pb.Stats()
	assert.GreaterOrEqual(t, stats.TotalThrottles, int64(5), "throttles must be recorded")
	assert.Equal(t, int64(0), stats.TotalFailures, "rate limits are not failures")

	// The rate-limited primary is still probed on every request instead of
	// being locked out by its breaker.
	assert.Equal(t, 5, primary.callCount)
}
