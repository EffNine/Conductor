package plugin

import (
	"context"
	"fmt"
)

// PluginLoader is the interface for loading plugins at runtime.
// Implementations may load from files, databases, remote URLs, or embedded resources.
type PluginLoader interface {
	// Load attempts to load a plugin with the given configuration.
	// Returns a fully initialized plugin ready for registration.
	Load(ctx context.Context, config PluginConfig) (Plugin, error)

	// LoadBatch attempts to load multiple plugins from a list of configurations.
	// Returns a map of plugin ID to loaded plugin.
	LoadBatch(ctx context.Context, configs []PluginConfig) (map[string]Plugin, error)

	// SupportedFormats returns the file or protocol formats this loader supports.
	SupportedFormats() []string
}

// PluginLoaderFunc is an adapter that turns a function into a PluginLoader.
type PluginLoaderFunc func(ctx context.Context, config PluginConfig) (Plugin, error)

// Load implements PluginLoader for PluginLoaderFunc.
func (f PluginLoaderFunc) Load(ctx context.Context, config PluginConfig) (Plugin, error) {
	return f(ctx, config)
}

// LoadBatch implements PluginLoader for PluginLoaderFunc.
func (f PluginLoaderFunc) LoadBatch(ctx context.Context, configs []PluginConfig) (map[string]Plugin, error) {
	result := make(map[string]Plugin, len(configs))
	for _, cfg := range configs {
		p, err := f.Load(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("plugin: load batch failed for %q: %w", cfg.PluginID, err)
		}
		result[p.ID()] = p
	}
	return result, nil
}

// SupportedFormats implements PluginLoader for PluginLoaderFunc.
func (f PluginLoaderFunc) SupportedFormats() []string {
	return []string{"function"}
}

// PluginFactory is a constructor function that creates a plugin from configuration.
type PluginFactory func(ctx context.Context, config PluginConfig) (Plugin, error)

// NewFactoryLoader creates a PluginLoader from a PluginFactory function.
func NewFactoryLoader(factory PluginFactory) PluginLoader {
	return PluginLoaderFunc(factory)
}
