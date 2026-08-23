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

// embOKProvider serves fixed successful embeddings.
type embOKProvider struct {
	name      string
	model     string
	callCount int
}

func (s *embOKProvider) Name() string                 { return s.name }
func (s *embOKProvider) SupportsModel(id string) bool { return true }
func (s *embOKProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *embOKProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (s *embOKProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	s.callCount++
	return &apitypes.EmbeddingResponse{
		Object: "list",
		Model:  s.model,
		Data: []apitypes.EmbeddingData{{
			Object:    "embedding",
			Embedding: []float64{0.1, 0.2, 0.3},
			Index:     0,
		}},
		Usage: &apitypes.Usage{PromptTokens: 2, TotalTokens: 2},
	}, nil
}
func (s *embOKProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: s.model, ModelID: s.model, OwnedBy: s.name}}, nil
}
func (s *embOKProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *embOKProvider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *embOKProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}

// setupEmbeddingsFailover routes text-embedding-3-small at a failing primary;
// the healthy embeddings-capable alternate is reachable only via the dynamic tail.
func setupEmbeddingsFailover(t *testing.T, enabled bool) (*embOKProvider, *fiber.App) {
	t.Helper()
	reg := provider.NewRegistry()
	primary := &p43FailingProvider{name: "openai", err: provider.ErrNotImplemented}
	alternate := &embOKProvider{name: "groq", model: "text-embedding-3-small"}
	reg.Register(primary)
	reg.Register(alternate)

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"text-embedding-3-small": {Provider: "openai"}},
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
	return alternate, app
}

func postEmbeddings(t *testing.T, app *fiber.App, body string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	return resp, string(respBody)
}

func TestEmbeddingsFailoverServesWhenPrimaryFails(t *testing.T) {
	alternate, app := setupEmbeddingsFailover(t, true)

	resp, body := postEmbeddings(t, app, `{"model":"text-embedding-3-small","input":"hello"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Contains(t, body, `"object":"list"`)
	assert.Equal(t, 1, alternate.callCount)
}

func TestEmbeddingsFailoverDisabledSurfacesError(t *testing.T) {
	alternate, app := setupEmbeddingsFailover(t, false)

	resp, _ := postEmbeddings(t, app, `{"model":"text-embedding-3-small","input":"hello"}`)
	assert.NotEqual(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, alternate.callCount)
}
