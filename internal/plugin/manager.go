package plugin

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PluginManager orchestrates the lifecycle of all registered plugins.
type PluginManager interface {
	// Register adds a plugin to the manager. Returns an error if the plugin
	// ID or name conflicts with an existing registration.
	Register(ctx context.Context, p Plugin) error

	// Deregister removes a plugin by ID and stops it if running.
	Deregister(ctx context.Context, id string) error

	// Get retrieves a plugin by ID.
	Get(id string) (Plugin, error)

	// List returns all registered plugins.
	List() []Plugin

	// ListByCategory returns plugins filtered by category.
	ListByCategory(category PluginCategory) []Plugin

	// Start initializes and starts all registered plugins in category order:
	// Provider → Policy → Learning → Scheduler → Dashboard → Tool.
	Start(ctx context.Context) error

	// Stop stops all running plugins in reverse category order.
	Stop(ctx context.Context) error

	// Health returns the health status of all plugins.
	Health() map[string]PluginHealth

	// Count returns the number of registered plugins.
	Count() int
}

type pluginManager struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
}

// NewPluginManager creates a new PluginManager.
func NewPluginManager() PluginManager {
	return &pluginManager{
		plugins: make(map[string]Plugin),
	}
}

func (m *pluginManager) Register(ctx context.Context, p Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p == nil {
		return fmt.Errorf("plugin: cannot register nil plugin")
	}

	if _, exists := m.plugins[p.ID()]; exists {
		return fmt.Errorf("plugin: duplicate ID %q", p.ID())
	}

	m.plugins[p.ID()] = p
	return nil
}

func (m *pluginManager) Deregister(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, exists := m.plugins[id]
	if !exists {
		return fmt.Errorf("plugin: plugin %q not found", id)
	}

	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := p.Stop(stopCtx); err != nil {
		return fmt.Errorf("plugin: stop failed for %q: %w", id, err)
	}

	delete(m.plugins, id)
	return nil
}

func (m *pluginManager) Get(id string) (Plugin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, exists := m.plugins[id]
	if !exists {
		return nil, fmt.Errorf("plugin: plugin %q not found", id)
	}
	return p, nil
}

func (m *pluginManager) List() []Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		result = append(result, p)
	}
	return result
}

func (m *pluginManager) ListByCategory(category PluginCategory) []Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Plugin, 0)
	for _, p := range m.plugins {
		if p.Category() == category {
			result = append(result, p)
		}
	}
	return result
}

var categoryOrder = []PluginCategory{
	CategoryProvider,
	CategoryPolicy,
	CategoryLearning,
	CategoryScheduler,
	CategoryDashboard,
	CategoryTool,
}

func categoryIndex(c PluginCategory) int {
	for i, cat := range categoryOrder {
		if cat == c {
			return i
		}
	}
	return len(categoryOrder)
}

func (m *pluginManager) Start(ctx context.Context) error {
	m.mu.RLock()
	sorted := make([]Plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		sorted = append(sorted, p)
	}
	m.mu.RUnlock()

	// Sort by category order.
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if categoryIndex(sorted[i].Category()) > categoryIndex(sorted[j].Category()) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	for _, p := range sorted {
		if err := p.Init(ctx, PluginConfig{
			Name:     p.Name(),
			PluginID: p.ID(),
		}); err != nil {
			return fmt.Errorf("plugin: init failed for %q: %w", p.ID(), err)
		}
		if err := p.Start(ctx); err != nil {
			return fmt.Errorf("plugin: start failed for %q: %w", p.ID(), err)
		}
	}
	return nil
}

func (m *pluginManager) Stop(ctx context.Context) error {
	m.mu.RLock()
	sorted := make([]Plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		sorted = append(sorted, p)
	}
	m.mu.RUnlock()

	// Sort in reverse category order.
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if categoryIndex(sorted[i].Category()) < categoryIndex(sorted[j].Category()) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	for _, p := range sorted {
		stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := p.Stop(stopCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("plugin: stop failed for %q: %w", p.ID(), err)
		}
	}
	return nil
}

func (m *pluginManager) Health() map[string]PluginHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]PluginHealth, len(m.plugins))
	for id, p := range m.plugins {
		result[id] = p.Health()
	}
	return result
}

func (m *pluginManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.plugins)
}
