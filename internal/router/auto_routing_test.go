package router

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
)

// mockAutoSelector selects based on task text
type mockAutoSelector struct {
	model string
	err   error
}

func (m *mockAutoSelector) Select(ctx context.Context, task string) (string, error) {
	return m.model, m.err
}

func TestAutoMode_WithNVIDIA(t *testing.T) {
	reg := provider.NewRegistry()
	mockProv := &mockProvider{name: "nvidia_nim"}
	reg.Register(mockProv)

	engine := NewEngine(&config.Config{}, reg)
	engine.SetAutoSelector(&mockAutoSelector{model: "meta/llama-3.1-8b-instruct"})

	route, err := engine.Resolve("auto")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if route.ProviderName != "nvidia_nim" {
		t.Errorf("expected nvidia_nim, got: %s", route.ProviderName)
	}
	if route.ProviderModelID != "meta/llama-3.1-8b-instruct" {
		t.Errorf("expected meta/llama-3.1-8b-instruct, got: %s", route.ProviderModelID)
	}
}

func TestAutoMode_WithoutNVIDIA(t *testing.T) {
	reg := provider.NewRegistry()
	mockProv := &mockProvider{name: "openai"}
	reg.Register(mockProv)

	engine := NewEngine(&config.Config{}, reg)
	engine.SetAutoSelector(&mockAutoSelector{model: "gpt-4o"})

	route, err := engine.Resolve("auto")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if route.ProviderName != "openai" {
		t.Errorf("expected openai, got: %s", route.ProviderName)
	}
	if route.ProviderModelID != "gpt-4o" {
		t.Errorf("expected gpt-4o, got: %s", route.ProviderModelID)
	}
}

func TestAutoMode_MultipleProviders_PicksHealthy(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&mockProvider{name: "anthropic"})
	reg.Register(&mockProvider{name: "openai"})

	engine := NewEngine(&config.Config{}, reg)
	engine.SetAutoSelector(&mockAutoSelector{model: "claude-3-5-sonnet"})

	route, err := engine.Resolve("auto")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// Registry iteration order is non-deterministic; just verify it picked a registered provider.
	if route.ProviderName != "anthropic" && route.ProviderName != "openai" {
		t.Errorf("expected anthropic or openai, got: %s", route.ProviderName)
	}
	if route.ProviderModelID != "claude-3-5-sonnet" {
		t.Errorf("expected claude-3-5-sonnet, got: %s", route.ProviderModelID)
	}
}

func TestAutoMode_NoProviders_FailsGracefully(t *testing.T) {
	reg := provider.NewRegistry()
	engine := NewEngine(&config.Config{}, reg)
	engine.SetAutoSelector(&mockAutoSelector{model: "gpt-4o"})

	_, err := engine.Resolve("auto")
	if err == nil {
		t.Fatal("expected error with no providers")
	}
}

func TestAutoMode_AllBreakersOpen(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&mockProvider{name: "openai"})

	engine := NewEngine(&config.Config{
		Circuit: config.CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 3,
		},
	}, reg)
	engine.SetAutoSelector(&mockAutoSelector{model: "gpt-4o"})

	// Open the breaker
	bp := engine.BreakerPool()
	if bp != nil {
		for i := 0; i < 5; i++ {
			bp.Get("openai").RecordFailure()
		}
	}

	_, err := engine.Resolve("auto")
	if err == nil {
		t.Fatal("expected error when all breakers open")
	}
}

func TestExplicitProviderRouting_Unaffected(t *testing.T) {
	reg := provider.NewRegistry()
	mockProv := &mockProvider{name: "openai"}
	reg.Register(mockProv)

	engine := NewEngine(&config.Config{}, reg)
	engine.SetAutoSelector(&mockAutoSelector{model: "gpt-4o"})

	// Explicit provider-prefixed model should bypass auto mode
	route, err := engine.Resolve("openai/gpt-4o")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if route.ProviderName != "openai" {
		t.Errorf("expected openai, got: %s", route.ProviderName)
	}
}

// mockProvider is a minimal provider implementation for testing
type mockProvider struct {
	name string
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{IsHealthy: true, LatencyMs: 10}, nil
}
func (m *mockProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return &apitypes.ChatCompletionResponse{}, nil
}
func (m *mockProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, nil
}
func (m *mockProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, nil
}
func (m *mockProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (m *mockProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (m *mockProvider) SupportsModel(modelID string) bool { return true }
func (m *mockProvider) GetMetadata() provider.Metadata {
	return provider.Metadata{Name: m.name}
}
