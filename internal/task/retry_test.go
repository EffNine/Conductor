package task_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/task"
	"github.com/google/uuid"
)

func openRetryTestDB(t *testing.T) *database.Database {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "retry_test.db")
	db, err := database.Connect(&config.DatabaseConfig{
		Driver:       "sqlite",
		DSN:          dsn,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := task.MigrateTasks(db.DB); err != nil {
		t.Fatalf("MigrateTasks: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertFailedTask(t *testing.T, db *database.Database, id string, retryCount, maxRetries int, nextRetryAt time.Time) {
	t.Helper()
	tsk := &task.Task{
		ID:          id,
		Status:      task.StatusFailed,
		Input:       "retry-test",
		RetryCount:  retryCount,
		MaxRetries:  maxRetries,
		NextRetryAt: &nextRetryAt,
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

func TestReadyRetries_MaxRetriesZero(t *testing.T) {
	db := openRetryTestDB(t)
	store := task.NewSQLiteStore(db)
	id := uuid.New().String()
	past := time.Now().UTC().Add(-time.Hour)
	insertFailedTask(t, db, id, 0, 0, past)

	ids, err := store.ReadyRetries(10)
	if err != nil {
		t.Fatalf("ReadyRetries: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("len(ids) = %d, want 0 (max_retries=0 means no retries)", len(ids))
	}
}

func TestReadyRetries_RetryCountEqualsMax(t *testing.T) {
	db := openRetryTestDB(t)
	store := task.NewSQLiteStore(db)
	id := uuid.New().String()
	past := time.Now().UTC().Add(-time.Hour)
	insertFailedTask(t, db, id, 3, 3, past)

	ids, err := store.ReadyRetries(10)
	if err != nil {
		t.Fatalf("ReadyRetries: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("len(ids) = %d, want 0 (retry_count == max_retries)", len(ids))
	}
}

func TestReadyRetries_RetryCountBelowMax(t *testing.T) {
	db := openRetryTestDB(t)
	store := task.NewSQLiteStore(db)
	id := uuid.New().String()
	past := time.Now().UTC().Add(-time.Hour)
	insertFailedTask(t, db, id, 1, 3, past)

	ids, err := store.ReadyRetries(10)
	if err != nil {
		t.Fatalf("ReadyRetries: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("len(ids) = %d, want 1", len(ids))
	}
	if ids[0] != id {
		t.Errorf("id = %q, want %q", ids[0], id)
	}
}

func TestReadyRetries_ModifiedRetryLimit(t *testing.T) {
	db := openRetryTestDB(t)
	store := task.NewSQLiteStore(db)
	id := uuid.New().String()
	past := time.Now().UTC().Add(-time.Hour)

	// Create with max_retries=0.
	insertFailedTask(t, db, id, 0, 0, past)

	ids, err := store.ReadyRetries(10)
	if err != nil {
		t.Fatalf("ReadyRetries: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("initial: len(ids) = %d, want 0", len(ids))
	}

	// Increase max_retries to 3, keep retry_count at 0.
	_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("max_retries", 3).Error

	ids, err = store.ReadyRetries(10)
	if err != nil {
		t.Fatalf("ReadyRetries after update: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("after update: len(ids) = %d, want 1", len(ids))
	}
	if ids[0] != id {
		t.Errorf("id = %q, want %q", ids[0], id)
	}

	// Exhaust retries.
	_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("retry_count", 3).Error

	ids, err = store.ReadyRetries(10)
	if err != nil {
		t.Fatalf("ReadyRetries after exhaustion: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("after exhaustion: len(ids) = %d, want 0", len(ids))
	}
}

func TestReadyRetries_NextRetryAtFuture(t *testing.T) {
	db := openRetryTestDB(t)
	store := task.NewSQLiteStore(db)
	id := uuid.New().String()
	future := time.Now().UTC().Add(time.Hour)
	insertFailedTask(t, db, id, 0, 3, future)

	ids, err := store.ReadyRetries(10)
	if err != nil {
		t.Fatalf("ReadyRetries: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("len(ids) = %d, want 0 (next_retry_at is in the future)", len(ids))
	}
}

func TestReadyRetries_MixedStates(t *testing.T) {
	db := openRetryTestDB(t)
	store := task.NewSQLiteStore(db)
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)

	// Eligible: failed, past retry, budget remaining.
	id1 := uuid.New().String()
	insertFailedTask(t, db, id1, 0, 3, past)

	// Ineligible: max_retries=0.
	id2 := uuid.New().String()
	insertFailedTask(t, db, id2, 0, 0, past)

	// Ineligible: exhausted retries.
	id3 := uuid.New().String()
	insertFailedTask(t, db, id3, 3, 3, past)

	// Ineligible: future retry.
	id4 := uuid.New().String()
	insertFailedTask(t, db, id4, 0, 3, future)

	// Ineligible: not failed (queued).
	id5 := uuid.New().String()
	tsk5 := &task.Task{ID: id5, Status: task.StatusQueued, Input: "queued", MaxRetries: 3}
	if err := db.DB.Create(tsk5).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	ids, err := store.ReadyRetries(10)
	if err != nil {
		t.Fatalf("ReadyRetries: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("len(ids) = %d, want 1", len(ids))
	}
	if ids[0] != id1 {
		t.Errorf("id = %q, want %q", ids[0], id1)
	}
}
