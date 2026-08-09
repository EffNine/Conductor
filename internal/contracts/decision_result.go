package contracts

import (
	"encoding/json"
	"time"
)

// DecisionResult is the immutable outcome of a routing decision.
type DecisionResult struct {
	SnapshotID SnapshotID       `json:"snapshot_id"`
	Schema     SchemaMetadata   `json:"schema"`
	DecisionID DecisionID       `json:"decision_id"`
	Winner     *ResolvedRoute   `json:"winner"`
	Candidates []*CandidateRecord `json:"candidates"`
	Confidence float64          `json:"confidence"`
	Decision   RoutingDecision  `json:"decision"`
	Timestamp  time.Time        `json:"timestamp"`
}

// ResolvedRoute is the resolved provider route for a decision.
type ResolvedRoute struct {
	ProviderName    string `json:"provider_name"`
	ProviderModelID string `json:"provider_model_id"`
	ModelID         string `json:"model_id"`
}

// RoutingDecision is the result of the scoring and selection process.
type RoutingDecision struct {
	SelectedProvider    string            `json:"selected_provider"`
	SelectedModelID     string            `json:"selected_model_id"`
	SelectedProviderID  string            `json:"selected_provider_model_id"`
	CandidateScores     []CandidateScore  `json:"candidate_scores"`
	RejectionReasons    []RejectionReason `json:"rejection_reasons,omitempty"`
	RoutingDurationMs   int64             `json:"routing_duration_ms"`
}

// CandidateScore is the score for one candidate provider.
type CandidateScore struct {
	Provider     string  `json:"provider"`
	ProviderID   string  `json:"provider_model_id"`
	TotalScore   float64 `json:"total_score"`
	HealthScore  float64 `json:"health_score"`
	LatencyScore float64 `json:"latency_score"`
	CostScore    float64 `json:"cost_score"`
	CapScore     float64 `json:"capability_score"`
	Selected     bool    `json:"selected"`
	Rejected     bool    `json:"rejected"`
	RejectionReason string `json:"rejection_reason,omitempty"`
}

// NewDecisionResultBuilder creates a builder for DecisionResult.
func NewDecisionResultBuilder(decisionID DecisionID, snapID SnapshotID) *DecisionResultBuilder {
	return &DecisionResultBuilder{
		schema:     NewSchemaMetadata("decision_result"),
		decisionID: decisionID,
		snapshotID: snapID,
		timestamp:  time.Now().UTC(),
	}
}

// DecisionResultBuilder incrementally constructs an immutable DecisionResult.
type DecisionResultBuilder struct {
	schema     SchemaMetadata
	decisionID DecisionID
	snapshotID SnapshotID
	winner     *ResolvedRoute
	candidates []*CandidateRecord
	confidence float64
	decision   RoutingDecision
	timestamp  time.Time
}

// SetWinner sets the winning resolved route.
func (b *DecisionResultBuilder) SetWinner(w *ResolvedRoute) *DecisionResultBuilder {
	b.winner = w
	return b
}

// AddCandidate adds a candidate record.
func (b *DecisionResultBuilder) AddCandidate(c *CandidateRecord) *DecisionResultBuilder {
	b.candidates = append(b.candidates, c)
	return b
}

// SetDecision sets the routing decision data.
func (b *DecisionResultBuilder) SetDecision(d RoutingDecision) *DecisionResultBuilder {
	b.decision = d
	return b
}

// SetConfidence sets the confidence score.
func (b *DecisionResultBuilder) SetConfidence(c float64) *DecisionResultBuilder {
	b.confidence = c
	return b
}

// Build returns an immutable DecisionResult.
func (b *DecisionResultBuilder) Build() (*DecisionResult, error) {
	if err := b.schema.Validate(); err != nil {
		return nil, err
	}
	if b.decisionID == "" {
		return nil, BuilderError{"DecisionResult", "decision_id is empty"}
	}
	return &DecisionResult{
		SnapshotID: b.snapshotID,
		Schema:     b.schema,
		DecisionID: b.decisionID,
		Winner:     b.winner,
		Candidates: b.candidates,
		Confidence: b.confidence,
		Decision:   b.decision,
		Timestamp:  b.timestamp,
	}, nil
}

// Validate checks that the DecisionResult is well-formed.
func (r *DecisionResult) Validate() error {
	if err := r.Schema.Validate(); err != nil {
		return err
	}
	if r.DecisionID == "" {
		return BuilderError{"DecisionResult", "decision_id is empty"}
	}
	return nil
}

// Clone returns a deep copy of the DecisionResult.
func (r *DecisionResult) Clone() *DecisionResult {
	cp := &DecisionResult{
		SnapshotID: r.SnapshotID,
		Schema:     r.Schema,
		DecisionID: r.DecisionID,
		Confidence: r.Confidence,
		Decision:   r.Decision,
		Timestamp:  r.Timestamp,
	}
	if r.Winner != nil {
		w := *r.Winner
		cp.Winner = &w
	}
	cp.Candidates = make([]*CandidateRecord, len(r.Candidates))
	for i, c := range r.Candidates {
		cc := *c
		cp.Candidates[i] = &cc
	}
	return cp
}

// Marshal serializes the result to JSON.
func (r *DecisionResult) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

// Unmarshal deserializes a decision result from JSON.
func UnmarshalDecisionResult(data []byte) (*DecisionResult, error) {
	var r DecisionResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ExecutionResult is the immutable outcome of executing a resolved route.
type ExecutionResult struct {
	ExecutionID   ExecutionID    `json:"execution_id"`
	DecisionID    DecisionID     `json:"decision_id"`
	TraceID       TraceID        `json:"trace_id"`
	SnapshotID    SnapshotID     `json:"snapshot_id"`
	Schema        SchemaMetadata `json:"schema"`
	ProviderName  string         `json:"provider_name"`
	ProviderModel string         `json:"provider_model_id"`
	ModelID       string         `json:"model_id"`
	Success       bool           `json:"success"`
	LatencyMs     int64          `json:"latency_ms"`
	StatusCode    int            `json:"status_code"`
	Error         string         `json:"error,omitempty"`
	Usage         *UsageRecord   `json:"usage,omitempty"`
	Timestamp     time.Time      `json:"timestamp"`
}

// UsageRecord captures token usage from a response.
type UsageRecord struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// NewExecutionResultBuilder creates a builder for ExecutionResult.
func NewExecutionResultBuilder(decisionID DecisionID, traceID TraceID, snapID SnapshotID) *ExecutionResultBuilder {
	return &ExecutionResultBuilder{
		schema:     NewSchemaMetadata("execution_result"),
		executionID: NewExecutionID(),
		decisionID: decisionID,
		traceID:    traceID,
		snapshotID: snapID,
		timestamp:  time.Now().UTC(),
	}
}

// ExecutionResultBuilder incrementally constructs an immutable ExecutionResult.
type ExecutionResultBuilder struct {
	schema        SchemaMetadata
	executionID   ExecutionID
	decisionID    DecisionID
	traceID       TraceID
	snapshotID    SnapshotID
	providerName  string
	providerModel string
	modelID       string
	success       bool
	latencyMs     int64
	statusCode    int
	err           string
	usage         *UsageRecord
	timestamp     time.Time
}

// SetProvider sets the executed provider.
func (b *ExecutionResultBuilder) SetProvider(provider, model, routeModel string) *ExecutionResultBuilder {
	b.providerName = provider
	b.providerModel = model
	b.modelID = routeModel
	return b
}

// SetSuccess marks the execution as successful.
func (b *ExecutionResultBuilder) SetSuccess(success bool) *ExecutionResultBuilder {
	b.success = success
	return b
}

// SetLatency records the execution latency.
func (b *ExecutionResultBuilder) SetLatency(ms int64) *ExecutionResultBuilder {
	b.latencyMs = ms
	return b
}

// SetStatusCode records the HTTP status code.
func (b *ExecutionResultBuilder) SetStatusCode(code int) *ExecutionResultBuilder {
	b.statusCode = code
	return b
}

// SetError records an execution error.
func (b *ExecutionResultBuilder) SetError(err error) *ExecutionResultBuilder {
	if err != nil {
		b.err = err.Error()
	}
	return b
}

// SetUsage records token usage.
func (b *ExecutionResultBuilder) SetUsage(u *UsageRecord) *ExecutionResultBuilder {
	b.usage = u
	return b
}

// Build returns an immutable ExecutionResult.
func (b *ExecutionResultBuilder) Build() (*ExecutionResult, error) {
	if err := b.schema.Validate(); err != nil {
		return nil, err
	}
	if b.decisionID == "" {
		return nil, BuilderError{"ExecutionResult", "decision_id is empty"}
	}
	if b.providerName == "" {
		return nil, BuilderError{"ExecutionResult", "provider_name is empty"}
	}
	return &ExecutionResult{
		ExecutionID:   b.executionID,
		DecisionID:    b.decisionID,
		TraceID:       b.traceID,
		SnapshotID:    b.snapshotID,
		Schema:        b.schema,
		ProviderName:  b.providerName,
		ProviderModel: b.providerModel,
		ModelID:       b.modelID,
		Success:       b.success,
		LatencyMs:     b.latencyMs,
		StatusCode:    b.statusCode,
		Error:         b.err,
		Usage:         b.usage,
		Timestamp:     b.timestamp,
	}, nil
}

// Validate checks that the ExecutionResult is well-formed.
func (r *ExecutionResult) Validate() error {
	if err := r.Schema.Validate(); err != nil {
		return err
	}
	if r.DecisionID == "" {
		return BuilderError{"ExecutionResult", "decision_id is empty"}
	}
	if r.ExecutionID == "" {
		return BuilderError{"ExecutionResult", "execution_id is empty"}
	}
	return nil
}

// Clone returns a deep copy of the ExecutionResult.
func (r *ExecutionResult) Clone() *ExecutionResult {
	cp := &ExecutionResult{
		ExecutionID:   r.ExecutionID,
		DecisionID:    r.DecisionID,
		TraceID:       r.TraceID,
		SnapshotID:    r.SnapshotID,
		Schema:        r.Schema,
		ProviderName:  r.ProviderName,
		ProviderModel: r.ProviderModel,
		ModelID:       r.ModelID,
		Success:       r.Success,
		LatencyMs:     r.LatencyMs,
		StatusCode:    r.StatusCode,
		Error:         r.Error,
		Timestamp:     r.Timestamp,
	}
	if r.Usage != nil {
		u := *r.Usage
		cp.Usage = &u
	}
	return cp
}

// Marshal serializes the result to JSON.
func (r *ExecutionResult) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

// Unmarshal deserializes an execution result from JSON.
func UnmarshalExecutionResult(data []byte) (*ExecutionResult, error) {
	var r ExecutionResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ProviderSnapshot is the immutable contract for a single provider's state.
type ProviderSnapshot struct {
	ProviderID  ProviderID       `json:"provider_id"`
	SnapshotID  SnapshotID       `json:"snapshot_id"`
	Schema      SchemaMetadata   `json:"schema"`
	State       string           `json:"state"`
	LatencyMs   int64            `json:"latency_ms"`
	ErrorRate   float64          `json:"error_rate"`
	Capacity    float64          `json:"capacity"`
	IsHealthy   bool             `json:"is_healthy"`
	Timestamp   time.Time        `json:"timestamp"`
}

// NewProviderSnapshotBuilder creates a builder for ProviderSnapshot.
func NewProviderSnapshotBuilder(providerID ProviderID) *ProviderSnapshotBuilder {
	return &ProviderSnapshotBuilder{
		schema:     NewSchemaMetadata("provider_snapshot"),
		providerID: providerID,
		snapshotID: NewSnapshotID(),
		timestamp:  time.Now().UTC(),
	}
}

// ProviderSnapshotBuilder incrementally constructs an immutable ProviderSnapshot.
type ProviderSnapshotBuilder struct {
	schema     SchemaMetadata
	providerID ProviderID
	snapshotID SnapshotID
	state      string
	latencyMs  int64
	errorRate  float64
	capacity   float64
	isHealthy  bool
	timestamp  time.Time
}

// SetState sets the provider state.
func (b *ProviderSnapshotBuilder) SetState(state string) *ProviderSnapshotBuilder {
	b.state = state
	return b
}

// SetLatency sets the latency.
func (b *ProviderSnapshotBuilder) SetLatency(ms int64) *ProviderSnapshotBuilder {
	b.latencyMs = ms
	return b
}

// SetErrorRate sets the error rate.
func (b *ProviderSnapshotBuilder) SetErrorRate(rate float64) *ProviderSnapshotBuilder {
	b.errorRate = rate
	return b
}

// SetCapacity sets the capacity.
func (b *ProviderSnapshotBuilder) SetCapacity(cap float64) *ProviderSnapshotBuilder {
	b.capacity = cap
	return b
}

// SetHealthy sets the health flag.
func (b *ProviderSnapshotBuilder) SetHealthy(healthy bool) *ProviderSnapshotBuilder {
	b.isHealthy = healthy
	return b
}

// Build returns an immutable ProviderSnapshot.
func (b *ProviderSnapshotBuilder) Build() (*ProviderSnapshot, error) {
	if err := b.schema.Validate(); err != nil {
		return nil, err
	}
	if b.providerID == "" {
		return nil, BuilderError{"ProviderSnapshot", "provider_id is empty"}
	}
	return &ProviderSnapshot{
		ProviderID: b.providerID,
		SnapshotID: b.snapshotID,
		Schema:     b.schema,
		State:      b.state,
		LatencyMs:  b.latencyMs,
		ErrorRate:  b.errorRate,
		Capacity:   b.capacity,
		IsHealthy:  b.isHealthy,
		Timestamp:  b.timestamp,
	}, nil
}

// Validate checks that the ProviderSnapshot is well-formed.
func (s *ProviderSnapshot) Validate() error {
	if err := s.Schema.Validate(); err != nil {
		return err
	}
	if s.ProviderID == "" {
		return BuilderError{"ProviderSnapshot", "provider_id is empty"}
	}
	return nil
}

// Clone returns a deep copy of the ProviderSnapshot.
func (s *ProviderSnapshot) Clone() *ProviderSnapshot {
	cp := *s
	return &cp
}

// Marshal serializes the snapshot to JSON.
func (s *ProviderSnapshot) Marshal() ([]byte, error) {
	return json.Marshal(s)
}

// Unmarshal deserializes a provider snapshot from JSON.
func UnmarshalProviderSnapshot(data []byte) (*ProviderSnapshot, error) {
	var s ProviderSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Candidate is the contract-level representation of a scoring candidate.
type Candidate struct {
	CandidateID     CandidateID  `json:"candidate_id"`
	ProviderID      ProviderID   `json:"provider_id"`
	ProviderModelID string       `json:"provider_model_id"`
	Schema          SchemaMetadata `json:"schema"`
	HealthScore     float64      `json:"health_score"`
	LatencyMs       int64        `json:"latency_ms"`
	CostPerToken    *float64     `json:"cost_per_token,omitempty"`
	Capabilities    Capabilities `json:"capabilities"`
	IsAvailable     bool         `json:"is_available"`
	TotalScore      float64      `json:"total_score"`
	RejectionReason string       `json:"rejection_reason,omitempty"`
	Timestamp       time.Time    `json:"timestamp"`
}

// NewCandidateBuilder creates a builder for Candidate.
func NewCandidateBuilder(providerID ProviderID, modelID string) *CandidateBuilder {
	return &CandidateBuilder{
		schema:      NewSchemaMetadata("candidate"),
		candidateID: NewCandidateID(string(providerID), modelID),
		providerID:  providerID,
		modelID:     modelID,
		timestamp:   time.Now().UTC(),
	}
}

// CandidateBuilder incrementally constructs an immutable Candidate.
type CandidateBuilder struct {
	schema      SchemaMetadata
	candidateID CandidateID
	providerID  ProviderID
	modelID     string
	healthScore float64
	latencyMs   int64
	costPerToken *float64
	capabilities Capabilities
	isAvailable bool
	totalScore  float64
	rejectionReason string
	timestamp   time.Time
}

// SetHealthScore sets the health score.
func (b *CandidateBuilder) SetHealthScore(s float64) *CandidateBuilder {
	b.healthScore = s
	return b
}

// SetLatency sets the latency.
func (b *CandidateBuilder) SetLatency(ms int64) *CandidateBuilder {
	b.latencyMs = ms
	return b
}

// SetCostPerToken sets the cost per token.
func (b *CandidateBuilder) SetCostPerToken(cost *float64) *CandidateBuilder {
	b.costPerToken = cost
	return b
}

// SetCapabilities sets the provider capabilities.
func (b *CandidateBuilder) SetCapabilities(c Capabilities) *CandidateBuilder {
	b.capabilities = c
	return b
}

// SetAvailable sets the availability flag.
func (b *CandidateBuilder) SetAvailable(avail bool) *CandidateBuilder {
	b.isAvailable = avail
	return b
}

// SetTotalScore sets the composite score.
func (b *CandidateBuilder) SetTotalScore(s float64) *CandidateBuilder {
	b.totalScore = s
	return b
}

// SetRejectionReason sets the rejection reason.
func (b *CandidateBuilder) SetRejectionReason(reason string) *CandidateBuilder {
	b.rejectionReason = reason
	return b
}

// Build returns an immutable Candidate.
func (b *CandidateBuilder) Build() (*Candidate, error) {
	if err := b.schema.Validate(); err != nil {
		return nil, err
	}
	if b.providerID == "" {
		return nil, BuilderError{"Candidate", "provider_id is empty"}
	}
	if b.modelID == "" {
		return nil, BuilderError{"Candidate", "model_id is empty"}
	}
	return &Candidate{
		CandidateID:     b.candidateID,
		ProviderID:      b.providerID,
		ProviderModelID: b.modelID,
		Schema:          b.schema,
		HealthScore:     b.healthScore,
		LatencyMs:       b.latencyMs,
		CostPerToken:    b.costPerToken,
		Capabilities:    b.capabilities,
		IsAvailable:     b.isAvailable,
		TotalScore:      b.totalScore,
		RejectionReason: b.rejectionReason,
		Timestamp:       b.timestamp,
	}, nil
}

// Validate checks that the Candidate is well-formed.
func (c *Candidate) Validate() error {
	if err := c.Schema.Validate(); err != nil {
		return err
	}
	if c.ProviderID == "" {
		return BuilderError{"Candidate", "provider_id is empty"}
	}
	if c.ProviderModelID == "" {
		return BuilderError{"Candidate", "provider_model_id is empty"}
	}
	return nil
}

// Clone returns a deep copy of the Candidate.
func (c *Candidate) Clone() *Candidate {
	cp := *c
	if c.CostPerToken != nil {
		v := *c.CostPerToken
		cp.CostPerToken = &v
	}
	return &cp
}

// Marshal serializes the candidate to JSON.
func (c *Candidate) Marshal() ([]byte, error) {
	return json.Marshal(c)
}

// Unmarshal deserializes a candidate from JSON.
func UnmarshalCandidate(data []byte) (*Candidate, error) {
	var c Candidate
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
