package catalog_test

import (
	"context"
	"strings"
	"testing"

	"github.com/novexa/gateway/internal/apitypes"
	"github.com/novexa/gateway/internal/catalog"
	"github.com/novexa/gateway/internal/provider"
)

func TestCatalogListsModelsFromAllProviders(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "openai",
		models: []provider.ModelInfo{
			{ProviderModelID: "gpt-4o", ModelID: "gpt-4o", OwnedBy: "openai"},
		},
	})
	reg.Register(&stubProvider{
		name: "groq",
		models: []provider.ModelInfo{
			{ProviderModelID: "llama3-8b", ModelID: "llama3-8b", OwnedBy: "meta"},
		},
	})

	c := catalog.New(reg, nil)
	entries, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	ids := modelIDs(entries)
	assertContains(t, ids, "openai/gpt-4o")
	assertContains(t, ids, "groq/llama3-8b")
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(entries), ids)
	}
	for _, e := range entries {
		if e.ModelID == "openai/gpt-4o" && e.DisplayName() != "gpt-4o" {
			t.Fatalf("DisplayName = %q, want gpt-4o", e.DisplayName())
		}
		if e.ModelID == "groq/llama3-8b" && e.DisplayName() != "llama3-8b" {
			t.Fatalf("DisplayName = %q, want llama3-8b", e.DisplayName())
		}
	}
}

func TestCatalogAlwaysPrefixesProvider(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "nvidia_nim",
		models: []provider.ModelInfo{
			{ProviderModelID: "deepseek-ai/deepseek-v4-flash", ModelID: "deepseek-ai/deepseek-v4-flash", OwnedBy: "deepseek-ai"},
		},
	})

	c := catalog.New(reg, nil)
	entries, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	ids := modelIDs(entries)
	assertContains(t, ids, "nvidia_nim/deepseek-ai/deepseek-v4-flash")
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1: %v", len(entries), ids)
	}
}

func TestCatalogPrefixesDuplicateBaseModelIDs(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "openai",
		models: []provider.ModelInfo{
			{ProviderModelID: "llama3-8b", ModelID: "llama3-8b"},
		},
	})
	reg.Register(&stubProvider{
		name: "groq",
		models: []provider.ModelInfo{
			{ProviderModelID: "llama3-8b", ModelID: "llama3-8b"},
		},
	})

	c := catalog.New(reg, nil)
	entries, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	ids := modelIDs(entries)
	assertContains(t, ids, "openai/llama3-8b")
	assertContains(t, ids, "groq/llama3-8b")
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(entries), ids)
	}
}

func TestCatalogUsesStaticModelsWhenListFails(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "ollama",
		err:  provider.ErrNotImplemented,
	})

	c := catalog.New(reg, catalog.StaticModels{
		"ollama": {"llama3", "mistral"},
	})
	entries, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	ids := modelIDs(entries)
	assertContains(t, ids, "ollama/llama3")
	assertContains(t, ids, "ollama/mistral")
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(entries), ids)
	}
	for _, e := range entries {
		if e.Provider != "ollama" {
			t.Fatalf("provider = %q, want ollama", e.Provider)
		}
		wantBare := strings.TrimPrefix(e.ModelID, "ollama/")
		if e.ProviderModelID != wantBare {
			t.Fatalf("ProviderModelID %q != bare %q", e.ProviderModelID, wantBare)
		}
	}
}

func TestCatalogCuratedOnlyUsesStaticAllowlist(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "nvidia_nim",
		models: []provider.ModelInfo{
			{ProviderModelID: "noise/a", ModelID: "noise/a"},
			{ProviderModelID: "noise/b", ModelID: "noise/b"},
			{ProviderModelID: "deepseek-ai/deepseek-v4-flash", ModelID: "deepseek-ai/deepseek-v4-flash"},
		},
	})
	reg.Register(&stubProvider{
		name: "openai",
		models: []provider.ModelInfo{
			{ProviderModelID: "gpt-4o", ModelID: "gpt-4o"},
			{ProviderModelID: "gpt-4o-mini", ModelID: "gpt-4o-mini"},
		},
	})

	c := catalog.New(reg, catalog.StaticModels{
		"nvidia_nim": {"deepseek-ai/deepseek-v4-flash", "meta/llama-3.1-8b-instruct"},
		// openai has no curated list → contributes nothing in curated_only mode
	})
	c.SetCuratedOnly(true)

	entries, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := modelIDs(entries)
	assertContains(t, ids, "nvidia_nim/deepseek-ai/deepseek-v4-flash")
	assertContains(t, ids, "nvidia_nim/meta/llama-3.1-8b-instruct")
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(entries), ids)
	}
	for _, id := range ids {
		if id == "openai/gpt-4o" || id == "nvidia_nim/noise/a" {
			t.Fatalf("non-curated model leaked into catalog: %v", ids)
		}
	}

	all, err := c.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll should also be curated-only, got %d: %v", len(all), modelIDs(all))
	}
}

func TestCatalogCuratedOnlyDisabledKeepsDynamicList(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "nvidia_nim",
		models: []provider.ModelInfo{
			{ProviderModelID: "noise/a", ModelID: "noise/a"},
			{ProviderModelID: "keep/me", ModelID: "keep/me"},
		},
	})

	c := catalog.New(reg, catalog.StaticModels{
		"nvidia_nim": {"keep/me"},
	})
	// curated_only defaults to false

	entries, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := modelIDs(entries)
	assertContains(t, ids, "nvidia_nim/noise/a")
	assertContains(t, ids, "nvidia_nim/keep/me")
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(entries), ids)
	}
}

func TestCatalogHidesUnreachableWhenFilterEnabled(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "nvidia_nim",
		models: []provider.ModelInfo{
			{ProviderModelID: "good", ModelID: "good"},
			{ProviderModelID: "bad", ModelID: "bad"},
		},
	})

	store := &stubFilter{hide: map[string]bool{"nvidia_nim/bad": true}, ready: true}
	c := catalog.New(reg, nil)
	c.SetReachabilityFilter(store, true)

	entries, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := modelIDs(entries)
	assertContains(t, ids, "nvidia_nim/good")
	for _, id := range ids {
		if id == "nvidia_nim/bad" {
			t.Fatalf("bad model should be hidden: %v", ids)
		}
	}

	all, err := c.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll = %d, want 2", len(all))
	}
}

func TestCatalogSkipsHideUntilFilterReady(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "nvidia_nim",
		models: []provider.ModelInfo{
			{ProviderModelID: "good", ModelID: "good"},
			{ProviderModelID: "bad", ModelID: "bad"},
		},
	})

	store := &stubFilter{hide: map[string]bool{"nvidia_nim/bad": true}, ready: false}
	c := catalog.New(reg, nil)
	c.SetReachabilityFilter(store, true)

	entries, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("before filter ready, want full catalog, got %v", modelIDs(entries))
	}
}

func TestCatalogAvoidsEmptyFlashWhenAllFiltered(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		name: "nvidia_nim",
		models: []provider.ModelInfo{
			{ProviderModelID: "a", ModelID: "a"},
			{ProviderModelID: "b", ModelID: "b"},
		},
	})

	store := &stubFilter{hide: map[string]bool{"nvidia_nim/a": true, "nvidia_nim/b": true}, ready: true}
	c := catalog.New(reg, nil)
	c.SetReachabilityFilter(store, true)

	entries, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// After filter ready, available-only mode may legitimately return empty.
	if len(entries) != 0 {
		t.Fatalf("after filter ready with all hidden, want empty available list, got %d", len(entries))
	}
}

type stubFilter struct {
	hide  map[string]bool
	ready bool
}

func (s *stubFilter) ShouldAdvertise(modelID string) bool {
	return !s.hide[modelID]
}

func (s *stubFilter) FilterReady() bool { return s.ready }

func modelIDs(entries []catalog.Entry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ModelID
	}
	return ids
}

func assertContains(t *testing.T, got []string, want string) {
	t.Helper()
	for _, id := range got {
		if id == want {
			return
		}
	}
	t.Fatalf("missing %q in %v", want, got)
}

// stubProvider is a test double at the provider boundary.
type stubProvider struct {
	name   string
	models []provider.ModelInfo
	err    error
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
	if s.err != nil {
		return nil, s.err
	}
	return s.models, nil
}

func (s *stubProvider) GetPricing(context.Context) (map[string]provider.PricingInfo, error) {
	return nil, provider.ErrNotImplemented
}

func (s *stubProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: s.name, IsHealthy: true}, nil
}

func (s *stubProvider) SupportsModel(string) bool { return false }
