package database

import (
	"context"
	"time"
)

// Request-attempt outcome values persisted in request_attempts.outcome.
const (
	AttemptOutcomeSuccess = "success"
	AttemptOutcomeFailed  = "failed"
	AttemptOutcomeSkipped = "skipped"
)

// RequestAttempt is one persisted execution attempt of a chat candidate
// chain (P4.4.3). Rows are written asynchronously off the request path and
// pruned by the retention job (P4.4.4).
type RequestAttempt struct {
	ID                uint      `gorm:"primaryKey"`
	CreatedAt         time.Time `gorm:"index"`
	RequestID         string    `gorm:"size:64;index"`
	CorrelationID     string    `gorm:"size:64;index"`
	VirtualModel      string    `gorm:"size:128"`
	Mode              string    `gorm:"size:32"`
	Provider          string    `gorm:"size:64;index"`
	ProviderModelID   string    `gorm:"size:191"`
	CandidateIndex    int
	AttemptIndex      int
	FailureClass      string `gorm:"size:32;index"`
	Outcome           string `gorm:"size:16"`
	SkipReason        string `gorm:"size:48"`
	HTTPStatus        int
	LatencyMS         int64
	RetryWaitMS       int64
	RetryAfterHonored bool
}

// AttemptRecord is the event-bus payload published by the handler sink and
// converted into a RequestAttempt row by the async consumer.
type AttemptRecord struct {
	RequestID         string
	CorrelationID     string
	VirtualModel      string
	Mode              string
	Provider          string
	ProviderModelID   string
	CandidateIndex    int
	AttemptIndex      int
	FailureClass      string
	Outcome           string
	SkipReason        string
	HTTPStatus        int
	LatencyMS         int64
	RetryWaitMS       int64
	RetryAfterHonored bool
}

func (r AttemptRecord) toRow(now time.Time) *RequestAttempt {
	return &RequestAttempt{
		CreatedAt:         now,
		RequestID:         r.RequestID,
		CorrelationID:     r.CorrelationID,
		VirtualModel:      r.VirtualModel,
		Mode:              r.Mode,
		Provider:          r.Provider,
		ProviderModelID:   r.ProviderModelID,
		CandidateIndex:    r.CandidateIndex,
		AttemptIndex:      r.AttemptIndex,
		FailureClass:      r.FailureClass,
		Outcome:           r.Outcome,
		SkipReason:        r.SkipReason,
		HTTPStatus:        r.HTTPStatus,
		LatencyMS:         r.LatencyMS,
		RetryWaitMS:       r.RetryWaitMS,
		RetryAfterHonored: r.RetryAfterHonored,
	}
}

// AttemptStore persists execution attempts into SQLite.
type AttemptStore struct {
	db *Database
}

// NewAttemptStore creates a store bound to the given database.
func NewAttemptStore(db *Database) *AttemptStore {
	return &AttemptStore{db: db}
}

// Save persists one attempt record.
func (s *AttemptStore) Save(ctx context.Context, rec AttemptRecord) error {
	return s.db.DB.WithContext(ctx).Create(rec.toRow(time.Now().UTC())).Error
}

// ListAttempts returns recent attempt rows, newest first (diagnostics/tests).
func (s *AttemptStore) ListAttempts(ctx context.Context, limit int) ([]RequestAttempt, error) {
	if limit <= 0 {
		limit = 100
	}
	rows := make([]RequestAttempt, 0, limit)
	err := s.db.DB.WithContext(ctx).
		Order("created_at DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// PruneBefore deletes attempt rows older than the cutoff in bounded
// batches, returning the total number of rows removed. Batching keeps each
// statement short so a large backlog cannot hold the SQLite writer for long.
func (s *AttemptStore) PruneBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	var total int64
	for {
		res := s.db.DB.WithContext(ctx).Exec(
			"DELETE FROM request_attempts WHERE id IN "+
				"(SELECT id FROM request_attempts WHERE created_at < ? LIMIT ?)",
			cutoff, batchSize)
		if res.Error != nil {
			return total, res.Error
		}
		if res.RowsAffected == 0 {
			return total, nil
		}
		total += res.RowsAffected
		if res.RowsAffected < int64(batchSize) {
			return total, nil
		}
	}
}
