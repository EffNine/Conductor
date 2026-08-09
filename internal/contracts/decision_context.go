package contracts

import "fmt"

// DecisionContext is the immutable context that flows through every pipeline
// stage. It carries the request, runtime state, configuration, task metadata,
// and environment for a single routing decision.
type DecisionContext struct {
	schema     SchemaMetadata
	decisionID DecisionID
	request    RequestSpec
	runtime    *RuntimeSnapshot
	config     *ConfigSnapshot
	taskMeta   *TaskMetadata
	env        *EnvironmentSpec
}

// RequestSpec describes the incoming chat completion request.
type RequestSpec struct {
	Model        string
	Stream       bool
	Messages     []MessageSpec
	Tools        []ToolSpec
	HasImage     bool
	HasTools     bool
	MessageCount int
}

// MessageSpec describes a single message in the request.
type MessageSpec struct {
	Role    string
	Content string
	HasPart bool
}

// ToolSpec describes a tool available in the request.
type ToolSpec struct {
	Name        string
	Description string
}

// ConfigSnapshot is a point-in-time copy of the routing configuration.
type ConfigSnapshot struct {
	Routes    map[string]RouteSpec
	Aliases   map[string]string
	Fallbacks map[string][]FallbackSpec
	Weights   RoutingWeights
}

// RouteSpec maps a model ID to a provider.
type RouteSpec struct {
	Provider string
	ModelID  string
}

// FallbackSpec describes a fallback provider/model pair.
type FallbackSpec struct {
	Provider string
	ModelID  string
}

// RoutingWeights holds the relative importance of each scoring dimension.
type RoutingWeights struct {
	Health     float64
	Latency    float64
	Cost       float64
	Capability float64
}

// EnvironmentSpec holds gateway environment constants.
type EnvironmentSpec struct {
	HealthCheckIntervalMs int64
	HealthTimeoutMs       int64
	UnknownAsReachable    bool
	CircuitBreakerEnabled bool
}

// TaskMetadata carries high-level metadata about the request.
type TaskMetadata struct {
	ModelID      string
	ProviderHint string
	IsStream     bool
	HasImage     bool
	HasTools     bool
	MessageCount int
}

// NewDecisionContextBuilder creates a builder for DecisionContext.
func NewDecisionContextBuilder() *DecisionContextBuilder {
	return &DecisionContextBuilder{
		schema:     NewSchemaMetadata("decision_context"),
		decisionID: NewDecisionID(),
	}
}

// DecisionContextBuilder incrementally constructs an immutable DecisionContext.
type DecisionContextBuilder struct {
	schema     SchemaMetadata
	decisionID DecisionID
	request    RequestSpec
	runtime    *RuntimeSnapshot
	config     *ConfigSnapshot
	taskMeta   *TaskMetadata
	env        *EnvironmentSpec
}

// SetRequest sets the request specification.
func (b *DecisionContextBuilder) SetRequest(req RequestSpec) *DecisionContextBuilder {
	b.request = req
	return b
}

// SetRuntime sets the runtime snapshot.
func (b *DecisionContextBuilder) SetRuntime(snap *RuntimeSnapshot) *DecisionContextBuilder {
	b.runtime = snap
	return b
}

// SetConfig sets the configuration snapshot.
func (b *DecisionContextBuilder) SetConfig(cfg *ConfigSnapshot) *DecisionContextBuilder {
	b.config = cfg
	return b
}

// SetTaskMeta sets the task metadata.
func (b *DecisionContextBuilder) SetTaskMeta(meta *TaskMetadata) *DecisionContextBuilder {
	b.taskMeta = meta
	return b
}

// SetEnvironment sets the environment specification.
func (b *DecisionContextBuilder) SetEnvironment(env *EnvironmentSpec) *DecisionContextBuilder {
	b.env = env
	return b
}

// Build returns an immutable DecisionContext.
func (b *DecisionContextBuilder) Build() (*DecisionContext, error) {
	if b.request.Model == "" {
		return nil, fmt.Errorf("contracts: DecisionContext requires a non-empty request.Model")
	}
	return &DecisionContext{
		schema:     b.schema,
		decisionID: b.decisionID,
		request:    b.request,
		runtime:    b.runtime,
		config:     b.config,
		taskMeta:   b.taskMeta,
		env:        b.env,
	}, nil
}

// Schema returns the schema metadata.
func (c *DecisionContext) Schema() SchemaMetadata { return c.schema }

// DecisionID returns the decision ID.
func (c *DecisionContext) DecisionID() DecisionID { return c.decisionID }

// Request returns the request specification.
func (c *DecisionContext) Request() RequestSpec { return c.request }

// Runtime returns the runtime snapshot (nil if not set).
func (c *DecisionContext) Runtime() *RuntimeSnapshot { return c.runtime }

// Config returns the configuration snapshot (nil if not set).
func (c *DecisionContext) Config() *ConfigSnapshot { return c.config }

// TaskMeta returns the task metadata (nil if not set).
func (c *DecisionContext) TaskMeta() *TaskMetadata { return c.taskMeta }

// Environment returns the environment specification (nil if not set).
func (c *DecisionContext) Environment() *EnvironmentSpec { return c.env }

// Validate checks that the DecisionContext is complete and consistent.
func (c *DecisionContext) Validate() error {
	if err := c.schema.Validate(); err != nil {
		return err
	}
	if c.decisionID == "" {
		return fmt.Errorf("contracts: DecisionContext requires a non-empty DecisionID")
	}
	if c.request.Model == "" {
		return fmt.Errorf("contracts: DecisionContext requires a non-empty request.Model")
	}
	return nil
}

// Clone returns a deep copy of the DecisionContext.
func (c *DecisionContext) Clone() *DecisionContext {
	cp := &DecisionContext{
		schema:     c.schema,
		decisionID: c.decisionID,
		request:    c.request,
		runtime:    c.runtime,
		config:     c.config,
		taskMeta:   c.taskMeta,
		env:        c.env,
	}
	cp.request.Messages = cloneMessageSpecs(c.request.Messages)
	cp.request.Tools = cloneToolSpecs(c.request.Tools)
	return cp
}

func cloneMessageSpecs(msgs []MessageSpec) []MessageSpec {
	out := make([]MessageSpec, len(msgs))
	copy(out, msgs)
	return out
}

func cloneToolSpecs(tools []ToolSpec) []ToolSpec {
	out := make([]ToolSpec, len(tools))
	copy(out, tools)
	return out
}
