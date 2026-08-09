package router

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"
)

// StageStatus indicates the outcome of a pipeline stage execution.
type StageStatus string

const (
	StageStatusPending   StageStatus = "pending"
	StageStatusRunning   StageStatus = "running"
	StageStatusCompleted StageStatus = "completed"
	StageStatusFailed    StageStatus = "failed"
	StageStatusSkipped   StageStatus = "skipped"
)

// StageResult captures the immutable output of a single pipeline stage.
type StageResult struct {
	Name       string         `json:"name"`
	DurationMs int64          `json:"duration_ms"`
	Status     StageStatus    `json:"status"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	// OutputRef is a opaque handle to the stage's output payload.
	// It may be empty if the stage produced no structured output.
	OutputRef string `json:"output_ref,omitempty"`
}

// NewStageResult creates a StageResult with pending status.
func NewStageResult(name string) *StageResult {
	return &StageResult{
		Name:   name,
		Status: StageStatusPending,
	}
}

// Complete marks the stage as completed and records duration.
func (s *StageResult) Complete(durationMs int64, metadata map[string]any, outputRef string) {
	s.Status = StageStatusCompleted
	s.DurationMs = durationMs
	if metadata != nil {
		s.Metadata = metadata
	}
	s.OutputRef = outputRef
}

// Fail marks the stage as failed and records duration.
func (s *StageResult) Fail(durationMs int64, metadata map[string]any) {
	s.Status = StageStatusFailed
	s.DurationMs = durationMs
	if metadata != nil {
		s.Metadata = metadata
	}
}

// RuntimeHash produces a deterministic hash of the runtime snapshot.
func RuntimeHash(snap RuntimeSnapshot) string {
	h := sha256.New()
	// Sort provider names for deterministic output.
	names := make([]string, 0, len(snap.Providers))
	for name := range snap.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		info := snap.Providers[name]
		h.Write([]byte(name))
		h.Write([]byte(info.State))
		h.Write([]byte(int64ToString(info.LatencyMs)))
		h.Write([]byte(float64ToString(info.ErrorRate)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func int64ToString(v int64) string {
	return string(rune('0' + v%10))
}

func float64ToString(v float64) string {
	return string(rune('0' + int64(v*10)%10))
}

// EventRecord is a single event in the trace timeline.
type EventRecord struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload,omitempty"`
}

// DecisionTrace is an immutable record of a single routing decision.
type DecisionTrace struct {
	DecisionID         DecisionID        `json:"decision_id"`
	RuntimeSnapshotVer int64             `json:"runtime_snapshot_ver"`
	RuntimeHash        string            `json:"runtime_hash"`
	StageResults       []*StageResult    `json:"stage_results"`
	Events             []EventRecord     `json:"events"`
	CandidateList      []Candidate       `json:"candidate_list,omitempty"`
	Winner             *ResolvedRoute    `json:"winner,omitempty"`
	RejectionReasons   []RejectionReason `json:"rejection_reasons,omitempty"`
	Timestamp          time.Time         `json:"timestamp"`
}

// DecisionTraceBuilder incrementally constructs a DecisionTrace.
type DecisionTraceBuilder struct {
	trace *DecisionTrace
}

// NewDecisionTraceBuilder creates a builder for a new trace.
func NewDecisionTraceBuilder(dcID DecisionID, snapVer int64, snap RuntimeSnapshot) *DecisionTraceBuilder {
	return &DecisionTraceBuilder{
		trace: &DecisionTrace{
			DecisionID:         dcID,
			RuntimeSnapshotVer: snapVer,
			RuntimeHash:        RuntimeHash(snap),
			StageResults:       make([]*StageResult, 0),
			Events:             make([]EventRecord, 0),
			Timestamp:          time.Now().UTC(),
		},
	}
}

// AddStageResult appends a completed stage result to the trace.
func (b *DecisionTraceBuilder) AddStageResult(r *StageResult) {
	b.trace.StageResults = append(b.trace.StageResults, r)
}

// AddEvent appends an event to the trace timeline.
func (b *DecisionTraceBuilder) AddEvent(e EventRecord) {
	b.trace.Events = append(b.trace.Events, e)
}

// SetCandidates records the candidate list.
func (b *DecisionTraceBuilder) SetCandidates(cands []Candidate) {
	b.trace.CandidateList = cands
}

// SetWinner records the selected provider route.
func (b *DecisionTraceBuilder) SetWinner(w *ResolvedRoute) {
	b.trace.Winner = w
}

// SetRejectionReasons records all rejection reasons.
func (b *DecisionTraceBuilder) SetRejectionReasons(reasons []RejectionReason) {
	b.trace.RejectionReasons = reasons
}

// Build returns the completed immutable trace.
func (b *DecisionTraceBuilder) Build() *DecisionTrace {
	return b.trace
}
