package usage

import (
	"context"
	"sync"
	"time"

	"github.com/novexa/gateway/internal/database"
	"go.uber.org/zap"
)

// Record represents a usage record to be tracked
type Record struct {
	RequestID        string
	ModelID          string
	ProviderModelID  string
	Provider         string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Requests         int
	DurationMs       int64
	InputChars       int
	OutputChars      int
	LatencyMs        int64
	ActualCostUSD    *float64
	StatusCode       int
	IsStream         bool
	ErrorMessage     *string
	CreatedAt        time.Time
}

// Tracker handles usage tracking
type Tracker struct {
	db        *database.Database
	estimator *Estimator
	logger    *zap.Logger
	mu        sync.Mutex
	buffer    []*Record
}

// NewTracker creates a new usage tracker. estimator may be nil (cost left unknown).
func NewTracker(db *database.Database, estimator *Estimator, logger *zap.Logger) *Tracker {
	return &Tracker{
		db:        db,
		estimator: estimator,
		logger:    logger,
		buffer:    make([]*Record, 0, 100),
	}
}

// Record records a usage entry
func (t *Tracker) Record(r *Record) {
	if t == nil || t.db == nil {
		return
	}

	t.mu.Lock()
	t.buffer = append(t.buffer, r)
	t.mu.Unlock()

	t.flush()
}

func (t *Tracker) flush() {
	t.mu.Lock()
	if len(t.buffer) == 0 {
		t.mu.Unlock()
		return
	}

	batch := make([]*Record, len(t.buffer))
	copy(batch, t.buffer)
	t.buffer = t.buffer[:0]
	t.mu.Unlock()

	for _, r := range batch {
		durationMs := r.DurationMs
		if durationMs == 0 {
			durationMs = r.LatencyMs
		}

		record := &database.UsageRecord{
			ID:               r.RequestID,
			RequestID:        r.RequestID,
			ModelID:          r.ModelID,
			ProviderModelID:  r.ProviderModelID,
			Provider:         r.Provider,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			TotalTokens:      r.TotalTokens,
			Requests:         r.Requests,
			DurationMs:       durationMs,
			InputChars:       r.InputChars,
			OutputChars:      r.OutputChars,
			LatencyMs:        r.LatencyMs,
			StatusCode:       r.StatusCode,
			IsStream:         r.IsStream,
			ErrorMessage:     r.ErrorMessage,
			CreatedAt:        r.CreatedAt,
		}

		if t.estimator != nil {
			result, err := t.estimator.Estimate(context.Background(), CostInput{
				Provider:         r.Provider,
				ProviderModelID:  r.ProviderModelID,
				PromptTokens:     r.PromptTokens,
				CompletionTokens: r.CompletionTokens,
				Requests:         r.Requests,
				DurationMs:       durationMs,
				InputChars:       r.InputChars,
				OutputChars:      r.OutputChars,
				ActualCostUSD:    r.ActualCostUSD,
			})
			if err != nil {
				t.logger.Warn("cost estimate failed",
					zap.String("request_id", r.RequestID),
					zap.Error(err),
				)
			} else {
				record.EstimatedCostUSD = result.CostUSD
				record.CostSource = string(result.Source)
			}
		} else {
			unknown := string(CostSourceUnknown)
			record.CostSource = unknown
		}

		if err := t.db.DB.Create(record).Error; err != nil {
			t.logger.Error("failed to save usage record",
				zap.String("request_id", r.RequestID),
				zap.Error(err),
			)
		}
	}
}

// AggregateQuery filters usage aggregation.
type AggregateQuery struct {
	Since time.Time
	Until time.Time
}

// AggregateSummary is totals plus breakdowns.
type AggregateSummary struct {
	Total      AggregateBucket
	ByProvider map[string]AggregateBucket
	ByModel    map[string]AggregateBucket
}

// AggregateBucket holds summed usage and optional cost.
type AggregateBucket struct {
	Requests         int64
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	DurationMs       int64
	InputChars       int64
	OutputChars      int64
	CostUSD          *float64
}

// Aggregate returns usage totals and per-provider/per-model breakdowns.
func (t *Tracker) Aggregate(q AggregateQuery) (*AggregateSummary, error) {
	if t == nil || t.db == nil {
		return &AggregateSummary{
			ByProvider: map[string]AggregateBucket{},
			ByModel:    map[string]AggregateBucket{},
		}, nil
	}

	query := t.db.DB.Model(&database.UsageRecord{})
	if !q.Since.IsZero() {
		query = query.Where("created_at >= ?", q.Since)
	}
	if !q.Until.IsZero() {
		query = query.Where("created_at < ?", q.Until)
	}

	var rows []database.UsageRecord
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}

	summary := &AggregateSummary{
		ByProvider: map[string]AggregateBucket{},
		ByModel:    map[string]AggregateBucket{},
	}
	for _, row := range rows {
		summary.Total = addBucket(summary.Total, row)
		summary.ByProvider[row.Provider] = addBucket(summary.ByProvider[row.Provider], row)
		summary.ByModel[row.ModelID] = addBucket(summary.ByModel[row.ModelID], row)
	}
	return summary, nil
}

func addBucket(b AggregateBucket, row database.UsageRecord) AggregateBucket {
	b.Requests += int64(row.Requests)
	if row.Requests == 0 {
		b.Requests++
	}
	b.PromptTokens += int64(row.PromptTokens)
	b.CompletionTokens += int64(row.CompletionTokens)
	b.TotalTokens += int64(row.TotalTokens)
	b.DurationMs += row.DurationMs
	b.InputChars += int64(row.InputChars)
	b.OutputChars += int64(row.OutputChars)
	if row.EstimatedCostUSD != nil {
		if b.CostUSD == nil {
			v := *row.EstimatedCostUSD
			b.CostUSD = &v
		} else {
			*b.CostUSD += *row.EstimatedCostUSD
		}
	}
	return b
}
