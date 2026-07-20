package catalog

import (
	"context"
	"sort"

	"github.com/novexa/gateway/internal/config"
	"github.com/novexa/gateway/internal/provider"
)

// Entry is one advertised model in the merged catalog.
type Entry struct {
	ModelID         string `json:"model_id"`
	Provider        string `json:"provider"`
	ProviderModelID string `json:"provider_model_id"`
	OwnedBy         string `json:"owned_by,omitempty"`
}

// ReachabilityFilter decides whether a catalog entry should be advertised.
type ReachabilityFilter interface {
	ShouldAdvertise(modelID string) bool
}

// FilterReadiness is implemented by ModelStatusStore. After FilterReady() becomes
// true, unprobed models follow unknown_as_reachable (default false = available-only).
type FilterReadiness interface {
	FilterReady() bool
}

// StaticModels maps provider name → configured static Model IDs.
type StaticModels map[string][]string

// Catalog merges model lists from registered providers.
type Catalog struct {
	registry    *provider.Registry
	static      StaticModels
	filter      ReachabilityFilter
	hide        bool
	curatedOnly bool
}

// New creates a Catalog. static may be nil.
func New(registry *provider.Registry, static StaticModels) *Catalog {
	if static == nil {
		static = StaticModels{}
	}
	return &Catalog{registry: registry, static: static}
}

// SetCuratedOnly toggles curated-only mode. When true, List/ListAll advertise
// only Model IDs from the configured Static Model List (providers.*.models).
func (c *Catalog) SetCuratedOnly(enabled bool) {
	c.curatedOnly = enabled
}

// SetReachabilityFilter configures optional auto-hide of unreachable models.
// When hide is false, List returns the full catalog regardless of filter.
func (c *Catalog) SetReachabilityFilter(filter ReachabilityFilter, hide bool) {
	c.filter = filter
	c.hide = hide
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
// Every Model ID is provider-prefixed (e.g. nvidia_nim/deepseek-ai/deepseek-v4-flash)
// so clients can send the listed ID directly to /v1/chat/completions.
// When curated_only is enabled, only Static Model List entries are advertised.
// When a reachability filter is configured with hide enabled, unreachable models
// are omitted.
func (c *Catalog) List(ctx context.Context) ([]Entry, error) {
	entries, err := c.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	if c.filter == nil || !c.hide {
		return entries, nil
	}
	// Always apply ShouldAdvertise: confirmed failures drop out during the pass;
	// unprobed stay until FilterReady (see ModelStatusStore.ShouldAdvertise).
	filtered := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if c.filter.ShouldAdvertise(e.ModelID) {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// ListAll returns the full merged catalog without reachability filtering.
// Used by the model prober so it can still check models that are currently hidden.
// When curated_only is enabled, this still returns only the curated Static Model List
// so probes target the operator's allowlist rather than the full upstream catalog.
func (c *Catalog) ListAll(ctx context.Context) ([]Entry, error) {
	if c.curatedOnly {
		return c.listCurated(), nil
	}

	var entries []Entry

	for _, p := range c.registry.All() {
		models, err := p.ListModels(ctx)
		if err != nil {
			entries = append(entries, c.staticEntries(p.Name())...)
			continue
		}
		for _, m := range models {
			baseID := m.ModelID
			if baseID == "" {
				baseID = m.ProviderModelID
			}
			entries = append(entries, Entry{
				ModelID:         p.Name() + "/" + baseID,
				Provider:        p.Name(),
				ProviderModelID: m.ProviderModelID,
				OwnedBy:         m.OwnedBy,
			})
		}
	}

	sortEntries(entries)
	return entries, nil
}

// listCurated builds the catalog exclusively from providers.*.models.
// Providers with an empty models list contribute nothing.
func (c *Catalog) listCurated() []Entry {
	var entries []Entry
	for _, p := range c.registry.All() {
		entries = append(entries, c.staticEntries(p.Name())...)
	}
	sortEntries(entries)
	return entries
}

func (c *Catalog) staticEntries(providerName string) []Entry {
	ids := c.static[providerName]
	if len(ids) == 0 {
		return nil
	}
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, Entry{
			ModelID:         providerName + "/" + id,
			Provider:        providerName,
			ProviderModelID: id,
		})
	}
	return entries
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ModelID != entries[j].ModelID {
			return entries[i].ModelID < entries[j].ModelID
		}
		return entries[i].Provider < entries[j].Provider
	})
}
