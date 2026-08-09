package contracts

import (
	"encoding/json"
	"time"
)

// DecisionTrace is an immutable record of a single routing decision's full
// execution timeline.
type DecisionTrace struct {
	TraceID            TraceID             `json:"trace_id"`
	DecisionID         DecisionID          `json:"decision_id"`
	SnapshotID         SnapshotID          `json:"snapshot_id"`
	RuntimeSnapshotVer int64               `json:"runtime_snapshot_ver"`
	RuntimeHash        string              `json:"runtime_hash"`
	Schema             SchemaMetadata      `json:"schema"`
	StageResults       []*StageResult      `json:"stage_results"`
	Events             []EventRecord       `json:"events"`
	CandidateList      []*CandidateRecord  `json:"candidate_list,omitempty"`
	Winner             *WinnerRecord       `json:"winner,omitempty"`
	RejectionReasons   []RejectionReason   `json:"rejection_reasons,omitempty"`
	Timestamp          time.Time           `json:"timestamp"`
}

// StageResult captures the outcome of a single pipeline stage.
type StageResult struct {
	Name       string         `json:"name"`
	DurationMs int64          `json:"duration_ms"`
	Status     StageStatus    `json:"status"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	OutputRef  string         `json:"output_ref,omitempty"`
}

// StageStatus indicates the outcome of a stage execution.
type StageStatus string

const (
	StageStatusPending   StageStatus = "pending"
	StageStatusRunning   StageStatus = "running"
	StageStatusCompleted StageStatus = "completed"
	StageStatusFailed    StageStatus = "failed"
	StageStatusSkipped   StageStatus = "skipped"
)

// EventRecord is a single event in the trace timeline.
type EventRecord struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload,omitempty"`
}

// CandidateRecord is a provider/model candidate in the decision trace.
type CandidateRecord struct {
	CandidateID     CandidateID  `json:"candidate_id"`
	ProviderName    string       `json:"provider_name"`
	ProviderModelID string       `json:"provider_model_id"`
	HealthScore     float64      `json:"health_score"`
	LatencyMs       int64        `json:"latency_ms"`
	CostPerToken    *float64     `json:"cost_per_token,omitempty"`
	Capabilities    Capabilities `json:"capabilities"`
	IsAvailable     bool         `json:"is_available"`
	TotalScore      float64      `json:"total_score"`
	Selected        bool         `json:"selected"`
	Rejected        bool         `json:"rejected"`
	RejectionReason string       `json:"rejection_reason,omitempty"`
}

// WinnerRecord is the selected provider route.
type WinnerRecord struct {
	ProviderName    string `json:"provider_name"`
	ProviderModelID string `json:"provider_model_id"`
	ModelID         string `json:"model_id"`
}

// RejectionReason describes why a candidate was not selected.
type RejectionReason struct {
	Provider string `json:"provider"`
	Reason   string `json:"reason"`
}

// Capabilities describes what a provider/model supports.
type Capabilities struct {
	Vision      bool
	Streaming   bool
	Reasoning   bool
	ToolCalling bool
	Structured  bool
	MaxContext  int
}

// DecisionTraceBuilder incrementally constructs a DecisionTrace.
type DecisionTraceBuilder struct {
	trace *DecisionTrace
}

// NewDecisionTraceBuilder creates a builder for a new trace.
func NewDecisionTraceBuilder(decisionID DecisionID, snapVer int64, snapID SnapshotID, runtimeHash string) *DecisionTraceBuilder {
	return &DecisionTraceBuilder{
		trace: &DecisionTrace{
			TraceID:            NewTraceID(),
			DecisionID:         decisionID,
			SnapshotID:         snapID,
			RuntimeSnapshotVer: snapVer,
			RuntimeHash:        runtimeHash,
			Schema:             NewSchemaMetadata("decision_trace"),
			StageResults:       make([]*StageResult, 0),
			Events:             make([]EventRecord, 0),
			Timestamp:          time.Now().UTC(),
		},
	}
}

// AddStageResult appends a stage result to the trace.
func (b *DecisionTraceBuilder) AddStageResult(r *StageResult) *DecisionTraceBuilder {
	b.trace.StageResults = append(b.trace.StageResults, r)
	return b
}

// AddEvent appends an event to the trace timeline.
func (b *DecisionTraceBuilder) AddEvent(e EventRecord) *DecisionTraceBuilder {
	b.trace.Events = append(b.trace.Events, e)
	return b
}

// SetCandidates records the candidate list.
func (b *DecisionTraceBuilder) SetCandidates(cands []*CandidateRecord) *DecisionTraceBuilder {
	b.trace.CandidateList = cands
	return b
}

// SetWinner records the selected provider route.
func (b *DecisionTraceBuilder) SetWinner(w *WinnerRecord) *DecisionTraceBuilder {
	b.trace.Winner = w
	return b
}

// SetRejectionReasons records all rejection reasons.
func (b *DecisionTraceBuilder) SetRejectionReasons(reasons []RejectionReason) *DecisionTraceBuilder {
	b.trace.RejectionReasons = reasons
	return b
}

// Build returns the completed immutable trace.
func (b *DecisionTraceBuilder) Build() (*DecisionTrace, error) {
	if err := b.trace.Schema.Validate(); err != nil {
		return nil, err
	}
	if b.trace.DecisionID == "" {
		return nil, BuilderError{"DecisionTrace", "decision_id is empty"}
	}
	return b.trace, nil
}

// Validate checks that the DecisionTrace is well-formed.
func (t *DecisionTrace) Validate() error {
	if err := t.Schema.Validate(); err != nil {
		return err
	}
	if t.DecisionID == "" {
		return BuilderError{"DecisionTrace", "decision_id is empty"}
	}
	if t.TraceID == "" {
		return BuilderError{"DecisionTrace", "trace_id is empty"}
	}
	if len(t.StageResults) == 0 {
		return BuilderError{"DecisionTrace", "stage_results is empty"}
	}
	return nil
}

// Clone returns a deep copy of the DecisionTrace.
func (t *DecisionTrace) Clone() *DecisionTrace {
	cp := &DecisionTrace{
		TraceID:            t.TraceID,
		DecisionID:         t.DecisionID,
		SnapshotID:         t.SnapshotID,
		RuntimeSnapshotVer: t.RuntimeSnapshotVer,
		RuntimeHash:        t.RuntimeHash,
		Schema:             t.Schema,
		StageResults:       make([]*StageResult, len(t.StageResults)),
		Events:             make([]EventRecord, len(t.Events)),
		CandidateList:      make([]*CandidateRecord, len(t.CandidateList)),
		Timestamp:          t.Timestamp,
	}
	if t.Winner != nil {
		w := *t.Winner
		cp.Winner = &w
	}
	if len(t.RejectionReasons) > 0 {
		cp.RejectionReasons = make([]RejectionReason, len(t.RejectionReasons))
		copy(cp.RejectionReasons, t.RejectionReasons)
	}
	for i, sr := range t.StageResults {
		s := *sr
		if sr.Metadata != nil {
			s.Metadata = make(map[string]any, len(sr.Metadata))
			for k, v := range sr.Metadata {
				s.Metadata[k] = v
			}
		}
		cp.StageResults[i] = &s
	}
	for i, e := range t.Events {
		cp.Events[i] = e
	}
	for i, c := range t.CandidateList {
		cc := *c
		cp.CandidateList[i] = &cc
	}
	return cp
}

// Marshal serializes the trace to JSON.
func (t *DecisionTrace) Marshal() ([]byte, error) {
	return json.Marshal(t)
}

// Unmarshal deserializes a trace from JSON.
func UnmarshalTrace(data []byte) (*DecisionTrace, error) {
	var t DecisionTrace
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
