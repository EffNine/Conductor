package shadow

import (
	"context"
	"strings"
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
	if err := s.db.AutoMigrate(&ShadowObservation{}); err != nil {
		return err
	}
	return s.migrateLegacy()
}

// migrateLegacy upgrades a pre-C3A shadow_observations table (which lacks
// the correlation_id column) by adding it. GORM AutoMigrate does not add
// columns to pre-existing tables; the index is created best-effort since
// the backfill lookup tolerates a table without the index.
func (s *SQLiteStore) migrateLegacy() error {
	var count int
	if err := s.db.Raw("SELECT COUNT(*) FROM pragma_table_info('shadow_observations') WHERE name = 'correlation_id'").Scan(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if err := s.db.Exec("ALTER TABLE shadow_observations ADD COLUMN correlation_id TEXT").Error; err != nil {
		return err
	}
	// Best-effort index; CREATE INDEX IF NOT EXISTS is SQLite-only but the
	// store is SQLite-backed by design.
	_ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_shadow_observations_correlation ON shadow_observations (correlation_id)").Error
	return nil
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

// UpdateAdvisory persists the JEV advisory fields of an existing row,
// leaving outcome/latency untouched (commutative with backfill).
func (s *SQLiteStore) UpdateAdvisory(ctx context.Context, obs *ShadowObservation) error {
	if obs == nil || obs.DecisionID == "" {
		return nil
	}
	// Build the advisory column set from the model tags, filtering out
	// columns that do not exist on the live table (legacy tables created
	// before the advisory fields were introduced may lack some or all).
	exists := make(map[string]bool)
	var cols []struct {
		Name string
	}
	if err := s.db.WithContext(ctx).Raw("PRAGMA table_info(shadow_observations)").Scan(&cols).Error; err != nil {
		return err
	}
	for _, c := range cols {
		exists[c.Name] = true
	}

	updates := map[string]any{
		"advisory_provider":      obs.AdvisoryProvider,
		"advisory_confidence":    obs.AdvisoryConfidence,
		"advisory_probabilities": obs.AdvisoryProbabilities,
		"jev_model":              obs.JEVModel,
		"jev_duration_ms":        obs.JEVDurationMs,
		"jev_input_tokens":       obs.JEVInputTokens,
		"jev_output_tokens":      obs.JEVOutputTokens,
		"advisory_status":        obs.AdvisoryStatus,
	}
	filtered := make(map[string]any, len(updates))
	for col, val := range updates {
		if exists[col] {
			filtered[col] = val
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&ShadowObservation{}).
		Where("decision_id = ?", obs.DecisionID).
		Updates(filtered).Error
}

// BackfillOutcome updates the outcome and provider latency for previously
// saved observations. The correlation id is the primary match key; a row
// without a recorded correlation falls back to the decision id (provenance
// match). Returns the number of rows matched.
func (s *SQLiteStore) BackfillOutcome(ctx context.Context, correlationID, decisionID, outcome string, latencyMs int64) (int, error) {
	if correlationID == "" && decisionID == "" {
		return 0, nil
	}
	conds := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if correlationID != "" {
		conds = append(conds, "correlation_id = ?")
		args = append(args, correlationID)
	}
	if decisionID != "" {
		conds = append(conds, "decision_id = ?")
		args = append(args, decisionID)
	}
	args = append(args, outcome, latencyMs)
	res := s.db.WithContext(ctx).Model(&ShadowObservation{}).
		Where("("+strings.Join(conds, " OR ")+")", args...).
		Updates(map[string]any{
			"outcome":             outcome,
			"provider_latency_ms": latencyMs,
		})
	if res.Error != nil {
		return 0, res.Error
	}
	return int(res.RowsAffected), nil
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
