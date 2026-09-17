package shadow

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SQLiteStore is the GORM-backed Store implementation. It owns the
// shadow_observations table and satisfies the persistence isolation
// contract: save errors are returned to the caller (the advisor), which logs
// them and never fails routing.
type SQLiteStore struct {
	db *gorm.DB
}

// NewSQLiteStore creates a store over the shared GORM database. The
// shadow_observations table must exist (call Migrate, or register the model
// with the gateway's own migration pass).
func NewSQLiteStore(db *gorm.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// Migrate creates the shadow_observations table. Idempotent.
func (s *SQLiteStore) Migrate() error {
	return s.db.AutoMigrate(&ShadowObservation{})
}

// Save persists one observation. Idempotent on decision_id: re-saving an
// existing decision is a no-op so outcome backfill does not duplicate rows.
func (s *SQLiteStore) Save(ctx context.Context, obs *ShadowObservation) error {
	if obs == nil || obs.DecisionID == "" {
		return nil
	}
	if obs.Outcome == "" {
		obs.Outcome = "unknown"
	}
	if obs.CreatedAt.IsZero() {
		obs.CreatedAt = time.Now().UTC()
	}
	return s.db.WithContext(ctx).Clauses(
		clause.OnConflict{
			Columns:   []clause.Column{{Name: "decision_id"}},
			DoNothing: true,
		},
	).Create(obs).Error
}

// BackfillOutcome updates the outcome and provider latency for a previously
// saved observation. Returns false when no row matched (e.g. pruned or never
// saved).
func (s *SQLiteStore) BackfillOutcome(ctx context.Context, decisionID, outcome string, latencyMs int64) (bool, error) {
	if decisionID == "" {
		return false, nil
	}
	res := s.db.WithContext(ctx).Model(&ShadowObservation{}).
		Where("decision_id = ?", decisionID).
		Updates(map[string]any{
			"outcome":             outcome,
			"provider_latency_ms": latencyMs,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// PruneBefore deletes observation rows older than the cutoff in bounded
// batches. Returns the number of rows removed.
func (s *SQLiteStore) PruneBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	var total int64
	for {
		res := s.db.WithContext(ctx).Exec(
			"DELETE FROM shadow_observations WHERE id IN ("+
				"SELECT id FROM shadow_observations WHERE created_at < ? LIMIT ?"+
				")",
			cutoff, batchSize,
		)
		if res.Error != nil {
			return total, res.Error
		}
		if res.RowsAffected == 0 {
			break
		}
		total += res.RowsAffected
	}
	return total, nil
}

// List returns the most recent observations (diagnostics/tests), newest first.
func (s *SQLiteStore) List(ctx context.Context, limit int) ([]ShadowObservation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows := make([]ShadowObservation, 0, limit)
	err := s.db.WithContext(ctx).
		Order("created_at DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}
