package contracts

// ─── Canonical IDs ───────────────────────────────────────────────────────────

// DecisionID is a UUID v4 that uniquely identifies a single routing decision.
type DecisionID string

// TraceID is a UUID v4 that uniquely identifies a single decision trace record.
type TraceID string

// SnapshotID is a UUID v4 that uniquely identifies a runtime snapshot.
type SnapshotID string

// ExecutionID is a UUID v4 that uniquely identifies a single provider execution.
type ExecutionID string

// CandidateID is a composite identifier: "provider/model".
type CandidateID string

// ProviderID is the provider name (e.g. "openai", "groq").
type ProviderID string

// PolicyID identifies the active routing policy (e.g. "default", "cost-optimized").
type PolicyID string

// NewDecisionID generates a new DecisionID.
func NewDecisionID() DecisionID {
	return DecisionID(generateUUID())
}

// NewTraceID generates a new TraceID.
func NewTraceID() TraceID {
	return TraceID(generateUUID())
}

// NewSnapshotID generates a new SnapshotID.
func NewSnapshotID() SnapshotID {
	return SnapshotID(generateUUID())
}

// NewExecutionID generates a new ExecutionID.
func NewExecutionID() ExecutionID {
	return ExecutionID(generateUUID())
}

// NewCandidateID creates a candidate ID from provider and model.
func NewCandidateID(provider, model string) CandidateID {
	return CandidateID(provider + "/" + model)
}

// NewProviderID creates a provider ID from a name string.
func NewProviderID(name string) ProviderID {
	return ProviderID(name)
}

// NewPolicyID creates a policy ID from a name string.
func NewPolicyID(name string) PolicyID {
	return PolicyID(name)
}
