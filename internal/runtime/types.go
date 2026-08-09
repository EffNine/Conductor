// Package runtime defines the ProviderRuntime interface and related types
// that abstract the lifecycle and state of a provider runtime.
//
// This package is a foundation for future runtime intelligence, resource
// management, and adaptive scaling features. No implementation is provided
// here — only interfaces and data types.
package runtime

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/provider"
)

// ProviderState represents the current state of a provider runtime.
type ProviderState string

const (
	// StateUnknown means the runtime state is not yet known.
	StateUnknown ProviderState = "unknown"
	// StateHealthy means the provider is operating normally.
	StateHealthy ProviderState = "healthy"
	// StateDegraded means the provider is operating with reduced capacity.
	StateDegraded ProviderState = "degraded"
	// StateUnhealthy means the provider is not responding.
	StateUnhealthy ProviderState = "unhealthy"
	// StateRecovering means the provider is attempting to recover.
	StateRecovering ProviderState = "recovering"
	// StateScalingUp means the provider is increasing capacity.
	StateScalingUp ProviderState = "scaling_up"
	// StateScalingDown means the provider is decreasing capacity.
	StateScalingDown ProviderState = "scaling_down"
)

// RuntimeSnapshot captures a point-in-time view of all provider runtimes.
type RuntimeSnapshot struct {
	Timestamp    time.Time
	Providers    map[string]ProviderStateSnapshot
	GlobalState  GlobalRuntimeState
}

// ProviderStateSnapshot captures the state of a single provider runtime.
type ProviderStateSnapshot struct {
	State           ProviderState
	LastHealthCheck time.Time
	LatencyMs       int64
	ErrorRate       float64
	Capacity        float64 // 0.0 to 1.0
	Tags            map[string]string
}

// GlobalRuntimeState captures system-wide runtime state.
type GlobalRuntimeState struct {
	TotalProviders   int
	HealthyProviders int
	DegradedProviders int
	UnhealthyProviders int
	AvgLatencyMs     int64
	TotalQPS         float64
}

// StateChange represents a transition in provider runtime state.
type StateChange struct {
	ProviderName    string
	From            ProviderState
	To              ProviderState
	Reason          string
	Timestamp       time.Time
	Metadata        map[string]any
}

// ProviderRuntime is the interface that all provider runtimes must implement.
// It abstracts the lifecycle, state, and capabilities of a provider.
type ProviderRuntime interface {
	// Name returns the provider identifier.
	Name() string

	// State returns the current runtime state.
	State() ProviderState

	// Snapshot returns a point-in-time snapshot of the runtime state.
	Snapshot(ctx context.Context) ProviderStateSnapshot

	// RecordLatency records a latency measurement.
	RecordLatency(latencyMs int64)

	// RecordError records an error event.
	RecordError(err error)

	// RecordSuccess records a successful request.
	RecordSuccess()

	// IsHealthy reports whether the provider is considered healthy.
	IsHealthy() bool

	// UpdateState transitions the provider to a new state.
	UpdateState(newState ProviderState, reason string, metadata map[string]any)

	// GetStateChanges returns recent state changes.
	GetStateChanges() []StateChange

	// GetUptime returns how long the runtime has been active.
	GetUptime() time.Duration

	// GetMetadata returns provider metadata.
	GetMetadata() map[string]any

	// GetTag returns a provider tag.
	GetTag(key string) (string, bool)
}

// RuntimeFactory is used to create provider runtime instances.
type RuntimeFactory interface {
	// Create creates a new runtime for the given provider.
	Create(ctx context.Context, provider provider.Provider) (ProviderRuntime, error)

	// Get retrieves an existing runtime by provider name.
	Get(name string) (ProviderRuntime, bool)

	// List returns all registered runtimes.
	List() []ProviderRuntime
}
