package router

import (
	"fmt"
	"strings"
	"sync"

	"github.com/novexa/gateway/internal/config"
	"github.com/novexa/gateway/internal/provider"
)

// Engine handles model routing, aliases, and fallbacks
type Engine struct {
	mu        sync.RWMutex
	routes    map[string]Route
	aliases   map[string]string
	fallbacks map[string][]Fallback
	registry  *provider.Registry
}

// Route represents a model-to-provider route
type Route struct {
	Provider string
	ModelID  string // Optional: override model name for provider
}

// Fallback represents a fallback provider
type Fallback struct {
	Provider string
	ModelID  string // Optional: override model name
}

// NewEngine creates a new routing engine
func NewEngine(cfg *config.Config, registry *provider.Registry) *Engine {
	engine := &Engine{
		routes:    make(map[string]Route),
		aliases:   make(map[string]string),
		fallbacks: make(map[string][]Fallback),
		registry:  registry,
	}

	// Load routes from config
	for modelID, routeCfg := range cfg.Routes {
		engine.routes[modelID] = Route{
			Provider: routeCfg.Provider,
			ModelID:  routeCfg.ModelID,
		}
	}

	// Load aliases from config
	for alias, modelID := range cfg.Aliases {
		engine.aliases[alias] = modelID
	}

	// Load fallbacks from config
	for modelID, fallbackCfgs := range cfg.Fallbacks {
		fallbacks := make([]Fallback, 0, len(fallbackCfgs))
		for _, fb := range fallbackCfgs {
			fallbacks = append(fallbacks, Fallback{
				Provider: fb.Provider,
				ModelID:  fb.ModelID,
			})
		}
		engine.fallbacks[modelID] = fallbacks
	}

	return engine
}

// Resolve resolves a model ID to a provider and (possibly overridden) model name
func (e *Engine) Resolve(modelID string) (*ResolvedRoute, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	providerHint, baseID := e.splitProviderPrefix(modelID)

	// Check if it's an alias
	if resolvedAlias, ok := e.aliases[baseID]; ok {
		baseID = resolvedAlias
	}

	// Check if it's a configured route
	if route, ok := e.routes[baseID]; ok {
		providerName := route.Provider
		if providerHint != "" {
			if providerHint != route.Provider {
				return nil, fmt.Errorf("model '%s' is not routed to provider '%s'", baseID, providerHint)
			}
			providerName = providerHint
		}

		p, found := e.registry.Get(providerName)
		if !found {
			return nil, fmt.Errorf("provider '%s' not found for model '%s'", providerName, baseID)
		}

		providerModelID := route.ModelID
		if providerModelID == "" {
			providerModelID = baseID
		}

		return &ResolvedRoute{
			Provider:        p,
			ProviderName:    providerName,
			ProviderModelID: providerModelID,
			ModelID:         baseID,
		}, nil
	}

	return nil, fmt.Errorf("model '%s' not found; add a route or use a provider-prefixed ID", modelID)
}

// splitProviderPrefix returns (provider, baseModelID) when modelID starts with a
// registered provider name followed by "/". Otherwise returns ("", modelID).
func (e *Engine) splitProviderPrefix(modelID string) (string, string) {
	providerName, base, ok := strings.Cut(modelID, "/")
	if !ok || providerName == "" || base == "" {
		return "", modelID
	}
	if _, found := e.registry.Get(providerName); !found {
		return "", modelID
	}
	return providerName, base
}

// ResolveWithFallback resolves a model and returns the route plus fallback chain
func (e *Engine) ResolveWithFallback(modelID string) (*ResolvedRoute, []ResolvedRoute, error) {
	primary, err := e.Resolve(modelID)
	if err != nil {
		return nil, nil, err
	}

	e.mu.RLock()
	fallbackCfgs, hasFallbacks := e.fallbacks[primary.ModelID]
	e.mu.RUnlock()

	if !hasFallbacks {
		return primary, nil, nil
	}

	fallbacks := make([]ResolvedRoute, 0, len(fallbackCfgs))
	for _, fb := range fallbackCfgs {
		p, found := e.registry.Get(fb.Provider)
		if !found {
			continue
		}

		modelName := fb.ModelID
		if modelName == "" {
			modelName = primary.ModelID
		}

		fallbacks = append(fallbacks, ResolvedRoute{
			Provider:        p,
			ProviderName:    fb.Provider,
			ProviderModelID: modelName,
			ModelID:         primary.ModelID,
		})
	}

	return primary, fallbacks, nil
}

// ResolvedRoute represents a resolved route with provider and model
type ResolvedRoute struct {
	Provider        provider.Provider
	ProviderName    string
	ProviderModelID string // The upstream model slug to send to the provider
	ModelID         string // The user-facing model ID from the route key
}
