package router

import "context"

// TraceStore is the interface for persisting and retrieving decision traces.
// Implementations may use SQLite, in-memory storage, or remote backends.
type TraceStore interface {
	// Save persists a decision trace.
	Save(ctx context.Context, trace *DecisionTrace) error

	// Load retrieves a trace by decision ID.
	Load(ctx context.Context, id DecisionID) (*DecisionTrace, error)

	// Latest returns the most recent trace.
	Latest(ctx context.Context) (*DecisionTrace, error)

	// Search returns traces matching the given filters.
	Search(ctx context.Context, filters TraceFilters) ([]*DecisionTrace, error)
}

// TraceFilters constrains a Search query.
type TraceFilters struct {
	Provider string
	ModelID  string
	After    int64 // Unix nano timestamp
	Before   int64 // Unix nano timestamp
	Limit    int
	Offset   int
}
