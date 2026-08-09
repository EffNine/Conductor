package provider

// Plugin defines the extensibility contract for AI providers.
// Any type implementing ProviderPlugin can be registered with the Registry
// and will be discovered by the router, catalog, and dashboard.
type Plugin interface {
	// Provider returns the underlying provider implementation.
	Provider() Provider

	// Metadata returns static information about the provider.
	Metadata() Metadata
}

// PluginFactory creates a new instance of a provider plugin.
// The factory receives configuration and returns a plugin ready for registration.
type PluginFactory func(config PluginConfig) (Plugin, error)

// PluginConfig holds the configuration needed to create a provider plugin.
type PluginConfig struct {
	Name    string
	APIKey  string
	BaseURL string
	Timeout interface{}
	Enabled bool
}
