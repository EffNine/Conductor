package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
)

type routingStubProvider struct {
	name       string
	supportsAll bool
}

func (s *routingStubProvider) Name() string { return s.name }
func (s *routingStubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *routingStubProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (s *routingStubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (s *routingStubProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (s *routingStubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (s *routingStubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}
func (s *routingStubProvider) SupportsModel(string) bool { return s.supportsAll }
func (s *routingStubProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(s.name)
}

func TestNewRouterEngine(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})
	if engine == nil {
		t.Fatal("expected non-nil router engine")
	}
}

func TestRouterEngineSelectsBestProvider(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	healthStore := health.NewModelStatusStore(1, true)
	metricsStore := router.NewMetricsStore()
	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		HealthStore:  healthStore,
		MetricsStore: metricsStore,
		Weights:      config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "hello"},
		},
	}
	result, err := engine.SelectBestProvider(context.Background(), "gpt-4o", req)
	if err != nil {
		t.Fatalf("SelectBestProvider: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider")
	}
	if len(result.Decision.CandidateScores) != 2 {
		t.Fatalf("expected 2 candidate scores, got %d", len(result.Decision.CandidateScores))
	}
}

func TestRouterEngineHandlesNoProviders(t *testing.T) {
	reg := provider.NewRegistry()
	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	_, err := engine.SelectBestProvider(context.Background(), "gpt-4o", &apitypes.ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error for no providers")
	}
}

func TestRouterEngineFiltersUnhealthyProvider(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	healthStore := health.NewModelStatusStore(1, true)
	metricsStore := router.NewMetricsStore()

	// Mark openai as unhealthy.
	healthStore.RecordFailure("openai/gpt-4o", "openai", "gpt-4o", "probe failed", 0)

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		HealthStore:  healthStore,
		MetricsStore: metricsStore,
		Weights:      config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "hello"},
		},
	}
	result, err := engine.SelectBestProvider(context.Background(), "gpt-4o", req)
	if err != nil {
		t.Fatalf("SelectBestProvider: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Should still select a provider (groq as fallback).
	if result.Decision.SelectedProvider == "" {
		t.Fatal("expected selected provider when one is unhealthy")
	}
}

func TestRouterEngineCapabilityFilter(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{{
			Role: "user",
			Content: []apitypes.ContentPart{
				{Type: apitypes.ContentPartImageURL, ImageURL: &apitypes.ImageURLContent{URL: "https://example.com/img.png"}},
			},
		}},
	}
	result, err := engine.SelectBestProvider(context.Background(), "gpt-4o", req)
	if err != nil {
		t.Fatalf("SelectBestProvider: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Decision.CandidateScores) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.Decision.CandidateScores))
	}
}

func TestRouterEngineWeightUpdate(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	engine.UpdateWeights(config.RoutingWeights{
		Health:     80,
		Latency:    10,
		Cost:       5,
		Capability: 5,
	})

	w := engine.GetScorer().LoadWeights()
	if w.Health != 0.8 {
		t.Fatalf("health weight = %f, want 0.8", w.Health)
	}
	if w.Latency != 0.1 {
		t.Fatalf("latency weight = %f, want 0.1", w.Latency)
	}
}

func TestRouterEngineGetProviderScores(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})
	reg.Register(&routingStubProvider{name: "groq", supportsAll: true})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	scores := engine.GetProviderScores(router.CapabilityHint{})
	if len(scores) != 2 {
		t.Fatalf("expected 2 provider scores, got %d", len(scores))
	}
	for _, s := range scores {
		if s.Provider == "" {
			t.Fatal("expected non-empty provider name")
		}
	}
}

func TestRouterEngineRecordResult(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: true})

	metricsStore := router.NewMetricsStore()
	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry:     reg,
		MetricsStore: metricsStore,
		Weights:      config.DefaultRoutingWeights(),
	})

	engine.RecordResult("openai", 150, true)
	engine.RecordResult("openai", 200, true)
	engine.RecordResult("openai", 500, false)

	scores := engine.GetProviderScores(router.CapabilityHint{})
	if len(scores) == 0 {
		t.Fatal("expected scores after recording results")
	}
}

func TestRouterEngineNoProviderSupportsModel(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&routingStubProvider{name: "openai", supportsAll: false})

	engine := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	_, err := engine.SelectBestProvider(context.Background(), "nonexistent-model", &apitypes.ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error when no provider supports the model")
	}
}
