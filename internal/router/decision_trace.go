package router

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"time"

	"github.com/EffNine/conductor/internal/policy"
	"github.com/EffNine/conductor/internal/runtime"
)

// traceSchemaVersion is the schema version of DecisionTrace.
// It describes the STRUCTURE of the trace, NOT a runtime snapshot generation.
//
// Version history:
//   - 1: initial trace (decision id, schema ver, runtime hash, stages,
//     events, candidate list, candidate scores, winner, rejections).
//   - 2: canonical P3.14 contract — mode resolution fields
//     (RequestedMode/ResolvedMode/ModeSource/ModeDescription/ModeTraits),
//     policy fields (Intent, CapabilityRequirements, ContextRequirement,
//     EffectiveWeights, ModeBonuses), candidate score component
//     contributions (ModeBonus/ContextBonus/TelemetryPref), removal of the
//     never-populated CandidateList, and RuntimeHash now covering scoring-
//     relevant execution telemetry with collision-free field encoding.
//
// When fields are added or removed, bump this constant deliberately. It is
// unrelated to any runtime snapshot version.
const traceSchemaVersion = int64(2)

// TraceSchemaVersion returns the current DecisionTrace schema version. It is a
// documented constant: bumping it is a deliberate, reviewed act (see the
// version history on traceSchemaVersion). It describes the trace STRUCTURE
// only and never implies a runtime snapshot generation.
func TraceSchemaVersion() int64 { return traceSchemaVersion }

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

// RuntimeHash produces a deterministic hash of the scoring-relevant state of a
// runtime.RuntimeSnapshot. It is the identity mechanism linking a DecisionTrace
// to the exact snapshot a routing decision used.
//
// Contract:
//  1. Deterministic: the same snapshot always produces the same hash.
//  2. Order-independent: provider and model iteration order never affects the
//     hash (keys are sorted before hashing).
//  3. Sensitive to scoring-relevant state: provider state, latency, error
//     rate, and execution telemetry counters (provider- and model-level) all
//     change the hash. These are exactly the fields the scorer reads.
//  4. Not a snapshot generation/version: GlobalState, Timestamp,
//     LastHealthCheck, Capacity, and Tags are intentionally excluded because
//     they do not influence scoring; two snapshots with identical scoring
//     inputs hash identically even if those non-scoring fields differ.
//
// The trace must never acquire a second snapshot to compute this hash — it is
// computed from the same snapshot the decision used.
func RuntimeHash(snap runtime.RuntimeSnapshot) string {
	h := sha256.New()
	names := make([]string, 0, len(snap.Providers))
	for name := range snap.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		info := snap.Providers[name]
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(info.State))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(info.LatencyMs, 10)))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatFloat(info.ErrorRate, 'g', -1, 64)))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(info.ExecutionCount, 10)))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(info.ExecutionSuccessCount, 10)))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(info.ExecutionFailureCount, 10)))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(info.ToolCallSuccessCount, 10)))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(info.ToolCallFailureCount, 10)))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(info.RetryCount, 10)))
		h.Write([]byte{0})

		// Model-level telemetry, sorted for determinism.
		modelIDs := make([]string, 0, len(info.ModelExecutions))
		for id := range info.ModelExecutions {
			modelIDs = append(modelIDs, id)
		}
		sort.Strings(modelIDs)
		for _, id := range modelIDs {
			es := info.ModelExecutions[id]
			h.Write([]byte(id))
			h.Write([]byte{0})
			h.Write([]byte(strconv.FormatInt(es.ExecutionCount, 10)))
			h.Write([]byte{0})
			h.Write([]byte(strconv.FormatInt(es.ExecutionSuccessCount, 10)))
			h.Write([]byte{0})
			h.Write([]byte(strconv.FormatInt(es.ExecutionFailureCount, 10)))
			h.Write([]byte{0})
			h.Write([]byte(strconv.FormatInt(es.ToolCallSuccessCount, 10)))
			h.Write([]byte{0})
			h.Write([]byte(strconv.FormatInt(es.ToolCallFailureCount, 10)))
			h.Write([]byte{0})
			h.Write([]byte(strconv.FormatInt(es.RetryCount, 10)))
			h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// EventRecord is a single event in the trace timeline.
type EventRecord struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload,omitempty"`
}

// DecisionTrace is an immutable record of a single routing decision.
//
// Canonical contract (P3.14) — every trace answers:
//
//	request identity:  DecisionID, Timestamp
//	mode resolution:   RequestedMode, ResolvedMode, ModeSource
//	mode policy:       ModeDescription, ModeTraits
//	request intent:    Intent (TaskType, Confidence)
//	requirements:      CapabilityRequirements, ContextRequirement
//	candidate set:     CandidateScores (authoritative candidate identity)
//	candidate scores:  CandidateScores with per-component contributions
//	selected candidate: Winner
//	rejections:        RejectionReasons
//	runtime identity:  RuntimeHash
//	policy:            EffectiveWeights, ModeBonuses
//	execution result:  StageResults, Events
//
// Safety contract: a trace never contains prompts, API keys, authorization
// headers, provider secrets, or raw request bodies. The original request is
// intentionally not embedded.
type DecisionTrace struct {
	DecisionID             DecisionID                    `json:"decision_id"`
	TraceSchemaVer         int64                         `json:"trace_schema_ver"`
	Timestamp              time.Time                     `json:"timestamp"`
	RequestedMode          string                        `json:"requested_mode,omitempty"`
	RequestedModel         string                        `json:"requested_model,omitempty"` // original virtual/model ID before resolution
	ResolvedMode           Mode                          `json:"resolved_mode,omitempty"`
	ModeSource             string                        `json:"mode_source,omitempty"`
	ModeDescription        string                        `json:"mode_description,omitempty"`
	ModeTraits             []string                      `json:"mode_traits,omitempty"`
	Intent                 *policy.Intent                `json:"intent,omitempty"`
	CapabilityRequirements *policy.CapabilityRequirement `json:"capability_requirements,omitempty"`
	ContextRequirement     int                           `json:"context_requirement,omitempty"`
	EffectiveWeights       Weights                       `json:"effective_weights"`
	ModeBonuses            CapabilityBonuses             `json:"mode_bonuses"`
	RuntimeHash            string                        `json:"runtime_hash"`
	CandidateScores        []CandidateScore              `json:"candidate_scores,omitempty"`
	Winner                 *ResolvedRoute                `json:"winner,omitempty"`
	RejectionReasons       []RejectionReason             `json:"rejection_reasons,omitempty"`
	StageResults           []*StageResult                `json:"stage_results"`
	Events                 []EventRecord                 `json:"events"`
}

// DecisionTraceBuilder incrementally constructs a DecisionTrace.
type DecisionTraceBuilder struct {
	trace *DecisionTrace
}

// NewDecisionTraceBuilder creates a builder for a new trace. The snapshot is
// the exact snapshot the decision used — no second snapshot is acquired.
func NewDecisionTraceBuilder(dcID DecisionID, snap runtime.RuntimeSnapshot) *DecisionTraceBuilder {
	return &DecisionTraceBuilder{
		trace: &DecisionTrace{
			DecisionID:     dcID,
			TraceSchemaVer: traceSchemaVersion,
			RuntimeHash:    RuntimeHash(snap),
			StageResults:   make([]*StageResult, 0),
			Events:         make([]EventRecord, 0),
			Timestamp:      time.Now().UTC(),
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

// SetCandidateScores records the per-candidate factor scores and totals from
// the routing decision, so a trace can explain why a candidate won or lost
// (factor breakdown, mode/context/telemetry contributions, rejection reason,
// selected flag). CandidateScores is the authoritative candidate set: it
// carries both candidate identity and ranking.
func (b *DecisionTraceBuilder) SetCandidateScores(scores []CandidateScore) {
	b.trace.CandidateScores = scores
}

// SetWinner records the selected provider route.
func (b *DecisionTraceBuilder) SetWinner(w *ResolvedRoute) {
	b.trace.Winner = w
}

// SetRejectionReasons records all rejection reasons.
func (b *DecisionTraceBuilder) SetRejectionReasons(reasons []RejectionReason) {
	b.trace.RejectionReasons = reasons
}

// SetModeResolution records how the request mode was resolved.
func (b *DecisionTraceBuilder) SetModeResolution(requested string, mp *ModeProfile, source string) {
	b.trace.RequestedMode = requested
	b.trace.ModeSource = source
	if mp != nil {
		b.trace.ResolvedMode = mp.Mode
		b.trace.ModeDescription = mp.Description
		b.trace.ModeTraits = mp.Traits
	}
}

// SetRequestedModel records the original model ID from the request (virtual model or concrete model).
func (b *DecisionTraceBuilder) SetRequestedModel(model string) {
	b.trace.RequestedModel = model
}

// SetIntent records the resolved request intent (task type, confidence).
func (b *DecisionTraceBuilder) SetIntent(i *policy.Intent) {
	b.trace.Intent = i
}

// SetCapabilityRequirements records the resolved capability requirements.
func (b *DecisionTraceBuilder) SetCapabilityRequirements(cr *policy.CapabilityRequirement) {
	b.trace.CapabilityRequirements = cr
}

// SetContextRequirement records the estimated token budget a mode enforced.
func (b *DecisionTraceBuilder) SetContextRequirement(n int) {
	b.trace.ContextRequirement = n
}

// SetEffectiveWeights records the normalized weights actually used by
// selection for this decision (request-local, never a copy of global state).
func (b *DecisionTraceBuilder) SetEffectiveWeights(w Weights) {
	b.trace.EffectiveWeights = w
}

// SetModeBonuses records the mode capability bonuses applied during scoring.
func (b *DecisionTraceBuilder) SetModeBonuses(bonuses CapabilityBonuses) {
	b.trace.ModeBonuses = bonuses
}

// Build returns the completed immutable trace.
func (b *DecisionTraceBuilder) Build() *DecisionTrace {
	return b.trace
}
