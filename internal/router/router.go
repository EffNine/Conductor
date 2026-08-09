package router

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/provider"
)

// AutoSelector resolves the best model for a given provider at request time.
// The automode package implements this interface.
type AutoSelector interface {
	Select(ctx context.Context, task string) (string, error)
}

// Engine handles model routing, aliases, and fallbacks
type Engine struct {
	mu           sync.RWMutex
	routes       map[string]Route
	aliases      map[string]string
	fallbacks    map[string][]Fallback
	registry     *provider.Registry
	autoSelector AutoSelector
	breakers     *BreakerPool
}

// BreakerPool holds per-provider circuit breakers.
type BreakerPool struct {
	mu       sync.RWMutex
	breakers map[string]*breaker.Breaker
	cfg      breaker.Config
}

// NewBreakerPool creates a pool with the given global config.
func NewBreakerPool(cfg breaker.Config) *BreakerPool {
	return &BreakerPool{
		breakers: make(map[string]*breaker.Breaker),
		cfg:      cfg,
	}
}

// Get returns the breaker for a provider, creating one if absent.
func (p *BreakerPool) Get(providerName string) *breaker.Breaker {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	b, ok := p.breakers[providerName]
	p.mu.RUnlock()
	if ok {
		return b
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if b, ok = p.breakers[providerName]; ok {
		return b
	}
	b = breaker.New(p.cfg)
	p.breakers[providerName] = b
	return b
}

// Stats returns per-provider breaker snapshots.
func (p *BreakerPool) Stats() map[string]breaker.BreakerStats {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]breaker.BreakerStats, len(p.breakers))
	for name, b := range p.breakers {
		out[name] = b.Stats()
	}
	return out
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

	if cfg.Circuit.Enabled {
		engine.breakers = NewBreakerPool(breaker.Config{
			FailureThreshold: cfg.Circuit.FailureThreshold,
			RecoveryTimeout:  cfg.Circuit.RecoveryTimeout,
			SuccessThreshold: cfg.Circuit.SuccessThreshold,
		})
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

// SetAutoSelector wires runtime automatic model selection.
func (e *Engine) SetAutoSelector(s AutoSelector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.autoSelector = s
}

// HasAutoSelector reports whether an auto selector is currently wired.
func (e *Engine) HasAutoSelector() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.autoSelector != nil
}

// Resolve resolves a model ID to a provider and (possibly overridden) model name
func (e *Engine) Resolve(modelID string) (*ResolvedRoute, error) {
	return e.ResolveWithContext(context.Background(), modelID, nil)
}

// ResolveWithMessages resolves a model ID with request messages so auto mode can
// classify the task.
func (e *Engine) ResolveWithMessages(modelID string, messages []apitypes.Message) (*ResolvedRoute, error) {
	return e.ResolveWithContext(context.Background(), modelID, messages)
}

// ResolveWithContext resolves a model ID using request context when auto mode
// needs to perform catalog/cost lookups.
func (e *Engine) ResolveWithContext(ctx context.Context, modelID string, messages []apitypes.Message) (*ResolvedRoute, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	providerHint, baseID := e.splitProviderPrefix(modelID)

	// Check if it's an alias
	if resolvedAlias, ok := e.aliases[baseID]; ok {
		baseID = resolvedAlias
	}

	// Runtime auto mode: if baseID is "auto" and the user did not supply a
	// provider prefix, let the configured auto selector pick the upstream model.
	if baseID == "auto" && providerHint == "" && e.autoSelector != nil {
		providerName := "nvidia_nim" // default scope for this iteration
		p, found := e.registry.Get(providerName)
		if !found {
			return nil, fmt.Errorf("auto mode is unavailable: provider '%s' is not registered", providerName)
		}
		taskText := joinMessages(messages)
		selected, err := e.autoSelector.Select(ctx, taskText)
		if err != nil {
			return nil, fmt.Errorf("auto mode failed: %w", err)
		}
		return &ResolvedRoute{
			Provider:        p,
			ProviderName:    providerName,
			ProviderModelID: selected,
			ModelID:         "auto",
			Breaker:         e.breakers.Get(providerName),
		}, nil
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
			Breaker:         e.breakers.Get(providerName),
		}, nil
	}

	// Provider-prefixed Model ID from /v1/models (e.g. nvidia_nim/meta/llama-3.1-8b-instruct)
	if providerHint != "" {
		if p, found := e.registry.Get(providerHint); found {
			return &ResolvedRoute{
				Provider:        p,
				ProviderName:    providerHint,
				ProviderModelID: baseID,
				ModelID:         baseID,
				Breaker:         e.breakers.Get(providerHint),
			}, nil
		}
	}

	return nil, fmt.Errorf("model '%s' not found; add a route or use a provider-prefixed ID", modelID)
}

// joinMessages concatenates message contents for task classification.
// Multimodal messages with image_url parts contribute an "image" token so
// image-only prompts still classify as the vision task profile.
func joinMessages(messages []apitypes.Message) string {
	if len(messages) == 0 {
		return ""
	}
	parts := make([]string, 0, len(messages)+1)
	hasImage := false
	for _, m := range messages {
		if s := m.ContentString(); s != "" {
			parts = append(parts, s)
		}
		if messageHasImageURL(m) {
			hasImage = true
		}
	}
	if hasImage {
		parts = append(parts, "image")
	}
	return strings.Join(parts, "\n")
}

func messageHasImageURL(m apitypes.Message) bool {
	parts, ok := m.Content.([]apitypes.ContentPart)
	if !ok {
		return false
	}
	for _, p := range parts {
		if p.Type == apitypes.ContentPartImageURL && p.ImageURL != nil && p.ImageURL.URL != "" {
			return true
		}
	}
	return false
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

// BreakerPool exposes the pool for dashboard access.
func (e *Engine) BreakerPool() *BreakerPool {
	return e.breakers
}
func (e *Engine) ResolveWithFallback(modelID string) (*ResolvedRoute, []ResolvedRoute, error) {
	return e.ResolveWithFallbackAndMessages(modelID, nil)
}

// ResolveWithFallbackAndMessages is the task-aware variant used by HTTP handlers.
func (e *Engine) ResolveWithFallbackAndMessages(modelID string, messages []apitypes.Message) (*ResolvedRoute, []ResolvedRoute, error) {
	return e.ResolveWithFallbackAndContext(context.Background(), modelID, messages)
}

// ResolveWithFallbackAndContext is the context-aware variant used by HTTP handlers.
func (e *Engine) ResolveWithFallbackAndContext(ctx context.Context, modelID string, messages []apitypes.Message) (*ResolvedRoute, []ResolvedRoute, error) {
	primary, err := e.ResolveWithContext(ctx, modelID, messages)
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
			Breaker:         e.breakers.Get(fb.Provider),
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
	Breaker         *breaker.Breaker
}
