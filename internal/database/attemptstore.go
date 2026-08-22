package database

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Request-attempt outcome values persisted in request_attempts.outcome.
const (
	AttemptOutcomeSuccess = "success"
	AttemptOutcomeFailed  = "failed"
	AttemptOutcomeSkipped = "skipped"
)

// RequestAttempt is one persisted execution attempt of a chat candidate
// chain (P4.4.3). Rows are written asynchronously off the request path and
// pruned by the retention job (P4.4.4). Serialization tags back the
// read-only analytics API (P4.5).
type RequestAttempt struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	CreatedAt         time.Time `gorm:"index" json:"created_at"`
	RequestID         string    `gorm:"size:64;index" json:"request_id"`
	CorrelationID     string    `gorm:"size:64;index" json:"correlation_id"`
	VirtualModel      string    `gorm:"size:128" json:"virtual_model"`
	Mode              string    `gorm:"size:32" json:"mode"`
	Provider          string    `gorm:"size:64;index" json:"provider"`
	ProviderModelID   string    `gorm:"size:191" json:"provider_model_id"`
	CandidateIndex    int       `json:"candidate_index"`
	AttemptIndex      int       `json:"attempt_index"`
	FailureClass      string    `gorm:"size:32;index" json:"failure_class"`
	Outcome           string    `gorm:"size:16" json:"outcome"`
	SkipReason        string    `gorm:"size:48" json:"skip_reason"`
	HTTPStatus        int       `json:"http_status"`
	LatencyMS         int64     `json:"latency_ms"`
	RetryWaitMS       int64     `json:"retry_wait_ms"`
	RetryAfterHonored bool      `json:"retry_after_honored"`
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

// FailureFilter selects non-success attempt rows for the analytics API
// (P4.5). Zero fields are ignored.
type FailureFilter struct {
	Class    string    // exact failure_class match
	Provider string    // exact provider match
	Model    string    // exact virtual_model match
	Since    time.Time // inclusive lower bound on created_at; zero = unbounded
	Limit    int       // default 50, max 200
	Offset   int
}

func (s *AttemptStore) failuresBaseQuery(ctx context.Context, f FailureFilter) *gorm.DB {
	q := s.db.DB.WithContext(ctx).
		Model(&RequestAttempt{}).
		Where("outcome <> ?", AttemptOutcomeSuccess)
	if f.Class != "" {
		q = q.Where("failure_class = ?", f.Class)
	}
	if f.Provider != "" {
		q = q.Where("provider = ?", f.Provider)
	}
	if f.Model != "" {
		q = q.Where("virtual_model = ?", f.Model)
	}
	if !f.Since.IsZero() {
		q = q.Where("created_at >= ?", f.Since)
	}
	return q
}

// ListFailures returns a page of non-success attempts, newest first, plus
// the total count of matching rows (independent of pagination).
func (s *AttemptStore) ListFailures(ctx context.Context, f FailureFilter) ([]RequestAttempt, int64, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	var total int64
	if err := s.failuresBaseQuery(ctx, f).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	rows := make([]RequestAttempt, 0, limit)
	err := s.failuresBaseQuery(ctx, f).
		Order("created_at DESC").
		Limit(limit).
		Offset(f.Offset).
		Find(&rows).Error
	return rows, total, err
}

// FailureBucket aggregates failures into fixed-width time buckets.
type FailureBucket struct {
	BucketStart time.Time `json:"bucket_start"`
	Count       int64     `json:"count"`
}

// FailureSummary is the aggregation payload behind GET /api/failures/summary.
type FailureSummary struct {
	Total      int64
	ByProvider map[string]int64
	ByClass    map[string]int64
	Buckets    []FailureBucket
}

type providerCountRow struct {
	Provider string
	N        int64
}

type classCountRow struct {
	FailureClass string
	N            int64
}

type bucketCountRow struct {
	Bucket int64 // epoch seconds of bucket start
	N      int64
}

// FailureSummary aggregates non-success attempts inside the window:
// totals, per-provider and per-class breakdowns, and fixed-width time
// buckets (the window is split into at most 24 buckets; minimum one minute
// wide). since zero means all history.
func (s *AttemptStore) FailureSummary(ctx context.Context, since time.Time, bucket time.Duration) (*FailureSummary, error) {
	out := &FailureSummary{
		ByProvider: map[string]int64{},
		ByClass:    map[string]int64{},
		Buckets:    []FailureBucket{},
	}
	if bucket <= 0 {
		bucket = time.Hour
	}
	bucketSecs := int64(bucket / time.Second)
	if bucketSecs < 60 {
		bucketSecs = 60
	}

	base := func() *gorm.DB {
		q := s.db.DB.WithContext(ctx).Model(&RequestAttempt{}).
			Where("outcome <> ?", AttemptOutcomeSuccess)
		if !since.IsZero() {
			q = q.Where("created_at >= ?", since)
		}
		return q
	}

	var providers []providerCountRow
	if err := base().Select("provider, count(*) as n").Group("provider").Scan(&providers).Error; err != nil {
		return nil, err
	}
	for _, p := range providers {
		out.ByProvider[p.Provider] = p.N
		out.Total += p.N
	}

	var classes []classCountRow
	if err := base().Select("failure_class, count(*) as n").Group("failure_class").Scan(&classes).Error; err != nil {
		return nil, err
	}
	for _, cRow := range classes {
		out.ByClass[cRow.FailureClass] = cRow.N
	}

	var buckets []bucketCountRow
	if err := base().
		Select("CAST(strftime('%s', created_at) AS INTEGER) / ? * ? AS bucket, count(*) as n",
			bucketSecs, bucketSecs).
		Group("bucket").Order("bucket ASC").Scan(&buckets).Error; err != nil {
		return nil, err
	}
	for _, b := range buckets {
		out.Buckets = append(out.Buckets, FailureBucket{
			BucketStart: time.Unix(b.Bucket, 0).UTC(),
			Count:       b.N,
		})
	}
	return out, nil
}
