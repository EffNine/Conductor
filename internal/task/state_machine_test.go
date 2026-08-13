package task_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/task"
	"github.com/google/uuid"
)

func openStateMachineDB(t *testing.T) *database.Database {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "state_machine.db")
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

func newStateMachineStore(t *testing.T, db *database.Database) task.Store {
	t.Helper()
	return task.NewSQLiteStore(db)
}

func insertRunningTask(t *testing.T, store task.Store, input string) string {
	t.Helper()
	id := uuid.New().String()
	// Create task directly as queued (same pattern as worker tests) so ClaimTask can transition it.
	tsk := &task.Task{
		ID:         id,
		Status:     task.StatusQueued,
		Input:      input,
		MaxRetries: 3,
	}
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Claim transitions queued → running and sets the lease.
	if _, err := store.ClaimTask("w1", 5*time.Minute); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	return id
}

func insertCompletedTask(t *testing.T, store task.Store, input string) string {
	t.Helper()
	id := insertRunningTask(t, store, input)
	if err := store.UpdateStatus(id, task.StatusCompleted); err != nil {
		t.Fatalf("UpdateStatus to completed: %v", err)
	}
	return id
}

func insertCancelledTask(t *testing.T, store task.Store, input string) string {
	t.Helper()
	id := insertRunningTask(t, store, input)
	if err := store.UpdateStatus(id, task.StatusCancelled); err != nil {
		t.Fatalf("UpdateStatus to cancelled: %v", err)
	}
	return id
}

// ── FailTask terminal-state rejection ────────────────────────────────────────

func TestFailTask_RejectsCompleted(t *testing.T) {
	db := openStateMachineDB(t)
	store := newStateMachineStore(t, db)
	id := insertCompletedTask(t, store, "done")

	err := store.FailTask(id, "too late")
	if err == nil {
		t.Fatal("expected error failing completed task")
	}
	if !strings.Contains(err.Error(), "completed") {
		t.Errorf("error = %q, want to contain 'completed'", err.Error())
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed (unchanged)", got.Status)
	}
}

func TestFailTask_RejectsCancelled(t *testing.T) {
	db := openStateMachineDB(t)
	store := newStateMachineStore(t, db)
	id := insertCancelledTask(t, store, "cancelled")

	err := store.FailTask(id, "too late")
	if err == nil {
		t.Fatal("expected error failing cancelled task")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error = %q, want to contain 'cancelled'", err.Error())
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCancelled {
		t.Errorf("status = %q, want cancelled (unchanged)", got.Status)
	}
}

func TestFailTask_AcceptsRunning(t *testing.T) {
	db := openStateMachineDB(t)
	store := newStateMachineStore(t, db)
	id := insertRunningTask(t, store, "fail-me")

	err := store.FailTask(id, "something went wrong")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.Error == nil || *got.Error != "something went wrong" {
		t.Errorf("error = %v, want 'something went wrong'", got.Error)
	}
}

// ── MakeRetryable terminal-state rejection ────────────────────────────────────

func TestMakeRetryable_RejectsCompleted(t *testing.T) {
	db := openStateMachineDB(t)
	store := newStateMachineStore(t, db)
	id := insertCompletedTask(t, store, "done")

	count, err := store.MakeRetryable(id, 5*time.Second)
	if err == nil {
		t.Fatal("expected error retrying completed task")
	}
	if !strings.Contains(err.Error(), "completed") {
		t.Errorf("error = %q, want to contain 'completed'", err.Error())
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed (unchanged)", got.Status)
	}
}

func TestMakeRetryable_RejectsCancelled(t *testing.T) {
	db := openStateMachineDB(t)
	store := newStateMachineStore(t, db)
	id := insertCancelledTask(t, store, "cancelled")

	count, err := store.MakeRetryable(id, 5*time.Second)
	if err == nil {
		t.Fatal("expected error retrying cancelled task")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error = %q, want to contain 'cancelled'", err.Error())
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCancelled {
		t.Errorf("status = %q, want cancelled (unchanged)", got.Status)
	}
}

func TestMakeRetryable_AcceptsFailed(t *testing.T) {
	db := openStateMachineDB(t)
	store := newStateMachineStore(t, db)
	id := insertRunningTask(t, store, "retry-me")

	// Transition to failed first.
	if err := store.UpdateStatus(id, task.StatusFailed); err != nil {
		t.Fatalf("UpdateStatus to failed: %v", err)
	}

	count, err := store.MakeRetryable(id, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.RetryCount != 1 {
		t.Errorf("retry_count = %d, want 1", got.RetryCount)
	}
	if got.NextRetryAt == nil {
		t.Fatal("next_retry_at is nil")
	}
}

// ── ReleaseLease from terminal states ─────────────────────────────────────────

func TestReleaseLease_FromCompleted(t *testing.T) {
	db := openStateMachineDB(t)
	store := newStateMachineStore(t, db)
	id := insertRunningTask(t, store, "release-test")

	// Complete the task while it still has a lease.
	if err := store.UpdateStatus(id, task.StatusCompleted); err != nil {
		t.Fatalf("UpdateStatus to completed: %v", err)
	}

	// ReleaseLease should succeed (clear lease) without changing status.
	if err := store.ReleaseLease(id, "w1"); err != nil {
		t.Fatalf("ReleaseLease from completed: %v", err)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed (unchanged)", got.Status)
	}
	if got.ClaimedBy != "" {
		t.Errorf("ClaimedBy = %q, want empty (lease cleared)", got.ClaimedBy)
	}
}

func TestReleaseLease_FromCancelled(t *testing.T) {
	db := openStateMachineDB(t)
	store := newStateMachineStore(t, db)
	id := insertRunningTask(t, store, "release-test")

	// Cancel the task while it still has a lease.
	if err := store.UpdateStatus(id, task.StatusCancelled); err != nil {
		t.Fatalf("UpdateStatus to cancelled: %v", err)
	}

	// ReleaseLease should succeed (clear lease) without changing status.
	if err := store.ReleaseLease(id, "w1"); err != nil {
		t.Fatalf("ReleaseLease from cancelled: %v", err)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCancelled {
		t.Errorf("status = %q, want cancelled (unchanged)", got.Status)
	}
	if got.ClaimedBy != "" {
		t.Errorf("ClaimedBy = %q, want empty (lease cleared)", got.ClaimedBy)
	}
}

// ── Valid running → retry accepted ────────────────────────────────────────────

func TestValidRunningToRetryAccepted(t *testing.T) {
	db := openStateMachineDB(t)
	store := newStateMachineStore(t, db)
	id := insertRunningTask(t, store, "valid-retry")

	// Transition running → failed (valid), then MakeRetryable should work.
	if err := store.UpdateStatus(id, task.StatusFailed); err != nil {
		t.Fatalf("UpdateStatus to failed: %v", err)
	}

	count, err := store.MakeRetryable(id, 5*time.Second)
	if err != nil {
		t.Fatalf("MakeRetryable from failed: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

// ── Valid running → release accepted ──────────────────────────────────────────

func TestValidRunningToReleaseAccepted(t *testing.T) {
	db := openStateMachineDB(t)
	store := newStateMachineStore(t, db)
	id := insertRunningTask(t, store, "valid-release")

	// Running → release should transition to queued.
	if err := store.ReleaseLease(id, "w1"); err != nil {
		t.Fatalf("ReleaseLease from running: %v", err)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
}
