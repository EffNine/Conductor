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

func openRecoveryTestDB(t *testing.T) *database.Database {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "recovery_test.db")
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

func insertPendingTask(t *testing.T, db *database.Database, id string) {
	t.Helper()
	tsk := &task.Task{
		ID:       id,
		Status:   task.StatusPending,
		Input:    "pending-task",
		MaxRetries: 3,
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

func insertRunningTaskWithLease(t *testing.T, db *database.Database, id string, leaseUntil time.Time) {
	t.Helper()
	tsk := &task.Task{
		ID:           id,
		Status:       task.StatusRunning,
		Input:        "running-task",
		MaxRetries:   3,
		ClaimedBy:    "worker-1",
		ClaimedAt:    &leaseUntil,
		LeaseUntil:   &leaseUntil,
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

func TestStartupRecovery_PendingToQueued(t *testing.T) {
	db := openRecoveryTestDB(t)
	store := task.NewSQLiteStore(db)
	id := uuid.New().String()
	insertPendingTask(t, db, id)

	n, err := store.RecoverPendingTasks()
	if err != nil {
		t.Fatalf("RecoverPendingTasks: %v", err)
	}
	if n != 1 {
		t.Errorf("n = %d, want 1", n)
	}

	got, err := store.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
}

func TestStartupRecovery_ExpiredLease(t *testing.T) {
	db := openRecoveryTestDB(t)
	store := task.NewSQLiteStore(db)
	id := uuid.New().String()
	expired := time.Now().UTC().Add(-time.Hour)
	insertRunningTaskWithLease(t, db, id, expired)

	n, err := store.ExpireStaleLeases()
	if err != nil {
		t.Fatalf("ExpireStaleLeases: %v", err)
	}
	if n != 1 {
		t.Errorf("n = %d, want 1", n)
	}

	got, err := store.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if got.ClaimedBy != "" {
		t.Errorf("claimed_by = %q, want empty", got.ClaimedBy)
	}
}

func TestStartupRecovery_ValidLeasePreserved(t *testing.T) {
	db := openRecoveryTestDB(t)
	store := task.NewSQLiteStore(db)
	id := uuid.New().String()
	valid := time.Now().UTC().Add(time.Hour)
	insertRunningTaskWithLease(t, db, id, valid)

	n, err := store.ExpireStaleLeases()
	if err != nil {
		t.Fatalf("ExpireStaleLeases: %v", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0 (lease is still valid)", n)
	}

	got, err := store.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != task.StatusRunning {
		t.Errorf("status = %q, want running", got.Status)
	}
	if got.ClaimedBy != "worker-1" {
		t.Errorf("claimed_by = %q, want worker-1", got.ClaimedBy)
	}
}

func TestStartupRecovery_Idempotent(t *testing.T) {
	db := openRecoveryTestDB(t)
	store := task.NewSQLiteStore(db)

	// Insert a pending task and an expired-lease running task.
	id1 := uuid.New().String()
	insertPendingTask(t, db, id1)
	id2 := uuid.New().String()
	expired := time.Now().UTC().Add(-time.Hour)
	insertRunningTaskWithLease(t, db, id2, expired)

	// First recovery run.
	n1, err := store.RecoverPendingTasks()
	if err != nil {
		t.Fatalf("first RecoverPendingTasks: %v", err)
	}
	e1, err := store.ExpireStaleLeases()
	if err != nil {
		t.Fatalf("first ExpireStaleLeases: %v", err)
	}

	// Second recovery run — should be no-ops.
	n2, err := store.RecoverPendingTasks()
	if err != nil {
		t.Fatalf("second RecoverPendingTasks: %v", err)
	}
	e2, err := store.ExpireStaleLeases()
	if err != nil {
		t.Fatalf("second ExpireStaleLeases: %v", err)
	}

	if n1 != 1 {
		t.Errorf("first pending recovery n = %d, want 1", n1)
	}
	if n2 != 0 {
		t.Errorf("second pending recovery n = %d, want 0 (idempotent)", n2)
	}
	if e1 != 1 {
		t.Errorf("first lease expiration n = %d, want 1", e1)
	}
	if e2 != 0 {
		t.Errorf("second lease expiration n = %d, want 0 (idempotent)", e2)
	}

	// Verify final states.
	got1, _ := store.GetTask(id1)
	if got1.Status != task.StatusQueued {
		t.Errorf("pending task status = %q, want queued", got1.Status)
	}
	got2, _ := store.GetTask(id2)
	if got2.Status != task.StatusQueued {
		t.Errorf("expired lease task status = %q, want queued", got2.Status)
	}
}

func TestStartupRecovery_TerminalUntouched(t *testing.T) {
	db := openRecoveryTestDB(t)
	store := task.NewSQLiteStore(db)

	// Completed task.
	idDone := uuid.New().String()
	tskDone := &task.Task{ID: idDone, Status: task.StatusCompleted, Input: "done"}
	if err := db.DB.Create(tskDone).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Cancelled task.
	idCancel := uuid.New().String()
	tskCancel := &task.Task{ID: idCancel, Status: task.StatusCancelled, Input: "cancel"}
	if err := db.DB.Create(tskCancel).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Failed task (with no retry budget).
	idFail := uuid.New().String()
	past := time.Now().UTC().Add(-time.Hour)
	tskFail := &task.Task{ID: idFail, Status: task.StatusFailed, Input: "fail", MaxRetries: 0, RetryCount: 0, NextRetryAt: &past}
	if err := db.DB.Create(tskFail).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	n, err := store.RecoverPendingTasks()
	if err != nil {
		t.Fatalf("RecoverPendingTasks: %v", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0 (no pending tasks)", n)
	}

	e, err := store.ExpireStaleLeases()
	if err != nil {
		t.Fatalf("ExpireStaleLeases: %v", err)
	}
	if e != 0 {
		t.Errorf("e = %d, want 0 (no expired leases)", e)
	}

	// Verify terminal tasks remain unchanged.
	for id, wantStatus := range map[string]task.Status{
		idDone:   task.StatusCompleted,
		idCancel: task.StatusCancelled,
		idFail:   task.StatusFailed,
	} {
		got, err := store.GetTask(id)
		if err != nil {
			t.Fatalf("GetTask %s: %v", id, err)
		}
		if got.Status != wantStatus {
			t.Errorf("task %s status = %q, want %q", id, got.Status, wantStatus)
		}
	}
}
