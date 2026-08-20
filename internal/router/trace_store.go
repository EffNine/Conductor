package router

import (
	"context"
	"errors"
	"time"
)

// ErrTraceNotFound is returned by TraceStore.Get when no persisted trace
// exists for the given decision ID.
var ErrTraceNotFound = errors.New("trace not found")

// TraceStore is the persistence contract for routing decision traces.
// It is intentionally decoupled from routing: the DecisionPipeline only
// publishes the final DecisionTrace on the event bus, and a persistence
// consumer (the TraceStore implementation) owns Save. Routing code never
// depends on SQL or on this interface's implementation details.
//
// Implementations may use SQLite, in-memory storage, or remote backends.
// The storage layer must not know about Fiber, HTTP, provider execution, or
// routing selection logic — it only persists and retrieves traces.
type TraceStore interface {
	// Save persists a completed decision trace. The trace is the canonical
	// DecisionTrace contract (P3.14): deterministic serialization, no
	// secrets, no prompts, no raw request bodies.
	Save(ctx context.Context, trace *DecisionTrace) error

	// Get retrieves a trace by decision ID. Returns ErrTraceNotFound when no
	// trace exists for the ID.
	Get(ctx context.Context, decisionID DecisionID) (*DecisionTrace, error)

	// List returns queryable summaries of persisted traces, most recent
	// first, matching the given filter. Empty filter fields are ignored.
	// Limit defaults to 100 and is capped at 1000; negative offsets are
	// treated as zero.
	List(ctx context.Context, filter TraceFilter) ([]DecisionTraceSummary, error)
}

// TraceFilter constrains a TraceStore.List query. Only the minimum filter
// set needed for future dashboard work (recent decisions, by mode/provider/
// model, failed decisions, runtime-hash correlation, time range) is
// supported — no arbitrary query languages.
type TraceFilter struct {
	Mode           string    // exact match on resolved_mode
	Provider       string    // exact match on selected_provider
	Model          string    // exact match on selected_model
	RequestedModel string    // exact match on requested_model (virtual model ID)
	RuntimeHash    string    // exact match on runtime_hash
	Outcome        string    // exact match on outcome (selected|rejected|failed)
	From           time.Time // inclusive lower bound on decision timestamp
	To             time.Time // inclusive upper bound on decision timestamp
	Limit          int       // max rows; 0 or negative uses the default (100)
	Offset         int       // skip N rows; negative treated as 0
}

// DecisionTraceSummary is the queryable column view of a persisted trace.
// It carries the queryable dimensions from the routing_traces schema so a
// List can be served without deserializing the canonical JSON payload. The
// full DecisionTrace remains available via Get.
type DecisionTraceSummary struct {
	DecisionID       DecisionID
	Timestamp        time.Time
	SchemaVersion    int64
	RequestedMode    string
	ResolvedMode     string
	ModeSource       string
	TaskType         string
	SelectedProvider string
	SelectedModel    string
	RequestedModel   string // original virtual/model ID before resolution (e.g. "frontier", "auto")
	RuntimeHash      string
	SelectedScore    float64
	CandidateCount   int
	Outcome          string
	CreatedAt        time.Time
}
