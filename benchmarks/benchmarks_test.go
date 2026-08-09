package benchmarks

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/cache"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/metrics"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/provider/openaibase"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

// mockProvider is a test-only provider that returns canned responses.
type mockProvider struct {
	name      string
	respCount int
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	m.respCount++
	return &apitypes.ChatCompletionResponse{
		ID:      fmt.Sprintf("mock-%d", m.respCount),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []apitypes.Choice{{
			Index: 0,
			Message: &apitypes.Message{
				Role:    "assistant",
				Content: "mock response body",
			},
			FinishReason: func() *string { s := "stop"; return &s }(),
		}},
		Usage: &apitypes.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}, nil
}

func (m *mockProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	ch := make(chan apitypes.StreamChunk, 5)
	ch <- apitypes.StreamChunk{
		ID:      "mock-stream",
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []apitypes.Choice{{
			Index: 0,
			Delta: &apitypes.Message{Role: "assistant", Content: "hello"},
		}},
	}
	ch <- apitypes.StreamChunk{
		Done:    true,
		ID:      "mock-stream",
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []apitypes.Choice{{
			Index:        0,
			Delta:        &apitypes.Message{Role: "assistant", Content: " world"},
			FinishReason: func() *string { s := "stop"; return &s }(),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return &apitypes.EmbeddingResponse{
		Object: "list",
		Data: []apitypes.EmbeddingData{{
			Object:    "embedding",
			Embedding: []float64{0.1, 0.2, 0.3},
			Index:     0,
		}},
		Model: req.Model,
		Usage: &apitypes.Usage{PromptTokens: 1, TotalTokens: 1},
	}, nil
}

func (m *mockProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: m.name + "/gpt-4o", ModelID: "gpt-4o", OwnedBy: m.name}}, nil
}

func (m *mockProvider) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return map[string]provider.PricingInfo{"gpt-4o": {UnitType: "token", UnitSize: 1000, InputPrice: 0.003, OutputPrice: 0.012}}, nil
}

func (m *mockProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: m.name, IsHealthy: true, LatencyMs: 5, CheckedAt: time.Now()}, nil
}

func (m *mockProvider) SupportsModel(_ string) bool { return true }
func (m *mockProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(m.name)
}

func setupTestEnvironment(t testing.TB) (*router.Engine, *provider.Registry, *metrics.Collector, *cache.Engine) {
	logger, _ := zap.NewDevelopment()
	m := metrics.NewCollector()
	reg := provider.NewRegistry()

	reg.Register(&mockProvider{name: "mock_openai"})
	reg.Register(&mockProvider{name: "mock_anthropic"})
	reg.Register(&mockProvider{name: "mock_gemini"})

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{
			"gpt-4o": {Provider: "mock_openai"},
			"claude": {Provider: "mock_anthropic", ModelID: "claude-3-5-sonnet"},
			"gemini": {Provider: "mock_gemini"},
		},
		Aliases: map[string]string{"fast": "gpt-4o", "smart": "gpt-4o"},
		Fallbacks: map[string][]config.FallbackConfig{
			"gpt-4o": {{Provider: "mock_gemini"}},
		},
		Circuit: config.CircuitBreakerConfig{Enabled: true},
	}
	eng := router.NewEngine(cfg, reg)

	cacheCfg := config.CacheConfig{
		Enabled:        true,
		TTL:            5 * time.Minute,
		MaxEntries:     1024,
		EvictionPolicy: "lru",
	}
	ce := cache.NewEngine(cacheCfg, m, logger)

	return eng, reg, m, ce
}

// ---- Routing Benchmarks ----

func BenchmarkRouterResolve(b *testing.B) {
	eng, _, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := eng.ResolveWithFallback("gpt-4o")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRouterResolveProviderPrefixed(b *testing.B) {
	eng, _, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := eng.ResolveWithFallback("mock_openai/gpt-4o")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRouterResolveWithMessages(b *testing.B) {
	eng, _, _, _ := setupTestEnvironment(b)
	msgs := []apitypes.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Write a short story about a robot learning to love."},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := eng.ResolveWithFallbackAndMessages("gpt-4o", msgs)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRouterResolveAlias(b *testing.B) {
	eng, _, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := eng.ResolveWithFallback("fast")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRouterResolveWithContext(b *testing.B) {
	eng, _, _, _ := setupTestEnvironment(b)
	ctx := context.Background()
	msgs := []apitypes.Message{{Role: "user", Content: "Explain quantum computing."}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := eng.ResolveWithContext(ctx, "gpt-4o", msgs)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ---- Provider Registry Benchmarks ----

func BenchmarkRegistryGet(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, ok := reg.Get("mock_openai")
		if !ok {
			b.Fatal("provider not found")
		}
	}
}

func BenchmarkRegistryAll(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reg.All()
	}
}

func BenchmarkRegistryNames(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reg.Names()
	}
}

func BenchmarkRegistryFindByCapability(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reg.FindByCapability("streaming")
	}
}

func BenchmarkRegistryGetProviderInfo(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = reg.GetProviderInfo("mock_openai")
	}
}

// ---- Cache Benchmarks ----

func BenchmarkCacheGetHit(b *testing.B) {
	_, _, m, ce := setupTestEnvironment(b)
	_ = m
	key := "resp:abcd1234"
	val := []byte(`{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"}}]}`)
	ce.Set(key, val, 5*time.Minute)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ce.Get(key)
	}
}

func BenchmarkCacheGetMiss(b *testing.B) {
	_, _, m, ce := setupTestEnvironment(b)
	_ = m
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ce.Get(fmt.Sprintf("nonexistent-%d", i))
	}
}

func BenchmarkCacheSet(b *testing.B) {
	_, _, m, ce := setupTestEnvironment(b)
	_ = m
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ce.Set(fmt.Sprintf("key-%d", i), []byte(`{"data":"value"}`), 5*time.Minute)
	}
}

func BenchmarkCacheBuildKey(b *testing.B) {
	model := "gpt-4o"
	messages := []interface{}{
		map[string]interface{}{"role": "user", "content": "Hello, world! This is a test message for benchmarking."},
		map[string]interface{}{"role": "assistant", "content": "Hello! How can I assist you today?"},
	}
	params := map[string]interface{}{
		"temperature": 0.7, "top_p": 1.0, "max_tokens": 256,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.BuildCacheKey(model, messages, params)
	}
}

func BenchmarkCacheEngineCacheResponse(b *testing.B) {
	_, _, m, ce := setupTestEnvironment(b)
	_ = m
	key := "resp:benchmark-key"
	resp := &apitypes.ChatCompletionResponse{
		ID: "bench", Object: "chat.completion", Created: time.Now().Unix(), Model: "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "bench"}}},
		Usage: &apitypes.Usage{PromptTokens: 5, CompletionTokens: 10, TotalTokens: 15},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ce.CacheResponse(key, resp)
	}
}

// ---- Circuit Breaker Benchmarks ----

func BenchmarkBreakerAllow(b *testing.B) {
	cfg := breaker.DefaultConfig()
	bh := breaker.New(cfg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bh.Allow()
	}
}

func BenchmarkBreakerRecordSuccess(b *testing.B) {
	cfg := breaker.DefaultConfig()
	bh := breaker.New(cfg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bh.RecordSuccess()
	}
}

func BenchmarkBreakerRecordFailure(b *testing.B) {
	cfg := breaker.DefaultConfig()
	bh := breaker.New(cfg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bh.RecordFailure()
	}
}

func BenchmarkBreakerStats(b *testing.B) {
	cfg := breaker.DefaultConfig()
	bh := breaker.New(cfg)
	for i := 0; i < 10; i++ {
		bh.RecordSuccess()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bh.Stats()
	}
}

func BenchmarkBreakerPoolGet(b *testing.B) {
	pool := router.NewBreakerPool(breaker.DefaultConfig())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pool.Get("mock_openai")
	}
}

func BenchmarkBreakerPoolStats(b *testing.B) {
	pool := router.NewBreakerPool(breaker.DefaultConfig())
	for i := 0; i < 10; i++ {
		pool.Get(fmt.Sprintf("provider-%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pool.Stats()
	}
}

// ---- Metrics Benchmarks ----

func BenchmarkMetricsSnapshot(b *testing.B) {
	m := metrics.NewCollector()
	for i := 0; i < 100; i++ {
		m.IncrementRequests()
		m.IncrementCacheHits()
		m.RecordProviderLatency(50)
		m.RecordCacheLookupLatency(2)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Snapshot()
	}
}

func BenchmarkMetricsIncrementRequests(b *testing.B) {
	m := metrics.NewCollector()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.IncrementRequests()
	}
}

func BenchmarkMetricsRecordProviderLatency(b *testing.B) {
	m := metrics.NewCollector()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.RecordProviderLatency(int64(i % 1000))
	}
}

func BenchmarkMetricsRecordProviderLatencyForProvider(b *testing.B) {
	m := metrics.NewCollector()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.RecordProviderLatencyForProvider("mock_openai", int64(i%1000))
	}
}

func BenchmarkMetricsRecordStreamOutcome(b *testing.B) {
	m := metrics.NewCollector()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.RecordStreamOutcome("mock_openai", metrics.StreamCompleted, 25, 4096, 1500)
	}
}

func BenchmarkMetricsRecordStreamStarted(b *testing.B) {
	m := metrics.NewCollector()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.RecordStreamStarted("mock_openai")
	}
}

// ---- Cache Hash Benchmarks ----

func BenchmarkBuildCacheKeySmallMessages(b *testing.B) {
	model := "gpt-4o"
	messages := []interface{}{
		map[string]interface{}{"role": "user", "content": "Hi"},
	}
	params := map[string]interface{}{"temperature": 0.7}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.BuildCacheKey(model, messages, params)
	}
}

func BenchmarkBuildCacheKeyLargeMessages(b *testing.B) {
	model := "gpt-4o"
	messages := []interface{}{
		map[string]interface{}{"role": "system", "content": "You are a helpful assistant. " + string(make([]byte, 2000))},
		map[string]interface{}{"role": "user", "content": "Explain the theory of relativity in detail. " + string(make([]byte, 1500))},
		map[string]interface{}{"role": "assistant", "content": "The theory of relativity, developed by Albert Einstein, consists of two interrelated physics theories... " + string(make([]byte, 1000))},
	}
	params := map[string]interface{}{
		"temperature": 0.7, "top_p": 1.0, "max_tokens": 1024,
		"presence_penalty": 0.0, "frequency_penalty": 0.0,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.BuildCacheKey(model, messages, params)
	}
}

// ---- Router Engine (Intelligent Routing) Benchmarks ----

func BenchmarkRouterEngineSelectBestProvider(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	reg := provider.NewRegistry()
	reg.Register(&mockProvider{name: "mock_openai"})
	reg.Register(&mockProvider{name: "mock_anthropic"})
	reg.Register(&mockProvider{name: "mock_gemini"})

	metricsStore := router.NewMetricsStore()
	breakerPool := router.NewBreakerPool(breaker.DefaultConfig())

	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: metricsStore,
		BreakerPool:  breakerPool,
		Logger:       logger,
		Weights:      config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "Hello"}},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := eng.SelectBestProvider(context.Background(), "gpt-4o", req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRouterEngineGetProviderScores(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	reg := provider.NewRegistry()
	reg.Register(&mockProvider{name: "mock_openai"})
	reg.Register(&mockProvider{name: "mock_anthropic"})
	reg.Register(&mockProvider{name: "mock_gemini"})

	metricsStore := router.NewMetricsStore()
	breakerPool := router.NewBreakerPool(breaker.DefaultConfig())

	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: metricsStore,
		BreakerPool:  breakerPool,
		Logger:       logger,
		Weights:      config.DefaultRoutingWeights(),
	})

	capHint := router.CapabilityHint{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = eng.GetProviderScores(capHint)
	}
}

func BenchmarkRouterEngineRecordResult(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	reg := provider.NewRegistry()
	reg.Register(&mockProvider{name: "mock_openai"})

	metricsStore := router.NewMetricsStore()
	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: metricsStore,
		Logger:       logger,
		Weights:      config.DefaultRoutingWeights(),
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eng.RecordResult("mock_openai", int64(i%500), i%3 != 0)
	}
}

// ---- LRU Cache Benchmarks ----

func BenchmarkLRUCacheGetHit(b *testing.B) {
	c := cache.NewLRUCache(1024)
	for i := 0; i < 100; i++ {
		c.Set(fmt.Sprintf("key-%d", i), []byte("value"), 5*time.Minute)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get(fmt.Sprintf("key-%d", i%100))
	}
}

func BenchmarkLRUCacheGetMiss(b *testing.B) {
	c := cache.NewLRUCache(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get(fmt.Sprintf("nonexistent-%d", i))
	}
}

func BenchmarkLRUCacheSet(b *testing.B) {
	c := cache.NewLRUCache(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set(fmt.Sprintf("key-%d", i), []byte("some value that is moderately long for benchmarking purposes"), 5*time.Minute)
	}
}

func BenchmarkLRUCacheSetThenGet(b *testing.B) {
	c := cache.NewLRUCache(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set(fmt.Sprintf("key-%d", i), []byte("value"), 5*time.Minute)
		_, _ = c.Get(fmt.Sprintf("key-%d", i))
	}
}

// ---- Discovery Benchmarks ----

func BenchmarkDiscoveryByCapability(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reg.DiscoverByCapability(ctx, "streaming")
	}
}

func BenchmarkDiscoveryByModel(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reg.DiscoverByModel(ctx, "gpt-4o")
	}
}

func BenchmarkDiscoveryAll(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reg.DiscoverAll(ctx)
	}
}

func BenchmarkDiscoveryByCapabilities(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reg.DiscoverByCapabilities(ctx, []string{"streaming", "vision"})
	}
}

// ---- Fallback Resolution Benchmarks ----

func BenchmarkResolveWithFallback(b *testing.B) {
	eng, _, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, fallbacks, err := eng.ResolveWithFallback("gpt-4o")
		if err != nil {
			b.Fatal(err)
		}
		if len(fallbacks) == 0 {
			b.Fatal("expected fallbacks")
		}
	}
}

func BenchmarkResolveWithFallbackAndMessages(b *testing.B) {
	eng, _, _, _ := setupTestEnvironment(b)
	msgs := []apitypes.Message{{Role: "user", Content: "Tell me a joke"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := eng.ResolveWithFallbackAndMessages("gpt-4o", msgs)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ---- OpenAI Base Provider Benchmarks ----

func BenchmarkOpenAIChatCompletionMarshal(b *testing.B) {
	bp := openaibase.New("mock", "test-key", "https://api.example.com/v1", 60*time.Second)
	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "Hello, this is a test message for benchmarking JSON marshaling performance."},
		},
		Temperature: func() *float64 { f := 0.7; return &f }(),
		MaxTokens:   func() *int { i := 256; return &i }(),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = bp.ChatCompletion(context.Background(), req)
	}
	_ = bp
}

// ---- Concurrency Stress Benchmarks ----

func BenchmarkRegistryConcurrentGets(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = reg.Get([]string{"mock_openai", "mock_anthropic", "mock_gemini"}[i%3])
			i++
		}
	})
}

func BenchmarkCacheConcurrentGetSet(b *testing.B) {
	_, _, m, ce := setupTestEnvironment(b)
	_ = m
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%256)
			ce.Get(key)
			ce.Set(key, []byte("value"), 5*time.Minute)
			i++
		}
	})
}

func BenchmarkBreakerPoolConcurrentAccess(b *testing.B) {
	pool := router.NewBreakerPool(breaker.DefaultConfig())
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			bh := pool.Get(fmt.Sprintf("provider-%d", i%5))
			_ = bh.Allow()
			i++
		}
	})
}

func BenchmarkScorerCompositeScore(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	reg := provider.NewRegistry()
	reg.Register(&mockProvider{name: "mock_openai"})
	reg.Register(&mockProvider{name: "mock_anthropic"})

	metricsStore := router.NewMetricsStore()
	eng := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: metricsStore,
		Logger:       logger,
		Weights:      config.DefaultRoutingWeights(),
	})

	c := router.Candidate{
		ProviderName:    "mock_openai",
		ProviderModelID: "gpt-4o",
		HealthScore:     0.9,
		LatencyMs:       120,
		CostPerToken:    func() *float64 { c := 0.00001; return &c }(),
		Capabilities:    router.Capabilities{Streaming: true},
		IsAvailable:     true,
	}
	hint := router.CapabilityHint{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = eng.GetScorer().CompositeScore(c, hint)
	}
}

func BenchmarkProviderRegistryForEach(b *testing.B) {
	_, reg, _, _ := setupTestEnvironment(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reg.ForEach(func(name string, p provider.Provider, meta provider.Metadata) {
			_ = name
			_ = p
			_ = meta
		})
	}
}

// ---- Memory / Allocation Benchmarks ----

func BenchmarkCacheBuildKeyAllocations(b *testing.B) {
	model := "gpt-4o"
	messages := []interface{}{
		map[string]interface{}{"role": "user", "content": "Write a short story about artificial intelligence and its impact on humanity."},
	}
	params := map[string]interface{}{"temperature": 0.7, "max_tokens": 512}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.BuildCacheKey(model, messages, params)
	}
}

func BenchmarkLRUCacheSetAllocations(b *testing.B) {
	c := cache.NewLRUCache(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set(fmt.Sprintf("key-%d", i), []byte("benchmark value for allocation measurement"), 5*time.Minute)
	}
}

// ---- Utility Functions ----

func printRuntimeInfo() {
	fmt.Printf("Go version: %s\n", runtime.Version())
	fmt.Printf("Number of CPUs: %d\n", runtime.NumCPU())
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	fmt.Printf("HeapAlloc MB: %.2f\n", float64(m.HeapAlloc)/1024/1024)
	fmt.Printf("NumGC: %d\n", m.NumGC)
}

func BenchmarkMain(b *testing.B) {
	printRuntimeInfo()
}
