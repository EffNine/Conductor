package provider

import (
	"context"
	"fmt"
	"strings"
)

// DiscoveryResult holds the result of a provider discovery query.
type DiscoveryResult struct {
	Providers []Provider
	Metadata  []Metadata
	Count     int
	Query     string
}

// DiscoverByCapability finds providers supporting a capability.
func (r *Registry) DiscoverByCapability(ctx context.Context, capability string) DiscoveryResult {
	providers := r.FindByCapability(capability)
	metas := make([]Metadata, 0, len(providers))
	for _, p := range providers {
		meta, _ := r.GetMetadata(p.Name())
		metas = append(metas, meta)
	}
	return DiscoveryResult{
		Providers: providers,
		Metadata:  metas,
		Count:     len(providers),
		Query:     fmt.Sprintf("capability:%s", capability),
	}
}

// DiscoverByModel finds providers supporting a model.
func (r *Registry) DiscoverByModel(ctx context.Context, modelID string) DiscoveryResult {
	providers := r.FindByModel(modelID)
	metas := make([]Metadata, 0, len(providers))
	for _, p := range providers {
		meta, _ := r.GetMetadata(p.Name())
		metas = append(metas, meta)
	}
	return DiscoveryResult{
		Providers: providers,
		Metadata:  metas,
		Count:     len(providers),
		Query:     fmt.Sprintf("model:%s", modelID),
	}
}

// DiscoverByCapabilities finds providers supporting any of the given capabilities.
func (r *Registry) DiscoverByCapabilities(ctx context.Context, capabilities []string) DiscoveryResult {
	seen := make(map[string]bool)
	var providers []Provider
	var metas []Metadata

	for _, cap := range capabilities {
		cap = strings.TrimSpace(strings.ToLower(cap))
		for _, p := range r.FindByCapability(cap) {
			if !seen[p.Name()] {
				seen[p.Name()] = true
				providers = append(providers, p)
				meta, _ := r.GetMetadata(p.Name())
				metas = append(metas, meta)
			}
		}
	}

	query := strings.Join(capabilities, ",")
	return DiscoveryResult{
		Providers: providers,
		Metadata:  metas,
		Count:     len(providers),
		Query:     fmt.Sprintf("capabilities:[%s]", query),
	}
}

// DiscoverAll returns all registered providers with their metadata.
func (r *Registry) DiscoverAll(ctx context.Context) DiscoveryResult {
	providers := r.All()
	metas := r.AllMetadata()
	return DiscoveryResult{
		Providers: providers,
		Metadata:  metas,
		Count:     len(providers),
		Query:     "all",
	}
}

// DiscoverStreaming returns providers that support streaming.
func (r *Registry) DiscoverStreaming(ctx context.Context) DiscoveryResult {
	return r.DiscoverByCapability(ctx, "streaming")
}

// DiscoverVision returns providers that support vision.
func (r *Registry) DiscoverVision(ctx context.Context) DiscoveryResult {
	return r.DiscoverByCapability(ctx, "vision")
}

// DiscoverReasoning returns providers that support reasoning.
func (r *Registry) DiscoverReasoning(ctx context.Context) DiscoveryResult {
	return r.DiscoverByCapability(ctx, "reasoning")
}

// DiscoverTools returns providers that support tool calling.
func (r *Registry) DiscoverTools(ctx context.Context) DiscoveryResult {
	return r.DiscoverByCapability(ctx, "tools")
}

// DiscoverStructured returns providers that support structured output.
func (r *Registry) DiscoverStructured(ctx context.Context) DiscoveryResult {
	return r.DiscoverByCapability(ctx, "structured")
}

// DiscoverLongContext returns providers that support long context.
func (r *Registry) DiscoverLongContext(ctx context.Context) DiscoveryResult {
	return r.DiscoverByCapability(ctx, "long-context")
}

// DiscoverEmbeddings returns providers that support embeddings.
func (r *Registry) DiscoverEmbeddings(ctx context.Context) DiscoveryResult {
	return r.DiscoverByCapability(ctx, "embeddings")
}
