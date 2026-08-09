package plugin

import (
	"fmt"
	"sync"
)

// PluginRegistry provides fast lookup of plugins by ID, name, and category.
// It is a read-optimized view of the PluginManager's registrations.
type PluginRegistry interface {
	// GetById returns a plugin by its unique ID.
	ById(id string) (Plugin, bool)

	// GetByName returns a plugin by its human-readable name.
	// If multiple plugins share the same name, the first match is returned.
	ByName(name string) (Plugin, bool)

	// GetByIdentifier returns a plugin by ID or name.
	ByIdentifier(identifier string) (Plugin, bool)

	// GetAll returns all registered plugins.
	GetAll() []Plugin

	// GetByCategory returns all plugins in the given category.
	ByCategory(category PluginCategory) []Plugin

	// Contains checks if a plugin with the given ID is registered.
	Contains(id string) bool

	// Count returns the total number of registered plugins.
	Count() int
}

type registry struct {
	mu     sync.RWMutex
	byId   map[string]Plugin
	byName map[string]Plugin
	byCat  map[PluginCategory][]Plugin
}

// NewPluginRegistry creates a new PluginRegistry.
func NewPluginRegistry() PluginRegistry {
	return &registry{
		byId:   make(map[string]Plugin),
		byName: make(map[string]Plugin),
		byCat:  make(map[PluginCategory][]Plugin),
	}
}

func (r *registry) add(p Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.byId[p.ID()] = p
	if _, exists := r.byName[p.Name()]; !exists {
		r.byName[p.Name()] = p
	}
	r.byCat[p.Category()] = append(r.byCat[p.Category()], p)
}

func (r *registry) ById(id string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.byId[id]
	return p, ok
}

func (r *registry) ByName(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.byName[name]
	return p, ok
}

func (r *registry) ByIdentifier(identifier string) (Plugin, bool) {
	if p, ok := r.ById(identifier); ok {
		return p, true
	}
	return r.ByName(identifier)
}

func (r *registry) GetAll() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Plugin, 0, len(r.byId))
	for _, p := range r.byId {
		result = append(result, p)
	}
	return result
}

func (r *registry) ByCategory(category PluginCategory) []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Plugin, len(r.byCat[category]))
	copy(result, r.byCat[category])
	return result
}

func (r *registry) Contains(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.byId[id]
	return ok
}

func (r *registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byId)
}

// SyncFromManager updates the registry to match the given manager's state.
func (r *registry) SyncFromManager(mgr PluginManager) error {
	current := make(map[string]Plugin)
	for _, p := range mgr.List() {
		current[p.ID()] = p
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.byId = make(map[string]Plugin, len(current))
	r.byName = make(map[string]Plugin)
	r.byCat = make(map[PluginCategory][]Plugin)

	for id, p := range current {
		r.byId[id] = p
		if _, exists := r.byName[p.Name()]; !exists {
			r.byName[p.Name()] = p
		}
		r.byCat[p.Category()] = append(r.byCat[p.Category()], p)
	}

	// Validate no duplicate names.
	nameCount := make(map[string]int)
	for _, p := range r.byId {
		nameCount[p.Name()]++
	}
	for name, count := range nameCount {
		if count > 1 {
			return fmt.Errorf("plugin: duplicate plugin name %q", name)
		}
	}

	return nil
}
