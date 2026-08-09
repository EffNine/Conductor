package plugin

import (
	"context"
	"time"
)

// Lifecycle represents the four-phase lifecycle of every plugin.
type Lifecycle interface {
	// Init performs one-time setup (config validation, connection pooling, etc.).
	// Must be called before Start.
	Init(ctx context.Context, cfg PluginConfig) error

	// Start begins the plugin's active operation (background goroutines, subscriptions, etc.).
	// Must be called after Init and before any functional use.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the plugin. All in-flight operations must complete
	// or be cancelled before Stop returns.
	Stop(ctx context.Context) error

	// Health returns the current health status of the plugin.
	// A plugin is considered healthy if Health().OK is true.
	Health() PluginHealth

	// Version returns the semantic version string of the plugin.
	Version() string
}

// Plugin is the base interface that all Conductor plugins must implement.
// It combines the Lifecycle interface with plugin identification.
type Plugin interface {
	Lifecycle

	// ID returns the unique identifier for this plugin instance.
	ID() string

	// Name returns the human-readable plugin name.
	Name() string

	// Category returns the plugin category (Provider, Policy, Learning, etc.).
	Category() PluginCategory

	// Metadata returns static information about the plugin.
	Metadata() PluginMetadata
}

// PluginConfig holds the configuration needed to initialize a plugin.
type PluginConfig struct {
	// Name is the plugin name (from config or auto-generated).
	Name string

	// PluginID is the unique instance ID.
	PluginID string

	// RawConfig is the provider-raw configuration map from the config file.
	RawConfig map[string]any

	// Enabled indicates whether the plugin is enabled.
	Enabled bool
}

// PluginHealth describes the health status of a plugin.
type PluginHealth struct {
	OK           bool      `json:"ok"`
	Status       string    `json:"status"`
	LastCheck    time.Time `json:"last_check"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

// PluginCategory identifies the category a plugin belongs to.
type PluginCategory string

const (
	CategoryProvider  PluginCategory = "provider"
	CategoryPolicy    PluginCategory = "policy"
	CategoryLearning  PluginCategory = "learning"
	CategoryScheduler PluginCategory = "scheduler"
	CategoryDashboard PluginCategory = "dashboard"
	CategoryTool      PluginCategory = "tool"
)

// String returns the string representation of a PluginCategory.
func (c PluginCategory) String() string {
	return string(c)
}

// IsValid checks if the PluginCategory is a recognized category.
func (c PluginCategory) IsValid() bool {
	switch c {
	case CategoryProvider, CategoryPolicy, CategoryLearning,
		CategoryScheduler, CategoryDashboard, CategoryTool:
		return true
	default:
		return false
	}
}
