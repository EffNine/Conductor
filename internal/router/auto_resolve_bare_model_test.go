package router_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
)

// bareModelProvider is a minimal provider that claims every model.
type bareModelProvider struct {
	name string
}

func (p *bareModelProvider) Name() string                 { return p.name }
func (p *bareModelProvider) SupportsModel(id string) bool { return true }
func (p *bareModelProvider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{IsHealthy: true, LatencyMs: 10}, nil
}
func (p *bareModelProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return &apitypes.ChatCompletionResponse{}, nil
}
func (p *bareModelProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, nil
}
func (p *bareModelProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, nil
}
func (p *bareModelProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: "x", ModelID: "x", OwnedBy: p.name}}, nil
}
func (p *bareModelProvider) GetPricing(ctx context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (p *bareModelProvider) GetMetadata() provider.Metadata {
	return provider.DefaultMetadata(p.name)
}

func TestAutoResolveBareModelSingleProvider(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&bareModelProvider{name: "openai"})

	cfg := &config.Config{}
	cfg.Routing.AutoResolveBareModels = true
	engine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	resolved, err := engine.Resolve("gpt-4o")
	if err != nil {
		t.Fatalf("Resolve(gpt-4o): %v", err)
	}
	if resolved.ProviderName != "openai" {
		t.Fatalf("provider = %s, want openai", resolved.ProviderName)
	}
	if resolved.ProviderModelID != "gpt-4o" {
		t.Fatalf("provider model = %s, want gpt-4o", resolved.ProviderModelID)
	}
}

func TestAutoResolveBareModelAmbiguousStaysUnresolved(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&bareModelProvider{name: "openai"})
	reg.Register(&bareModelProvider{name: "groq"})

	engine, err := router.NewEngine(&config.Config{}, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if _, err := engine.Resolve("gpt-4o"); err == nil {
		t.Fatal("ambiguous bare model must not auto-resolve")
	}
}

func TestAutoResolveBareModelDisabledByConfig(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&bareModelProvider{name: "openai"})

	cfg := &config.Config{}
	cfg.Routing.AutoResolveBareModels = false
	engine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if _, err := engine.Resolve("gpt-4o"); err == nil {
		t.Fatal("bare model must stay unresolved when the feature is disabled")
	}
}

func TestAutoResolveSkipsVirtualModels(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&bareModelProvider{name: "openai"})

	engine, err := router.NewEngine(&config.Config{}, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// "fast" is a virtual category and must never resolve to a concrete
	// provider as if it were a literal upstream model name.
	if _, err := engine.Resolve(string(router.VirtualFast)); err == nil {
		t.Fatal("virtual model must not be hijacked by bare-model resolution")
	}
}

func TestConfiguredRouteBeatsBareModelResolution(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&bareModelProvider{name: "openai"})
	reg.Register(&bareModelProvider{name: "groq"})

	cfg := &config.Config{
		Routes: map[string]config.RouteConfig{"gpt-4o": {Provider: "groq"}},
	}
	engine, err := router.NewEngine(cfg, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	resolved, err := engine.Resolve("gpt-4o")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.ProviderName != "groq" {
		t.Fatalf("route must win over ambiguity fallback: provider = %s", resolved.ProviderName)
	}
}
