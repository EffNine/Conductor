package tool

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrInvalidTool is returned when a tool fails validation.
var ErrInvalidTool = errors.New("invalid tool")

// ErrDuplicateTool is returned when registering a tool with an existing name.
var ErrDuplicateTool = errors.New("duplicate tool")

// ErrToolNotFound is returned when a tool does not exist.
var ErrToolNotFound = errors.New("tool not found")

// Registry holds registered tools and provides thread-safe lookup.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string // preserves insertion order for deterministic listing
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
		order: make([]string, 0),
	}
}

// Register adds a tool to the registry.
// Returns ErrInvalidTool if tool is nil or has an empty name.
// Returns ErrDuplicateTool if a tool with the same name already exists.
func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("%w: tool is nil", ErrInvalidTool)
	}
	name := tool.Name()
	if name == "" {
		return fmt.Errorf("%w: tool name is empty", ErrInvalidTool)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateTool, name)
	}

	r.tools[name] = tool
	r.order = append(r.order, name)
	return nil
}

// Unregister removes a tool from the registry.
// Returns ErrToolNotFound if the tool does not exist.
func (r *Registry) Unregister(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrToolNotFound)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; !exists {
		return fmt.Errorf("%w: %q", ErrToolNotFound, name)
	}

	delete(r.tools, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

// Get retrieves a tool by name.
// Returns (nil, false) if the tool does not exist.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Has reports whether a tool with the given name is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[name]
	return ok
}

// List returns all registered tools in deterministic (insertion) order.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, len(r.order))
	for i, n := range r.order {
		out[i] = r.tools[n]
	}
	return out
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Names returns all registered tool names in deterministic order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// SortByName returns a copy of all tools sorted alphabetically by name.
func (r *Registry) SortByName() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ordered := make([]string, len(r.order))
	copy(ordered, r.order)
	sort.Strings(ordered)
	out := make([]Tool, len(ordered))
	for i, n := range ordered {
		out[i] = r.tools[n]
	}
	return out
}
