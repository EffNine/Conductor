package catalog

import (
	"context"
	"sort"

	"github.com/novexa/gateway/internal/config"
	"github.com/novexa/gateway/internal/provider"
)

// Entry is one advertised model in the merged catalog.
type Entry struct {
	ModelID         string // User-facing Model ID (may be provider-prefixed)
	Provider        string
	ProviderModelID string
	OwnedBy         string
}

// StaticModels maps provider name → configured static Model IDs.
type StaticModels map[string][]string

// Catalog merges model lists from registered providers.
type Catalog struct {
	registry *provider.Registry
	static   StaticModels
}

// New creates a Catalog. static may be nil.
func New(registry *provider.Registry, static StaticModels) *Catalog {
	if static == nil {
		static = StaticModels{}
	}
	return &Catalog{registry: registry, static: static}
}

// StaticFromConfig builds StaticModels from provider config.
func StaticFromConfig(cfg *config.Config) StaticModels {
	if cfg == nil {
		return StaticModels{}
	}
	out := StaticModels{}
	add := func(name string, p config.ProviderConfig) {
		if len(p.Models) > 0 {
			out[name] = append([]string(nil), p.Models...)
		}
	}
	add("openai", cfg.Providers.OpenAI)
	add("anthropic", cfg.Providers.Anthropic)
	add("gemini", cfg.Providers.Gemini)
	add("deepseek", cfg.Providers.DeepSeek)
	add("openrouter", cfg.Providers.OpenRouter)
	add("groq", cfg.Providers.Groq)
	add("ollama", cfg.Providers.Ollama)
	add("lmstudio", cfg.Providers.LMStudio)
	add("opencode", cfg.Providers.Opencode)
	add("nvidia_nim", cfg.Providers.NvidiaNim)
	add("nous_portal", cfg.Providers.NousPortal)
	return out
}

// List returns the merged Model Catalog from all providers.
func (c *Catalog) List(ctx context.Context) ([]Entry, error) {
	var entries []Entry

	for _, p := range c.registry.All() {
		models, err := p.ListModels(ctx)
		if err != nil {
			for _, id := range c.static[p.Name()] {
				entries = append(entries, Entry{
					ModelID:         id,
					Provider:        p.Name(),
					ProviderModelID: id,
				})
			}
			continue
		}
		for _, m := range models {
			baseID := m.ModelID
			if baseID == "" {
				baseID = m.ProviderModelID
			}
			entries = append(entries, Entry{
				ModelID:         baseID,
				Provider:        p.Name(),
				ProviderModelID: m.ProviderModelID,
				OwnedBy:         m.OwnedBy,
			})
		}
	}

	qualifyDuplicates(entries)

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ModelID != entries[j].ModelID {
			return entries[i].ModelID < entries[j].ModelID
		}
		return entries[i].Provider < entries[j].Provider
	})

	return entries, nil
}

// qualifyDuplicates prefixes Model IDs that appear under more than one provider.
func qualifyDuplicates(entries []Entry) {
	counts := make(map[string]int, len(entries))
	for _, e := range entries {
		counts[e.ModelID]++
	}
	for i := range entries {
		if counts[entries[i].ModelID] > 1 {
			entries[i].ModelID = entries[i].Provider + "/" + entries[i].ModelID
		}
	}
}
