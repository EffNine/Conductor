// Package runtime provides the Provider Runtime subsystem.
//
// RuntimeManager is the public API for runtime lifecycle management,
// snapshot creation, and queries.
package runtime

import (
	"context"
	"sync"
	"time"
)

// Manager is the public interface for runtime lifecycle management.
type Manager interface {
	// Register adds a new provider runtime.
	Register(runtime ProviderRuntime) error

	// Deregister removes a runtime.
	Deregister(name string) error

	// Get retrieves a runtime by name.
	Get(name string) (ProviderRuntime, error)

	// GetAll returns all registered runtimes.
	GetAll() ([]ProviderRuntime, error)

	// Snapshot returns a snapshot of all runtimes.
	Snapshot(ctx context.Context) RuntimeSnapshot

	// Watch subscribes to provider state changes.
	Watch(providerName string, callback func(ProviderStateSnapshot)) uint64

	// Unwatch unsubscribes from provider state changes.
	Unwatch(id uint64)

	// Update updates a provider's state.
	Update(name string, updater func(ProviderRuntime) error) error

	// Count returns the number of registered providers.
	Count() int
}

// ManagerImpl is the concrete implementation of Manager.
type ManagerImpl struct {
	store *RuntimeStore
	mu    sync.RWMutex
}

// NewManager creates a new runtime manager.
func NewManager(store *RuntimeStore) *ManagerImpl {
	return &ManagerImpl{store: store}
}

// Register adds a new provider runtime.
func (m *ManagerImpl) Register(runtime ProviderRuntime) error {
	return m.store.Register(runtime)
}

// Deregister removes a runtime.
func (m *ManagerImpl) Deregister(name string) error {
	return m.store.Deregister(name)
}

// Get retrieves a runtime by name.
func (m *ManagerImpl) Get(name string) (ProviderRuntime, error) {
	return m.store.Get(name)
}

// GetAll returns all registered runtimes.
func (m *ManagerImpl) GetAll() ([]ProviderRuntime, error) {
	return m.store.GetAll()
}

// Snapshot returns a snapshot of all runtimes.
func (m *ManagerImpl) Snapshot(ctx context.Context) RuntimeSnapshot {
	return m.store.Snapshot(ctx)
}

// Watch subscribes to provider state changes.
func (m *ManagerImpl) Watch(providerName string, callback func(ProviderStateSnapshot)) uint64 {
	return m.store.Watch(providerName, callback)
}

// Unwatch unsubscribes from provider state changes.
func (m *ManagerImpl) Unwatch(id uint64) {
	m.store.Unwatch(id)
}

// Update updates a provider's state.
func (m *ManagerImpl) Update(name string, updater func(ProviderRuntime) error) error {
	return m.store.Update(name, updater)
}

// Count returns the number of registered providers.
func (m *ManagerImpl) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.store.runtimes)
}

// GetHealthyCount returns the number of healthy providers.
func (m *ManagerImpl) GetHealthyCount() int {
	snap := m.Snapshot(context.Background())
	return snap.GlobalState.HealthyProviders
}

// GetUnhealthyCount returns the number of unhealthy providers.
func (m *ManagerImpl) GetUnhealthyCount() int {
	snap := m.Snapshot(context.Background())
	return snap.GlobalState.UnhealthyProviders
}

// GetHealthyProviders returns the names of healthy providers.
func (m *ManagerImpl) GetHealthyProviders() []string {
	snap := m.Snapshot(context.Background())
	var healthy []string
	for name, state := range snap.Providers {
		if state.State == StateHealthy {
			healthy = append(healthy, name)
		}
	}
	return healthy
}

// GetUnhealthyProviders returns the names of unhealthy providers.
func (m *ManagerImpl) GetUnhealthyProviders() []string {
	snap := m.Snapshot(context.Background())
	var unhealthy []string
	for name, state := range snap.Providers {
		if state.State == StateUnhealthy || state.State == StateRecovering {
			unhealthy = append(unhealthy, name)
		}
	}
	return unhealthy
}

// GetProviderUptime returns the uptime of a provider.
func (m *ManagerImpl) GetProviderUptime(name string) (time.Duration, error) {
	r, err := m.Get(name)
	if err != nil {
		return 0, err
	}
	return r.GetUptime(), nil
}

// AggregateStats returns aggregated statistics across all providers.
func (m *ManagerImpl) AggregateStats() AggregateStats {
	snap := m.Snapshot(context.Background())

	var totalLatency int64
	var totalSuccess, totalFailure int64
	var totalRequests int64

	for name, runtime := range m.store.runtimes {
		snap := runtime.Snapshot(context.Background())
		stats := runtime.GetStats()
		totalLatency += snap.LatencyMs
		totalSuccess += stats.SuccessCount
		totalFailure += stats.FailureCount
		totalRequests += stats.TotalRequests
		_ = name
	}

	return AggregateStats{
		TotalProviders:   snap.GlobalState.TotalProviders,
		HealthyProviders: snap.GlobalState.HealthyProviders,
		AvgLatencyMs:     snap.GlobalState.AvgLatencyMs,
		TotalRequests:    totalRequests,
		TotalSuccess:     totalSuccess,
		TotalFailure:     totalFailure,
	}
}

// AggregateStats holds aggregated statistics across all providers.
type AggregateStats struct {
	TotalProviders   int
	HealthyProviders int
	AvgLatencyMs     int64
	TotalRequests    int64
	TotalSuccess     int64
	TotalFailure     int64
}

// Ensure ManagerImpl implements Manager.
var _ Manager = (*ManagerImpl)(nil)
