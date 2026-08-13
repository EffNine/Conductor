package provider

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
)

type testProvider struct {
	name   string
	models []string
	caps   Capabilities
}

func newTestProvider(name string, caps Capabilities) *testProvider {
	return &testProvider{name: name, caps: caps}
}

func (p *testProvider) Name() string { return p.name }
func (p *testProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	return nil, nil
}
func (p *testProvider) ChatCompletionStream(ctx context.Context, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, nil
}
func (p *testProvider) Embeddings(ctx context.Context, req *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, nil
}
func (p *testProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return nil, nil
}
func (p *testProvider) GetPricing(ctx context.Context) (map[string]PricingInfo, error) {
	return nil, nil
}
func (p *testProvider) HealthCheck(ctx context.Context) (*HealthStatus, error) {
	return &HealthStatus{Provider: p.name, IsHealthy: true}, nil
}
func (p *testProvider) SupportsModel(modelID string) bool {
	for _, m := range p.models {
		if m == modelID {
			return true
		}
	}
	return false
}
func (p *testProvider) GetMetadata() Metadata {
	meta := DefaultMetadata(p.name)
	meta.Capabilities = p.caps
	return meta
}

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	p := newTestProvider("openai", Capabilities{Streaming: true, Vision: true})
	reg.Register(p)

	got, ok := reg.Get("openai")
	if !ok {
		t.Fatal("expected provider to be registered")
	}
	if got.Name() != "openai" {
		t.Errorf("expected openai, got %s", got.Name())
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("expected nonexistent provider not to be found")
	}
}

func TestRegistryGetMetadata(t *testing.T) {
	reg := NewRegistry()
	p := newTestProvider("openai", Capabilities{Streaming: true, Vision: true, ToolCalling: true})
	reg.Register(p)

	meta, ok := reg.GetMetadata("openai")
	if !ok {
		t.Fatal("expected metadata to be found")
	}
	if meta.Name != "openai" {
		t.Errorf("expected name openai, got %s", meta.Name)
	}
	if !meta.Capabilities.Streaming {
		t.Error("expected streaming capability")
	}
	if !meta.Capabilities.Vision {
		t.Error("expected vision capability")
	}
}

func TestRegistryUnregister(t *testing.T) {
	reg := NewRegistry()
	p := newTestProvider("openai", Capabilities{})
	reg.Register(p)

	if reg.Count() != 1 {
		t.Errorf("expected 1 provider, got %d", reg.Count())
	}

	removed := reg.Unregister("openai")
	if !removed {
		t.Error("expected unregister to succeed")
	}

	_, ok := reg.Get("openai")
	if ok {
		t.Error("expected provider to be unregistered")
	}

	if reg.Count() != 0 {
		t.Errorf("expected 0 providers, got %d", reg.Count())
	}

	// Unregistering again should return false
	removed = reg.Unregister("openai")
	if removed {
		t.Error("expected unregister of nonexistent provider to fail")
	}
}

func TestRegistryClear(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{}))
	reg.Register(newTestProvider("anthropic", Capabilities{}))

	reg.Clear()

	if reg.Count() != 0 {
		t.Errorf("expected 0 providers after clear, got %d", reg.Count())
	}
	if len(reg.Names()) != 0 {
		t.Errorf("expected empty names after clear")
	}
}

func TestRegistryNames(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{}))
	reg.Register(newTestProvider("anthropic", Capabilities{}))

	names := reg.Names()
	if len(names) != 2 {
		t.Errorf("expected 2 names, got %d", len(names))
	}
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["openai"] || !nameSet["anthropic"] {
		t.Error("expected both openai and anthropic in names")
	}
}

func TestRegistryAll(t *testing.T) {
	reg := NewRegistry()
	p1 := newTestProvider("openai", Capabilities{})
	p2 := newTestProvider("anthropic", Capabilities{})
	reg.Register(p1)
	reg.Register(p2)

	all := reg.All()
	if len(all) != 2 {
		t.Errorf("expected 2 providers, got %d", len(all))
	}
}

func TestRegistryFindByCapability(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Streaming: true, Vision: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{Streaming: true}))
	reg.Register(newTestProvider("groq", Capabilities{Streaming: true, ToolCalling: true}))

	streaming := reg.FindByCapability("streaming")
	if len(streaming) != 3 {
		t.Errorf("expected 3 streaming providers, got %d", len(streaming))
	}

	vision := reg.FindByCapability("vision")
	if len(vision) != 1 {
		t.Errorf("expected 1 vision provider, got %d", len(vision))
	}
	if vision[0].Name() != "openai" {
		t.Errorf("expected openai, got %s", vision[0].Name())
	}

	tools := reg.FindByCapability("tools")
	if len(tools) != 1 {
		t.Errorf("expected 1 tools provider, got %d", len(tools))
	}
}

func TestRegistryFindByModel(t *testing.T) {
	reg := NewRegistry()
	p1 := newTestProvider("openai", Capabilities{})
	p1.models = []string{"gpt-4o", "gpt-3.5-turbo"}
	p2 := newTestProvider("anthropic", Capabilities{})
	p2.models = []string{"claude-3-opus"}
	reg.Register(p1)
	reg.Register(p2)

	got := reg.FindByModel("gpt-4o")
	if len(got) != 1 {
		t.Errorf("expected 1 provider for gpt-4o, got %d", len(got))
	}
	if got[0].Name() != "openai" {
		t.Errorf("expected openai, got %s", got[0].Name())
	}

	got = reg.FindByModel("claude-3-opus")
	if len(got) != 1 {
		t.Errorf("expected 1 provider for claude-3-opus, got %d", len(got))
	}
	if got[0].Name() != "anthropic" {
		t.Errorf("expected anthropic, got %s", got[0].Name())
	}
}

func TestRegistryFindByCapabilityAndModel(t *testing.T) {
	reg := NewRegistry()
	p1 := newTestProvider("openai", Capabilities{Streaming: true, Vision: true})
	p1.models = []string{"gpt-4o"}
	p2 := newTestProvider("anthropic", Capabilities{Streaming: true})
	p2.models = []string{"claude-3-opus"}
	p3 := newTestProvider("groq", Capabilities{Streaming: true})
	p3.models = []string{"llama-3.1-70b"}
	reg.Register(p1)
	reg.Register(p2)
	reg.Register(p3)

	// Streaming + gpt-4o should only match openai
	got := reg.FindByCapabilityAndModel("streaming", "gpt-4o")
	if len(got) != 1 {
		t.Errorf("expected 1 provider, got %d", len(got))
	}
	if got[0].Name() != "openai" {
		t.Errorf("expected openai, got %s", got[0].Name())
	}

	// Streaming + llama should match groq
	got = reg.FindByCapabilityAndModel("streaming", "llama-3.1-70b")
	if len(got) != 1 {
		t.Errorf("expected 1 provider, got %d", len(got))
	}
	if got[0].Name() != "groq" {
		t.Errorf("expected groq, got %s", got[0].Name())
	}
}

func TestRegistryGetRegistrationTime(t *testing.T) {
	reg := NewRegistry()
	before := time.Now().UTC()
	reg.Register(newTestProvider("openai", Capabilities{}))
	after := time.Now().UTC()

	tm, ok := reg.GetRegistrationTime("openai")
	if !ok {
		t.Fatal("expected registration time to be found")
	}
	if tm.Before(before) || tm.After(after) {
		t.Errorf("expected registration time between %v and %v, got %v", before, after, tm)
	}

	_, ok = reg.GetRegistrationTime("nonexistent")
	if ok {
		t.Error("expected nonexistent provider to not have registration time")
	}
}

func TestRegistryGetProviderInfo(t *testing.T) {
	reg := NewRegistry()
	p := newTestProvider("openai", Capabilities{Streaming: true})
	reg.Register(p)

	gotP, meta, ok := reg.GetProviderInfo("openai")
	if !ok {
		t.Fatal("expected provider info to be found")
	}
	if gotP.Name() != "openai" {
		t.Errorf("expected openai, got %s", gotP.Name())
	}
	if meta.Name != "openai" {
		t.Errorf("expected metadata name openai, got %s", meta.Name)
	}
	if !meta.Capabilities.Streaming {
		t.Error("expected streaming capability in metadata")
	}

	_, _, ok = reg.GetProviderInfo("nonexistent")
	if ok {
		t.Error("expected nonexistent provider info to not be found")
	}
}

func TestRegistryForEach(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Streaming: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{Streaming: true, Vision: true}))

	called := make(map[string]bool)
	reg.ForEach(func(name string, p Provider, meta Metadata) {
		called[name] = true
		if name != p.Name() {
			t.Errorf("expected name %s, got %s", name, p.Name())
		}
		if meta.Name != name {
			t.Errorf("expected metadata name %s, got %s", name, meta.Name)
		}
	})

	if len(called) != 2 {
		t.Errorf("expected ForEach to be called 2 times, got %d", len(called))
	}
	if !called["openai"] || !called["anthropic"] {
		t.Error("expected ForEach to be called for openai and anthropic")
	}
}

func TestRegistryDuplicateRegistration(t *testing.T) {
	reg := NewRegistry()
	p1 := newTestProvider("openai", Capabilities{})
	p2 := newTestProvider("openai", Capabilities{Streaming: true})
	reg.Register(p1)
	reg.Register(p2) // duplicate - should replace

	got, ok := reg.Get("openai")
	if !ok {
		t.Fatal("expected provider to be registered")
	}
	if got.Name() != "openai" {
		t.Errorf("expected openai, got %s", got.Name())
	}
	if reg.Count() != 1 {
		t.Errorf("expected 1 provider, got %d", reg.Count())
	}
}

func TestRegistryConcurrentRegistration(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := "provider-" + string(rune('a'+idx%26))
			p := newTestProvider(name, Capabilities{Streaming: true})
			reg.Register(p)
		}(i)
	}
	wg.Wait()

	if reg.Count() == 0 {
		t.Error("expected at least one provider to be registered")
	}
}

func TestRegistryConcurrentLookup(t *testing.T) {
	reg := NewRegistry()
	for i := 0; i < 10; i++ {
		reg.Register(newTestProvider("provider-"+string(rune('a'+i)), Capabilities{}))
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := "provider-" + string(rune('a'+idx%10))
			_, ok := reg.Get(name)
			if !ok {
				t.Errorf("expected to find provider %s", name)
			}
			_, ok = reg.GetMetadata(name)
			if !ok {
				t.Errorf("expected to find metadata for %s", name)
			}
		}(i)
	}
	wg.Wait()
}

func TestRegistryConsistency(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Streaming: true, Vision: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{Streaming: true}))
	reg.Register(newTestProvider("groq", Capabilities{Streaming: true}))

	names := reg.Names()
	all := reg.All()
	if len(names) != len(all) {
		t.Errorf("names (%d) != all (%d)", len(names), len(all))
	}
	if reg.Count() != len(names) {
		t.Errorf("count (%d) != names (%d)", reg.Count(), len(names))
	}

	for _, n := range names {
		p, ok := reg.Get(n)
		if !ok {
			t.Errorf("provider %s not found via Get", n)
		}
		if p.Name() != n {
			t.Errorf("Get returned wrong provider for %s", n)
		}
		meta, ok := reg.GetMetadata(n)
		if !ok {
			t.Errorf("metadata for %s not found", n)
		}
		if meta.Name != n {
			t.Errorf("metadata name mismatch for %s", n)
		}
		regTime, ok := reg.GetRegistrationTime(n)
		if !ok {
			t.Errorf("registration time for %s not found", n)
		}
		if regTime.IsZero() {
			t.Errorf("registration time is zero for %s", n)
		}
	}
}

func TestRegistryDiscoverStreaming(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{Streaming: true}))
	reg.Register(newTestProvider("anthropic", Capabilities{Streaming: true}))
	reg.Register(newTestProvider("groq", Capabilities{}))

	result := reg.DiscoverStreaming(context.Background())
	if result.Count != 2 {
		t.Errorf("expected 2 streaming providers, got %d", result.Count)
	}
	if result.Query != "capability:streaming" {
		t.Errorf("expected query 'capability:streaming', got %s", result.Query)
	}
}

func TestRegistryDiscoverAll(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newTestProvider("openai", Capabilities{}))
	reg.Register(newTestProvider("anthropic", Capabilities{}))

	result := reg.DiscoverAll(context.Background())
	if result.Count != 2 {
		t.Errorf("expected 2 providers, got %d", result.Count)
	}
	if result.Query != "all" {
		t.Errorf("expected query 'all', got %s", result.Query)
	}
}

func TestMetadataSupportedCapabilities(t *testing.T) {
	meta := NewMetadata("openai", Capabilities{
		Streaming: true, Vision: true, ToolCalling: true,
		Structured: true, LongContext: true,
	})
	caps := meta.SupportedCapabilities()
	expected := []string{"streaming", "vision", "tools", "structured", "long-context"}
	if len(caps) != len(expected) {
		t.Errorf("expected %d capabilities, got %d: %v", len(expected), len(caps), caps)
	}
	for _, exp := range expected {
		found := false
		for _, c := range caps {
			if c == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected capability %s in %v", exp, caps)
		}
	}
}

func TestMetadataHasCapability(t *testing.T) {
	meta := NewMetadata("openai", Capabilities{Streaming: true, Vision: true})
	if !meta.HasCapability("streaming") {
		t.Error("expected streaming capability")
	}
	if !meta.HasCapability("vision") {
		t.Error("expected vision capability")
	}
	if meta.HasCapability("reasoning") {
		t.Error("did not expect reasoning capability")
	}
}

func TestDefaultMetadata(t *testing.T) {
	meta := DefaultMetadata("openai")
	if meta.Name != "openai" {
		t.Errorf("expected name openai, got %s", meta.Name)
	}
	if meta.DisplayName != "OpenAI" {
		t.Errorf("expected display name OpenAI, got %s", meta.DisplayName)
	}
	if meta.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestGetMetadata(t *testing.T) {
	p := newTestProvider("openai", Capabilities{Streaming: true})
	meta := GetMetadata(p)
	if meta.Name != "openai" {
		t.Errorf("expected name openai, got %s", meta.Name)
	}
	if !meta.Capabilities.Streaming {
		t.Error("expected streaming capability")
	}
}
