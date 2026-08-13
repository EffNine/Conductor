package agent

import (
	"errors"
	"fmt"
	"sync"
)

// RoutingHints contains soft provider preferences that influence the
// orchestration candidate scorer without bypassing RouterEngine.
type RoutingHints struct {
	PreferredProviders    []string
	ExcludedProviders     []string
	PreferredCapabilities []string
}

// AgentDefinition describes a named agent role.
type AgentDefinition struct {
	Name             string       `json:"name"`
	Description      string       `json:"description,omitempty"`
	SystemPromptHint string       `json:"system_prompt_hint,omitempty"`
	MaxSteps         int          `json:"max_steps,omitempty"`
	PreferredTools   []string     `json:"preferred_tools,omitempty"`
	ExcludeTools     []string     `json:"exclude_tools,omitempty"`
	RoutingHints     RoutingHints `json:"routing_hints,omitempty"`
	Tags             []string     `json:"tags,omitempty"`
}

// Registry is a thread-safe lookup table for agent role definitions.
type Registry struct {
	mu    sync.RWMutex
	defs  map[string]AgentDefinition
	order []string
}

// ErrDuplicateDefinition is returned when Register is called with a name
// that already exists.
var ErrDuplicateDefinition = errors.New("duplicate agent definition")

// NewRegistry creates an empty agent definition registry.
func NewRegistry() *Registry {
	return &Registry{
		defs:  make(map[string]AgentDefinition),
		order: make([]string, 0),
	}
}

// Register adds a definition. Returns ErrDuplicateDefinition on conflict.
func (r *Registry) Register(def AgentDefinition) error {
	if def.Name == "" {
		return errors.New("agent definition name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.defs[def.Name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateDefinition, def.Name)
	}
	r.defs[def.Name] = def
	r.order = append(r.order, def.Name)
	return nil
}

// Get returns the definition for the given role name.
func (r *Registry) Get(name string) (AgentDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[name]
	return def, ok
}

// Has reports whether the given role exists.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.defs[name]
	return ok
}

// List returns all registered definitions in deterministic order.
func (r *Registry) List() []AgentDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AgentDefinition, len(r.order))
	for i, n := range r.order {
		out[i] = r.defs[n]
	}
	return out
}

// Names returns all registered role names in deterministic order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}
