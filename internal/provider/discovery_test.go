package provider

import (
	"context"
	"sync"
	"testing"
)

func TestDiscoveryByCapability(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Streaming: true, Vision: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{Streaming: true, Reasoning: true}))
	reg.Register(newTestProvider("groq", Capabilities{Streaming: true}))

	ctx := context.Background()

	result := reg.DiscoverByCapability(ctx, "streaming")
	if result.Count != 3 {
		t.Errorf("expected 3 streaming providers, got %d", result.Count)
	}
	if result.Query != "capability:streaming" {
		t.Errorf("expected query 'capability:streaming', got %s", result.Query)
	}
	if len(result.Providers) != 3 {
		t.Errorf("expected 3 providers, got %d", len(result.Providers))
	}
	if len(result.Metadata) != 3 {
		t.Errorf("expected 3 metadata entries, got %d", len(result.Metadata))
	}
}

func TestDiscoveryByModel(t *testing.T) {
	reg := NewRegistry()
	p1 := newTestProvider("openai", Capabilities{})
	p1.models = []string{"gpt-4o"}
	p2 := newTestProvider("anthropic", Capabilities{})
	p2.models = []string{"claude-3-opus"}
	reg.Register(p1)
	reg.Register(p2)

	ctx := context.Background()
	result := reg.DiscoverByModel(ctx, "gpt-4o")
	if result.Count != 1 {
		t.Errorf("expected 1 provider for gpt-4o, got %d", result.Count)
	}
	if result.Providers[0].Name() != "openai" {
		t.Errorf("expected openai, got %s", result.Providers[0].Name())
	}
}

func TestDiscoveryByCapabilities(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Streaming: true, Vision: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{Streaming: true, Reasoning: true}))
	reg.Register(newTestProvider("groq", Capabilities{Streaming: true}))

	ctx := context.Background()
	result := reg.DiscoverByCapabilities(ctx, []string{"streaming", "vision"})
	// Should find openai (has both) and potentially others with streaming
	if result.Count < 1 {
		t.Error("expected at least 1 provider")
	}
}

func TestDiscoveryStreaming(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Streaming: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{Streaming: true}))
	reg.Register(newTestProvider("groq", Capabilities{}))

	result := reg.DiscoverStreaming(context.Background())
	if result.Count != 2 {
		t.Errorf("expected 2 streaming providers, got %d", result.Count)
	}
}

func TestDiscoveryVision(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Vision: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{}))

	result := reg.DiscoverVision(context.Background())
	if result.Count != 1 {
		t.Errorf("expected 1 vision provider, got %d", result.Count)
	}
}

func TestDiscoveryReasoning(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Reasoning: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{Reasoning: true}))
	reg.Register(newTestProvider("groq", Capabilities{}))

	result := reg.DiscoverReasoning(context.Background())
	if result.Count != 2 {
		t.Errorf("expected 2 reasoning providers, got %d", result.Count)
	}
}

func TestDiscoveryTools(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{ToolCalling: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{Functions: true}))
	reg.Register(newTestProvider("groq", Capabilities{}))

	result := reg.DiscoverTools(context.Background())
	if result.Count != 2 {
		t.Errorf("expected 2 tools providers, got %d", result.Count)
	}
}

func TestDiscoveryStructured(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Structured: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{}))

	result := reg.DiscoverStructured(context.Background())
	if result.Count != 1 {
		t.Errorf("expected 1 structured provider, got %d", result.Count)
	}
}

func TestDiscoveryLongContext(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{LongContext: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{LongContext: true}))
	reg.Register(newTestProvider("groq", Capabilities{}))

	result := reg.DiscoverLongContext(context.Background())
	if result.Count != 2 {
		t.Errorf("expected 2 long-context providers, got %d", result.Count)
	}
}

func TestDiscoveryEmbeddings(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Embeddings: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{}))

	result := reg.DiscoverEmbeddings(context.Background())
	if result.Count != 1 {
		t.Errorf("expected 1 embeddings provider, got %d", result.Count)
	}
}

func TestDiscoveryConcurrent(t *testing.T) {
	reg := NewRegistry()
	for i := 0; i < 10; i++ {
		reg.Register(newTestProvider("provider-"+string(rune('a'+i)), Capabilities{Streaming: true}))
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx := context.Background()
			reg.DiscoverByCapability(ctx, "streaming")
			reg.DiscoverAll(ctx)
			reg.DiscoverByModel(ctx, "test")
		}(i)
	}
	wg.Wait()
}
