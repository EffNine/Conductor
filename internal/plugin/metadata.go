package plugin

import "time"

// PluginMetadata holds static information about a plugin.
type PluginMetadata struct {
	// ID is the unique plugin instance identifier.
	ID string `json:"id"`

	// Name is the human-readable plugin name.
	Name string `json:"name"`

	// Version is the semantic version string.
	Version string `json:"version"`

	// Category is the plugin category.
	Category PluginCategory `json:"category"`

	// Description is a human-readable description of the plugin.
	Description string `json:"description"`

	// Author is the plugin author or organization.
	Author string `json:"author"`

	// Tags are optional keywords for plugin discovery.
	Tags []string `json:"tags,omitempty"`

	// ConfigSchema describes the expected configuration keys.
	ConfigSchema map[string]ConfigField `json:"config_schema,omitempty"`

	// RegisteredAt is the time the plugin was registered.
	RegisteredAt time.Time `json:"registered_at"`

	// DeprecationNote is set if the plugin is deprecated.
	DeprecationNote string `json:"deprecation_note,omitempty"`
}

// ConfigField describes a single configuration field.
type ConfigField struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Default     any    `json:"default,omitempty"`
	Required    bool   `json:"required"`
}

// NewPluginMetadata creates PluginMetadata with the given id, name, version, and category.
func NewPluginMetadata(id, name, version string, category PluginCategory) PluginMetadata {
	return PluginMetadata{
		ID:           id,
		Name:         name,
		Version:      version,
		Category:     category,
		RegisteredAt: time.Now().UTC(),
	}
}

// WithDescription sets the description and returns the metadata for chaining.
func (m PluginMetadata) WithDescription(desc string) PluginMetadata {
	m.Description = desc
	return m
}

// WithAuthor sets the author and returns the metadata for chaining.
func (m PluginMetadata) WithAuthor(author string) PluginMetadata {
	m.Author = author
	return m
}

// WithTags sets the tags and returns the metadata for chaining.
func (m PluginMetadata) WithTags(tags []string) PluginMetadata {
	m.Tags = tags
	return m
}

// WithConfigSchema sets the config schema and returns the metadata for chaining.
func (m PluginMetadata) WithConfigSchema(schema map[string]ConfigField) PluginMetadata {
	m.ConfigSchema = schema
	return m
}

// WithDeprecationNote sets the deprecation note and returns the metadata for chaining.
func (m PluginMetadata) WithDeprecationNote(note string) PluginMetadata {
	m.DeprecationNote = note
	return m
}
