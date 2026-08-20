package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/router"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Trace outcome values persisted in routing_traces.outcome. They describe the
// ROUTING decision outcome only — provider execution outcome is out of scope
// for trace persistence (see the P3.15 report, "Execution Outcome Boundary").
const (
	TraceOutcomeSelected = "selected" // a candidate was selected
	TraceOutcomeRejected = "rejected" // decision completed, no candidate selected
	TraceOutcomeFailed   = "failed"   // a pipeline stage failed before selection
)

// RoutingTrace is the persisted, queryable representation of a routing
// decision trace. The queryable dimensions (mode, provider, model, runtime
// hash, outcome, timestamps, score, candidate count) live in dedicated
// columns; PayloadJSON retains the complete canonical DecisionTrace payload
// (P3.14 contract) as the single source of truth for detailed inspection.
//
// No nested trace field is duplicated into columns — the JSON payload remains
// canonical; the columns exist only for efficient querying.
type RoutingTrace struct {
	DecisionID       string    `gorm:"primaryKey;type:text" json:"decision_id"`
	Timestamp        time.Time `gorm:"index:idx_routing_traces_timestamp;index:idx_routing_traces_timestamp_mode,priority:1;index:idx_routing_traces_timestamp_provider,priority:1" json:"timestamp"`
	SchemaVersion    int64     `json:"schema_version"`
	RequestedMode    string    `gorm:"type:text" json:"requested_mode"`
	ResolvedMode     string    `gorm:"type:text;index:idx_routing_traces_timestamp_mode,priority:2" json:"resolved_mode"`
	ModeSource       string    `gorm:"type:text" json:"mode_source"`
	TaskType         string    `gorm:"type:text" json:"task_type"`
	SelectedProvider string    `gorm:"type:text;index:idx_routing_traces_timestamp_provider,priority:2" json:"selected_provider"`
	SelectedModel    string    `gorm:"type:text;index:idx_routing_traces_selected_model" json:"selected_model"`
	RequestedModel   string    `gorm:"type:text;index:idx_routing_traces_requested_model" json:"requested_model"`
	RuntimeHash      string    `gorm:"type:text;index:idx_routing_traces_runtime_hash" json:"runtime_hash"`
	SelectedScore    float64   `json:"selected_score"`
	CandidateCount   int       `json:"candidate_count"`
	Outcome          string    `gorm:"type:text" json:"outcome"`
	PayloadJSON      string    `gorm:"type:text" json:"-"`
	CreatedAt        time.Time `json:"created_at"`
}

// SQLiteTraceStore is the SQLite-backed router.TraceStore. It owns SQL only
// here — no routing code depends on SQL.
type SQLiteTraceStore struct {
	db *gorm.DB
}

// NewSQLiteTraceStore creates a trace store over the shared database. The
// routing_traces table must exist (see Database.Migrate).
func NewSQLiteTraceStore(db *Database) *SQLiteTraceStore {
	return &SQLiteTraceStore{db: db.DB}
}

// Save persists a completed decision trace. Serialization uses the canonical
// DecisionTrace JSON (deterministic, secret-free by contract). Saves are
// idempotent: re-saving an existing decision ID is a no-op. Errors are
// returned to the caller (a persistence consumer), which must never fail the
// routing request because of them.
func (s *SQLiteTraceStore) Save(ctx context.Context, trace *router.DecisionTrace) error {
	if trace == nil {
		return errors.New("trace store: nil trace")
	}
	if trace.DecisionID == "" {
		return errors.New("trace store: empty decision id")
	}
	payload, err := json.Marshal(trace)
	if err != nil {
		return fmt.Errorf("trace store: serialize trace %s: %w", trace.DecisionID, err)
	}
	row := traceToRow(trace, string(payload))
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "decision_id"}},
		DoNothing: true,
	}).Create(&row).Error
}

// Get retrieves a trace by decision ID, returning router.ErrTraceNotFound
// when no trace exists for the ID.
func (s *SQLiteTraceStore) Get(ctx context.Context, decisionID router.DecisionID) (*router.DecisionTrace, error) {
	var row RoutingTrace
	err := s.db.WithContext(ctx).First(&row, "decision_id = ?", string(decisionID)).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, router.ErrTraceNotFound
		}
		return nil, err
	}
	return rowToTrace(&row)
}

// List returns queryable summaries of persisted traces, most recent first
// (timestamp DESC, decision_id DESC as a deterministic tiebreaker), matching
// the given filter. Empty filter fields are ignored. All filtering uses
// parameterized queries — no string concatenation with user-controlled
// values.
func (s *SQLiteTraceStore) List(ctx context.Context, filter router.TraceFilter) ([]router.DecisionTraceSummary, error) {
	q := s.db.WithContext(ctx).Model(&RoutingTrace{})
	if filter.Mode != "" {
		q = q.Where("resolved_mode = ?", filter.Mode)
	}
	if filter.Provider != "" {
		q = q.Where("selected_provider = ?", filter.Provider)
	}
	if filter.Model != "" {
		q = q.Where("selected_model = ?", filter.Model)
	}
	if filter.RequestedModel != "" {
		q = q.Where("requested_model = ?", filter.RequestedModel)
	}
	if filter.RuntimeHash != "" {
		q = q.Where("runtime_hash = ?", filter.RuntimeHash)
	}
	if filter.Outcome != "" {
		q = q.Where("outcome = ?", filter.Outcome)
	}
	if !filter.From.IsZero() {
		q = q.Where("timestamp >= ?", filter.From.UTC())
	}
	if !filter.To.IsZero() {
		q = q.Where("timestamp <= ?", filter.To.UTC())
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	var rows []RoutingTrace
	if err := q.Order("timestamp DESC, decision_id DESC").
		Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, err
	}

	summaries := make([]router.DecisionTraceSummary, 0, len(rows))
	for i := range rows {
		summaries = append(summaries, rowToSummary(&rows[i]))
	}
	return summaries, nil
}

// traceOutcome derives the routing decision outcome from the canonical trace.
// The trace is the source of truth: a failed pipeline stage means "failed"; a
// trace with a winner means "selected"; otherwise the decision completed
// without a selection ("rejected").
func traceOutcome(trace *router.DecisionTrace) string {
	for _, sr := range trace.StageResults {
		if sr != nil && sr.Status == router.StageStatusFailed {
			return TraceOutcomeFailed
		}
	}
	if trace.Winner != nil {
		return TraceOutcomeSelected
	}
	return TraceOutcomeRejected
}

// selectedScore returns the TotalScore of the selected candidate, or 0 when
// no candidate was selected.
func selectedScore(trace *router.DecisionTrace) float64 {
	for _, cs := range trace.CandidateScores {
		if cs.Selected {
			return cs.TotalScore
		}
	}
	return 0
}

// traceToRow maps a canonical DecisionTrace onto the queryable row.
func traceToRow(trace *router.DecisionTrace, payload string) RoutingTrace {
	var taskType string
	if trace.Intent != nil {
		taskType = string(trace.Intent.TaskType)
	}
	var provider, model, requestedModel string
	if trace.Winner != nil {
		provider = trace.Winner.ProviderName
		model = trace.Winner.ProviderModelID  // Use concrete model for selected_model
		requestedModel = trace.Winner.ModelID // Preserve original requested model
	}
	// Fall back to trace.RequestedModel if winner is not available
	if requestedModel == "" && trace.RequestedModel != "" {
		requestedModel = trace.RequestedModel
	}
	now := time.Now().UTC()
	return RoutingTrace{
		DecisionID:       string(trace.DecisionID),
		Timestamp:        trace.Timestamp.UTC(),
		SchemaVersion:    trace.TraceSchemaVer,
		RequestedMode:    trace.RequestedMode,
		ResolvedMode:     string(trace.ResolvedMode),
		ModeSource:       trace.ModeSource,
		TaskType:         taskType,
		SelectedProvider: provider,
		SelectedModel:    model,
		RequestedModel:   requestedModel,
		RuntimeHash:      trace.RuntimeHash,
		SelectedScore:    selectedScore(trace),
		CandidateCount:   len(trace.CandidateScores),
		Outcome:          traceOutcome(trace),
		PayloadJSON:      payload,
		CreatedAt:        now,
	}
}

// rowToTrace deserializes the canonical DecisionTrace payload from a row.
func rowToTrace(row *RoutingTrace) (*router.DecisionTrace, error) {
	var trace router.DecisionTrace
	if err := json.Unmarshal([]byte(row.PayloadJSON), &trace); err != nil {
		return nil, fmt.Errorf("trace store: deserialize trace %s: %w", row.DecisionID, err)
	}
	return &trace, nil
}

// rowToSummary maps a row onto the queryable summary view.
func rowToSummary(row *RoutingTrace) router.DecisionTraceSummary {
	return router.DecisionTraceSummary{
		DecisionID:       router.DecisionID(row.DecisionID),
		Timestamp:        row.Timestamp,
		SchemaVersion:    row.SchemaVersion,
		RequestedMode:    row.RequestedMode,
		ResolvedMode:     row.ResolvedMode,
		ModeSource:       row.ModeSource,
		TaskType:         row.TaskType,
		SelectedProvider: row.SelectedProvider,
		SelectedModel:    row.SelectedModel,
		RequestedModel:   row.RequestedModel,
		RuntimeHash:      row.RuntimeHash,
		SelectedScore:    row.SelectedScore,
		CandidateCount:   row.CandidateCount,
		Outcome:          row.Outcome,
		CreatedAt:        row.CreatedAt,
	}
}
