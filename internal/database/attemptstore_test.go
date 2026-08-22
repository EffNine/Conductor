package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/config"
)

func newFileDB(t *testing.T) *Database {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "attempts.db")
	db, err := Connect(&config.DatabaseConfig{
		Driver:       "sqlite",
		DSN:          dsn,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestRequestAttemptMigration: request_attempts is created on fresh
// databases and remains healthy on re-migration of existing ones.
func TestRequestAttemptMigration(t *testing.T) {
	db := newFileDB(t)
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var count int64
	if err := db.DB.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='request_attempts'").Scan(&count).Error; err != nil {
		t.Fatalf("table check: %v", err)
	}
	if count != 1 {
		t.Fatalf("request_attempts table count = %d, want 1", count)
	}

	// Existing tables remain writable after the additive migration.
	rec := AttemptRecord{
		RequestID: "r-1", CorrelationID: "c-1",
		VirtualModel: "frontier", Mode: "auto",
		Provider: "openai", ProviderModelID: "gpt-4o",
		CandidateIndex: 0, AttemptIndex: 0,
		FailureClass: "rate_limited", Outcome: AttemptOutcomeSkipped,
		SkipReason: "circuit_breaker_open", HTTPStatus: 429,
	}
	store := NewAttemptStore(db)
	if err := store.Save(context.Background(), rec); err != nil {
		t.Fatalf("save on migrated schema: %v", err)
	}

	// Re-migration (startup on an existing database) is idempotent.
	if err := db.Migrate(); err != nil {
		t.Fatalf("re-Migrate: %v", err)
	}
	rows, err := store.ListAttempts(context.Background(), 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows after re-migrate: n=%d err=%v", len(rows), err)
	}
}

func TestAttemptStoreSaveAndList(t *testing.T) {
	db := newFileDB(t)
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewAttemptStore(db)
	ctx := context.Background()

	base := func(reqID string, outcome string) AttemptRecord {
		return AttemptRecord{
			RequestID: reqID, CorrelationID: "corr-" + reqID,
			VirtualModel: "coding", Mode: "coding",
			Provider: "groq", ProviderModelID: "llama-3.1-8b-instruct",
			CandidateIndex: 0, AttemptIndex: 0,
			Outcome: outcome, HTTPStatus: 200, LatencyMS: 42,
		}
	}
	require2 := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	require2(store.Save(ctx, base("r-old", AttemptOutcomeSuccess)), "save old")
	time.Sleep(5 * time.Millisecond) // distinct created_at ordering
	require2(store.Save(ctx, base("r-new", AttemptOutcomeFailed)), "save new")

	rows, err := store.ListAttempts(ctx, 10)
	require2(err, "list")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].RequestID != "r-new" {
		t.Fatalf("ordering wrong: newest=%s", rows[0].RequestID)
	}
	if rows[1].FailureClass != "" || rows[0].LatencyMS != 42 {
		t.Fatalf("field mapping drifted: %+v", rows[0])
	}
}
