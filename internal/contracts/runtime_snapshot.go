package contracts

// RuntimeSnapshot captures a point-in-time view of all provider runtimes.
type RuntimeSnapshot struct {
	SnapshotID SnapshotID                  `json:"snapshot_id"`
	Schema     SchemaMetadata              `json:"schema"`
	Providers  map[ProviderID]ProviderInfo `json:"providers"`
	Global     GlobalRuntimeState          `json:"global"`
}

// ProviderInfo holds per-provider health and performance data.
type ProviderInfo struct {
	State     string  `json:"state"`
	LatencyMs int64   `json:"latency_ms"`
	ErrorRate float64 `json:"error_rate"`
	Capacity  float64 `json:"capacity"`
	IsHealthy bool    `json:"is_healthy"`
}

// GlobalRuntimeState captures system-wide runtime state.
type GlobalRuntimeState struct {
	TotalProviders    int     `json:"total_providers"`
	HealthyProviders  int     `json:"healthy_providers"`
	DegradedProviders int     `json:"degraded_providers"`
	UnhealthyProviders int    `json:"unhealthy_providers"`
	AvgLatencyMs      int64   `json:"avg_latency_ms"`
	TotalQPS          float64 `json:"total_qps"`
}

// NewRuntimeSnapshotBuilder creates a builder for RuntimeSnapshot.
func NewRuntimeSnapshotBuilder() *RuntimeSnapshotBuilder {
	return &RuntimeSnapshotBuilder{
		snapshotID: NewSnapshotID(),
		schema:     NewSchemaMetadata("runtime_snapshot"),
		providers:  make(map[ProviderID]ProviderInfo),
	}
}

// RuntimeSnapshotBuilder incrementally constructs an immutable RuntimeSnapshot.
type RuntimeSnapshotBuilder struct {
	snapshotID SnapshotID
	schema     SchemaMetadata
	providers  map[ProviderID]ProviderInfo
	global     GlobalRuntimeState
}

// SetProvider sets the info for a single provider.
func (b *RuntimeSnapshotBuilder) SetProvider(id ProviderID, info ProviderInfo) *RuntimeSnapshotBuilder {
	b.providers[id] = info
	return b
}

// SetGlobal sets the global runtime state.
func (b *RuntimeSnapshotBuilder) SetGlobal(g GlobalRuntimeState) *RuntimeSnapshotBuilder {
	b.global = g
	return b
}

// Build returns an immutable RuntimeSnapshot.
func (b *RuntimeSnapshotBuilder) Build() (*RuntimeSnapshot, error) {
	if err := b.schema.Validate(); err != nil {
		return nil, err
	}
	// SnapshotID must be non-empty.
	if b.snapshotID == "" {
		return nil, BuilderError{"RuntimeSnapshot", "snapshot_id is empty"}
	}
	return &RuntimeSnapshot{
		SnapshotID: b.snapshotID,
		Schema:     b.schema,
		Providers:  b.providers,
		Global:     b.global,
	}, nil
}

// Validate checks that the RuntimeSnapshot is well-formed.
func (s *RuntimeSnapshot) Validate() error {
	if err := s.Schema.Validate(); err != nil {
		return err
	}
	if s.SnapshotID == "" {
		return BuilderError{"RuntimeSnapshot", "snapshot_id is empty"}
	}
	return nil
}

// Clone returns a deep copy of the RuntimeSnapshot.
func (s *RuntimeSnapshot) Clone() *RuntimeSnapshot {
	cp := &RuntimeSnapshot{
		SnapshotID: s.SnapshotID,
		Schema:     s.Schema,
		Global:     s.Global,
		Providers:  make(map[ProviderID]ProviderInfo, len(s.Providers)),
	}
	for k, v := range s.Providers {
		cp.Providers[k] = v
	}
	return cp
}

// BuilderError is a validation error from a builder.
type BuilderError struct {
	Type  string
	Field string
}

func (e BuilderError) Error() string {
	return "contracts: " + e.Type + ": " + e.Field
}
