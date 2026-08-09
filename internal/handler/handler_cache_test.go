package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/cache"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/handler"
	"github.com/EffNine/conductor/internal/metrics"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type cachingStubProvider struct {
	name       string
	response   *apitypes.ChatCompletionResponse
	streamErr  error
	chatErr    error
	callCount  int
	mu         sync.Mutex
}

func (s *cachingStubProvider) Name() string { return s.name }

func (s *cachingStubProvider) ChatCompletion(_ context.Context, _ *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	s.mu.Lock()
	s.callCount++
	s.mu.Unlock()
	if s.chatErr != nil {
		return nil, s.chatErr
	}
	return s.response, nil
}

func (s *cachingStubProvider) ChatCompletionStream(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	ch := make(chan apitypes.StreamChunk, 1)
	ch <- apitypes.StreamChunk{Done: true}
	close(ch)
	return ch, s.streamErr
}

func (s *cachingStubProvider) Embeddings(_ context.Context, _ *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *cachingStubProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: "test-model", ModelID: "test-model", OwnedBy: s.name}}, nil
}

func (s *cachingStubProvider) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *cachingStubProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}

func (s *cachingStubProvider) SupportsModel(string) bool { return true }
func (s *cachingStubProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}

func setupCachedTestApp(t *testing.T, cacheEnabled bool) (*fiber.App, *metrics.Collector) {
	t.Helper()
	reg := provider.NewRegistry()
	reg.Register(&cachingStubProvider{
		name: "openai",
		response: &apitypes.ChatCompletionResponse{
			ID:      "chatcmpl-1",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "gpt-4o",
			Choices: []apitypes.Choice{{
				Index: 0,
				Message: &apitypes.Message{
					Role:    "assistant",
					Content: "cached response",
				},
				FinishReason: strPtr("stop"),
			}},
			Usage: &apitypes.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	})

	cfg := &config.Config{
		APIKey: "test-key",
		Routes: map[string]config.RouteConfig{
			"test-model": {Provider: "openai", ModelID: "gpt-4o"},
		},
		Cache: config.CacheConfig{
			Enabled:    cacheEnabled,
			TTL:        5 * time.Minute,
			MaxEntries: 100,
		},
	}
	engine := router.NewEngine(cfg, reg)
	h := handler.New(engine, reg, nil, zap.NewNop(), nil, nil)

	m := metrics.NewCollector()
	h.SetMetrics(m)

	if cacheEnabled {
		cacheEngine := cache.NewEngine(cfg.Cache, m, zap.NewNop())
		h.SetCacheEngine(cacheEngine)
	}

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("correlation_id", "test-corr")
		c.Locals("request_id", "test-req")
		return c.Next()
	})
	h.Register(app)
	return app, m
}

func strPtr(s string) *string { return &s }

func TestCacheHitReturnsCachedResponse(t *testing.T) {
	app, _ := setupCachedTestApp(t, true)

	body := `{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")

	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var chatResp apitypes.ChatCompletionResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&chatResp))
	assert.Equal(t, "cached response", chatResp.Choices[0].Message.Content)
}

func TestCacheMissCallsProvider(t *testing.T) {
	app, m := setupCachedTestApp(t, true)

	body := `{"model":"test-model","messages":[{"role":"user","content":"miss"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")

	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	snap := m.Snapshot()
	assert.GreaterOrEqual(t, snap.CacheMisses, int64(1))
	assert.GreaterOrEqual(t, snap.CacheStores, int64(1))
}

func TestCacheHitOnSecondRequest(t *testing.T) {
	app, m := setupCachedTestApp(t, true)

	body := `{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")

	// First request — miss + store.
	resp1, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp1.StatusCode)

	// Second request — should be a hit.
	resp2, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	snap := m.Snapshot()
	assert.GreaterOrEqual(t, snap.CacheHits, int64(1))
}

func TestStreamingBypassesCache(t *testing.T) {
	app, m := setupCachedTestApp(t, true)

	body := `{"model":"test-model","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")

	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	snap := m.Snapshot()
	assert.Equal(t, int64(0), snap.CacheHits)
	assert.Equal(t, int64(0), snap.CacheStores)
}

func TestFailedRequestBypassesCache(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&cachingStubProvider{
		name:    "openai",
		chatErr: provider.ErrNotImplemented,
	})

	cfg := &config.Config{
		APIKey: "test-key",
		Routes: map[string]config.RouteConfig{
			"test-model": {Provider: "openai", ModelID: "gpt-4o"},
		},
		Cache: config.CacheConfig{
			Enabled:    true,
			TTL:        5 * time.Minute,
			MaxEntries: 100,
		},
	}
	engine := router.NewEngine(cfg, reg)
	h := handler.New(engine, reg, nil, zap.NewNop(), nil, nil)

	m := metrics.NewCollector()
	h.SetMetrics(m)
	cacheEngine := cache.NewEngine(cfg.Cache, m, zap.NewNop())
	h.SetCacheEngine(cacheEngine)

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("correlation_id", "test-corr")
		c.Locals("request_id", "test-req")
		return c.Next()
	})
	h.Register(app)

	body := `{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")

	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)

	snap := m.Snapshot()
	assert.Equal(t, int64(0), snap.CacheStores)
}

func TestCacheDisabledBypassesCache(t *testing.T) {
	app, m := setupCachedTestApp(t, false)

	body := `{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")

	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	snap := m.Snapshot()
	assert.Equal(t, int64(0), snap.CacheHits)
	assert.Equal(t, int64(0), snap.CacheMisses)
	assert.Equal(t, int64(0), snap.CacheStores)
}

func TestCacheDashboardEndpoint(t *testing.T) {
	app, _ := setupCachedTestApp(t, true)

	req := httptest.NewRequest(http.MethodGet, "/api/cache", nil)
	req.Header.Set("Authorization", "Bearer test-key")

	resp, err := app.Test(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", string(body))

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &result))
	assert.Equal(t, true, result["enabled"])
	stats, ok := result["stats"].(map[string]interface{})
	require.True(t, ok)
	assert.GreaterOrEqual(t, stats["hits"], float64(0))
	assert.GreaterOrEqual(t, stats["misses"], float64(0))
}

func TestCacheDashboardDisabled(t *testing.T) {
	app, _ := setupCachedTestApp(t, false)

	req := httptest.NewRequest(http.MethodGet, "/api/cache", nil)
	req.Header.Set("Authorization", "Bearer test-key")

	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, false, result["enabled"])
}

func TestCacheTTLExpiration(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&cachingStubProvider{
		name: "openai",
		response: &apitypes.ChatCompletionResponse{
			ID:      "chatcmpl-1",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "gpt-4o",
			Choices: []apitypes.Choice{{
				Index: 0,
				Message: &apitypes.Message{
					Role:    "assistant",
					Content: "expire-me",
				},
				FinishReason: strPtr("stop"),
			}},
			Usage: &apitypes.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		},
	})

	cfg := &config.Config{
		APIKey: "test-key",
		Routes: map[string]config.RouteConfig{
			"test-model": {Provider: "openai", ModelID: "gpt-4o"},
		},
		Cache: config.CacheConfig{
			Enabled:    true,
			TTL:        100 * time.Millisecond,
			MaxEntries: 100,
		},
	}
	engine := router.NewEngine(cfg, reg)
	h := handler.New(engine, reg, nil, zap.NewNop(), nil, nil)

	m := metrics.NewCollector()
	h.SetMetrics(m)
	cacheEngine := cache.NewEngine(cfg.Cache, m, zap.NewNop())
	h.SetCacheEngine(cacheEngine)

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("correlation_id", "test-corr")
		c.Locals("request_id", "test-req")
		return c.Next()
	})
	h.Register(app)

	body := `{"model":"test-model","messages":[{"role":"user","content":"expire"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")

	// First request — miss + store.
	resp1, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp1.StatusCode)

	// Wait for TTL to expire.
	time.Sleep(150 * time.Millisecond)

	// Second request — should miss again.
	resp2, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	snap := m.Snapshot()
	assert.GreaterOrEqual(t, snap.CacheMisses, int64(2))
}

func TestCacheEviction(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&cachingStubProvider{
		name: "openai",
		response: &apitypes.ChatCompletionResponse{
			ID:      "chatcmpl-1",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "gpt-4o",
			Choices: []apitypes.Choice{{
				Index: 0,
				Message: &apitypes.Message{
					Role:    "assistant",
					Content: "evict-me",
				},
				FinishReason: strPtr("stop"),
			}},
			Usage: &apitypes.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		},
	})

	cfg := &config.Config{
		APIKey: "test-key",
		Routes: map[string]config.RouteConfig{
			"test-model": {Provider: "openai", ModelID: "gpt-4o"},
		},
		Cache: config.CacheConfig{
			Enabled:    true,
			TTL:        5 * time.Minute,
			MaxEntries: 2,
		},
	}
	engine := router.NewEngine(cfg, reg)
	h := handler.New(engine, reg, nil, zap.NewNop(), nil, nil)

	m := metrics.NewCollector()
	h.SetMetrics(m)
	cacheEngine := cache.NewEngine(cfg.Cache, m, zap.NewNop())
	h.SetCacheEngine(cacheEngine)

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("correlation_id", "test-corr")
		c.Locals("request_id", "test-req")
		return c.Next()
	})
	h.Register(app)

	for i := 0; i < 5; i++ {
		// Vary messages to get different cache keys.
		varyBody := `{"model":"test-model","messages":[{"role":"user","content":"msg-` + string(rune('0'+i)) + `"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(varyBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer test-key")

	resp, err := app.Test(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	}

	// Cache should have evicted some entries due to max_entries=2.
	s := cacheEngine.Stats()
	assert.LessOrEqual(t, s.CurrentEntries, int64(2))
}

func TestCacheConcurrentAccess(t *testing.T) {
	app, m := setupCachedTestApp(t, true)

	body := `{"model":"test-model","messages":[{"role":"user","content":"concurrent"}]}`
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")

			resp, err := app.Test(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	snap := m.Snapshot()
	assert.GreaterOrEqual(t, snap.CacheHits+snap.CacheMisses, int64(10))
}
