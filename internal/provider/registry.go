package provider

import (
	"strings"
	"sync"
	"time"
)

// Registry holds all registered providers with plugin metadata.
type Registry struct {
	mu            sync.RWMutex
	providers     map[string]Provider
	metadata      map[string]Metadata
	registerTimes map[string]time.Time
	modelCaps     map[string]modelCapabilities // keyed by "provider/modelID"
}

// modelCapabilities holds model-level overrides for routing.
type modelCapabilities struct {
	Caps             Capabilities
	MaxContextLength int
}

// NewRegistry creates a new provider registry
func NewRegistry() *Registry {
	return &Registry{
		providers:     make(map[string]Provider),
		metadata:      make(map[string]Metadata),
		registerTimes: make(map[string]time.Time),
		modelCaps:     make(map[string]modelCapabilities),
	}
}

// Register adds a provider to the registry
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
	meta := GetMetadata(p)
	meta.RegistrationTime = time.Now().UTC()
	r.metadata[p.Name()] = meta
	r.registerTimes[p.Name()] = meta.RegistrationTime
}

// RegisterWithMetadata adds a provider with explicit metadata.
func (r *Registry) RegisterWithMetadata(p Provider, meta Metadata) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
	meta.RegistrationTime = time.Now().UTC()
	r.metadata[p.Name()] = meta
	r.registerTimes[p.Name()] = meta.RegistrationTime
}

// Unregister removes a provider from the registry
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[name]; !ok {
		return false
	}
	delete(r.providers, name)
	delete(r.metadata, name)
	delete(r.registerTimes, name)
	// Also remove all model-level capabilities for this provider.
	for key := range r.modelCaps {
		if strings.HasPrefix(key, name+"/") {
			delete(r.modelCaps, key)
		}
	}
	return true
}

// Get returns a provider by name
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// GetMetadata returns metadata for a provider by name
func (r *Registry) GetMetadata(name string) (Metadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	meta, ok := r.metadata[name]
	return meta, ok
}

// All returns all registered providers
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		providers = append(providers, p)
	}
	return providers
}

// AllMetadata returns all provider metadata
func (r *Registry) AllMetadata() []Metadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	metas := make([]Metadata, 0, len(r.metadata))
	for _, m := range r.metadata {
		metas = append(metas, m)
	}
	return metas
}

// Clear removes all registered providers.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = make(map[string]Provider)
	r.metadata = make(map[string]Metadata)
	r.registerTimes = make(map[string]time.Time)
	r.modelCaps = make(map[string]modelCapabilities)
}

// Names returns the names of all registered providers
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// Count returns the number of registered providers
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.providers)
}

// FindByCapability returns providers that support the given capability
func (r *Registry) FindByCapability(capability string) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []Provider
	for name, meta := range r.metadata {
		if meta.HasCapability(capability) {
			if p, ok := r.providers[name]; ok {
				result = append(result, p)
			}
		}
	}
	return result
}

// FindByModel returns providers that support the given model ID
func (r *Registry) FindByModel(modelID string) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []Provider
	for _, p := range r.providers {
		if p.SupportsModel(modelID) {
			result = append(result, p)
		}
	}
	return result
}

// FindByCapabilityAndModel returns providers that support both the capability and model
func (r *Registry) FindByCapabilityAndModel(capability string, modelID string) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []Provider
	for name, meta := range r.metadata {
		if meta.HasCapability(capability) {
			if p, ok := r.providers[name]; ok && p.SupportsModel(modelID) {
				result = append(result, p)
			}
		}
	}
	return result
}

// GetRegistrationTime returns when a provider was registered
func (r *Registry) GetRegistrationTime(name string) (time.Time, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.registerTimes[name]
	return t, ok
}

// GetProviderInfo returns combined provider info (provider + metadata)
func (r *Registry) GetProviderInfo(name string) (Provider, Metadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, okP := r.providers[name]
	meta, okMeta := r.metadata[name]
	if !okP || !okMeta {
		return nil, Metadata{}, false
	}
	return p, meta, true
}

// ForEach iterates over all registered providers
func (r *Registry) ForEach(fn func(name string, p Provider, meta Metadata)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, p := range r.providers {
		meta := r.metadata[name]
		fn(name, p, meta)
	}
}

// SetModelCapabilities registers explicit model-level capability overrides.
// Keyed by "provider/modelID". These take priority over provider defaults.
func (r *Registry) SetModelCapabilities(providerName, modelID string, caps Capabilities, maxContext int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modelCaps[providerName+"/"+modelID] = modelCapabilities{
		Caps:             caps,
		MaxContextLength: maxContext,
	}
}

// GetModelCapabilities returns model-specific capabilities if explicitly registered.
// Returns zero modelCapabilities and false when no model-level override exists.
func (r *Registry) GetModelCapabilities(providerName, modelID string) (modelCapabilities, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	caps, ok := r.modelCaps[providerName+"/"+modelID]
	return caps, ok
}
