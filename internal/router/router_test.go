package router_test

import (
	"context"
	"testing"

	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/config"
	"github.com/novexa/gateway/internal/provider"
	"github.com/novexa/gateway/internal/router"
)

func TestResolveStripsProviderPrefix(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{name: "groq"})
	reg.Register(&stubProvider{name: "openai"})

	engine := router.NewEngine(&config.Config{
		Routes: map[string]config.RouteConfig{
			"llama3-8b": {Provider: "groq"},
		},
	}, reg)

	resolved, err := engine.Resolve("groq/llama3-8b")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.ProviderName != "groq" {
		t.Fatalf("ProviderName = %q, want groq", resolved.ProviderName)
	}
	if resolved.ModelID != "llama3-8b" {
		t.Fatalf("ModelID = %q, want llama3-8b", resolved.ModelID)
	}
	if resolved.ProviderModelID != "llama3-8b" {
		t.Fatalf("ProviderModelID = %q, want llama3-8b", resolved.ProviderModelID)
	}
}

func TestResolveRejectsMismatchedProviderPrefix(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{name: "groq"})
	reg.Register(&stubProvider{name: "openai"})

	engine := router.NewEngine(&config.Config{
		Routes: map[string]config.RouteConfig{
			"llama3-8b": {Provider: "groq"},
		},
	}, reg)

	_, err := engine.Resolve("openai/llama3-8b")
	if err == nil {
		t.Fatal("expected error for mismatched provider prefix")
	}
}

func TestResolveBareModelIDUsesRoute(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{name: "openai"})

	engine := router.NewEngine(&config.Config{
		Routes: map[string]config.RouteConfig{
			"gpt-4o": {Provider: "openai", ModelID: "gpt-4o-2024-08-06"},
		},
	}, reg)

	resolved, err := engine.Resolve("gpt-4o")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.ProviderName != "openai" {
		t.Fatalf("ProviderName = %q, want openai", resolved.ProviderName)
	}
	if resolved.ProviderModelID != "gpt-4o-2024-08-06" {
		t.Fatalf("ProviderModelID = %q, want gpt-4o-2024-08-06", resolved.ProviderModelID)
	}
}

func TestResolveAutoSelectsWhenWired(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{name: "nvidia_nim"})

	engine := router.NewEngine(&config.Config{}, reg)
	engine.SetAutoSelector(&fixedAutoSelector{modelID: "meta/llama-3.1-8b-instruct"})

	resolved, err := engine.ResolveWithMessages("auto", []apitypes.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.ProviderName != "nvidia_nim" {
		t.Fatalf("ProviderName = %q, want nvidia_nim", resolved.ProviderName)
	}
	if resolved.ProviderModelID != "meta/llama-3.1-8b-instruct" {
		t.Fatalf("ProviderModelID = %q, want meta/llama-3.1-8b-instruct", resolved.ProviderModelID)
	}
	if resolved.ModelID != "auto" {
		t.Fatalf("ModelID = %q, want auto", resolved.ModelID)
	}
}

func TestResolveAutoReturnsErrorWhenNotWired(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{name: "nvidia_nim"})

	engine := router.NewEngine(&config.Config{}, reg)

	_, err := engine.Resolve("auto")
	if err == nil {
		t.Fatal("expected error when auto selector is not wired")
	}
}

type fixedAutoSelector struct {
	modelID string
}

func (f *fixedAutoSelector) Select(_ context.Context, _ string) (string, error) {
	return f.modelID, nil
}

type stubProvider struct {
	name string
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) ChatCompletion(context.Context, *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) ChatCompletionStream(context.Context, *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) Embeddings(context.Context, *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}

func (s *stubProvider) SupportsModel(string) bool { return false }
