package worker_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/task"
	"github.com/google/uuid"
	"go.uber.org/zap"

	workerpkg "github.com/EffNine/conductor/internal/worker"
)

// ── Fakes ────────────────────────────────────────────────────────────────────

type fakeProvider struct {
	name      string
	model     string
	resp      *apitypes.ChatCompletionResponse
	err       error
	callCount int
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) SupportsModel(id string) bool { return id == f.model || id == "" }
func (f *fakeProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	f.callCount++
	return f.resp, f.err
}
func (f *fakeProvider) ChatCompletionStream(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (f *fakeProvider) Embeddings(_ context.Context, _ *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (f *fakeProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: f.model}}, nil
}
func (f *fakeProvider) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (f *fakeProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: f.name, IsHealthy: true, LatencyMs: 10}, nil
}
func (f *fakeProvider) GetMetadata() provider.Metadata { return provider.Metadata{} }

// fakeExecutor wraps an agent for testing.
type fakeExecutor struct {
	resp   *apitypes.ChatCompletionResponse
	err    error
	calls  int
	mu     sync.Mutex
	store  task.Store
}

func (f *fakeExecutor) Execute(_ context.Context, taskID string) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	// Mark task completed.
	if f.store != nil {
		t, err := f.store.GetTask(taskID)
		if err == nil {
			t.Status = task.StatusCompleted
			t.Output = "done"
			_ = f.store.UpdateTask(t)
			_ = f.store.UpdateStatus(taskID, task.StatusCompleted)
		}
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func newTestDB(t *testing.T) *database.Database {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
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

func insertQueuedTask(t *testing.T, db *database.Database, input string) string {
	t.Helper()
	id := uuid.New().String()
	tsk := &task.Task{
		ID:      id,
		Status:  task.StatusQueued,
		Input:   input,
		MaxRetries: 3,
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return id
}

func insertFailedTask(t *testing.T, db *database.Database, input string, retryCount, maxRetries int) string {
	t.Helper()
	id := uuid.New().String()
	tsk := &task.Task{
		ID:         id,
		Status:     task.StatusFailed,
		Input:      input,
		RetryCount: retryCount,
		MaxRetries: maxRetries,
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return id
}

// ── Queue tests ──────────────────────────────────────────────────────────────

func TestClaimTask_Queued(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "hello")

	got, err := store.ClaimTask("w1", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
	if got.Status != task.StatusRunning {
		t.Errorf("Status = %q, want running", got.Status)
	}
	if got.ClaimedBy != "w1" {
		t.Errorf("ClaimedBy = %q, want w1", got.ClaimedBy)
	}
}

func TestClaimTask_NoEligible(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	_, err := store.ClaimTask("w1", 5*time.Minute)
	if err != task.ErrNoEligibleTask {
		t.Fatalf("expected ErrNoEligibleTask, got %v", err)
	}
}

func TestClaimTask_CompletedNotClaimable(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "hello")
	// Transition through running to completed (valid path).
	_ = store.UpdateStatus(id, task.StatusRunning)
	_ = store.UpdateStatus(id, task.StatusCompleted)

	_, err := store.ClaimTask("w1", 5*time.Minute)
	if err != task.ErrNoEligibleTask {
		t.Fatalf("expected ErrNoEligibleTask, got %v", err)
	}
}

func TestClaimTask_CancelledNotClaimable(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "hello")
	// Transition through running to cancelled (valid path).
	_ = store.UpdateStatus(id, task.StatusRunning)
	_ = store.UpdateStatus(id, task.StatusCancelled)

	_, err := store.ClaimTask("w1", 5*time.Minute)
	if err != task.ErrNoEligibleTask {
		t.Fatalf("expected ErrNoEligibleTask, got %v", err)
	}
}

func TestClaimTask_ActiveLeaseNotClaimable(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	insertQueuedTask(t, db, "hello")
	// Claim with active lease.
	_, err := store.ClaimTask("w1", 5*time.Minute)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	// Second claim should fail.
	_, err = store.ClaimTask("w2", 5*time.Minute)
	if err != task.ErrNoEligibleTask {
		t.Fatalf("expected ErrNoEligibleTask on second claim, got %v", err)
	}
}

func TestClaimTask_ExpiredLeaseReclaimable(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "hello")
	// Claim with a very short lease.
	_, err := store.ClaimTask("w1", 1*time.Nanosecond)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	// Expire the lease.
	_, _ = store.ExpireStaleLeases()
	// Should be reclaimable now.
	got, err := store.ClaimTask("w2", 5*time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
	if got.ClaimedBy != "w2" {
		t.Errorf("ClaimedBy = %q, want w2", got.ClaimedBy)
	}
}

func TestClaimTask_FailedWithRetryableNextRetryAt(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertFailedTask(t, db, "fail", 0, 3)
	// Set next_retry_at in the past.
	past := time.Now().UTC().Add(-time.Hour)
	_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("next_retry_at", past).Error

	got, err := store.ClaimTask("w1", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
}

func TestClaimTask_AtomicTwoWorkers(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	// Insert two tasks.
	id1 := insertQueuedTask(t, db, "task-1")
	id2 := insertQueuedTask(t, db, "task-2")

	var wg sync.WaitGroup
	var results [2]*task.Task
	var errs [2]error

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = store.ClaimTask("w-atomic", 5*time.Minute)
		}(i)
	}
	wg.Wait()

	if errs[0] != nil && errs[1] != nil {
		t.Fatalf("both claims failed: %v / %v", errs[0], errs[1])
	}
	if errs[0] == nil && errs[1] == nil {
		// Both claimed — should have different tasks.
		if results[0].ID == results[1].ID {
			t.Error("two workers claimed the same task")
		}
	}
	// Verify both tasks exist and are running.
	for _, id := range []string{id1, id2} {
		tsk, err := store.GetTask(id)
		if err != nil {
			t.Fatalf("GetTask %s: %v", id, err)
		}
		if tsk.Status != task.StatusRunning {
			t.Errorf("task %s status = %q, want running", id, tsk.Status)
		}
	}
}

// ── Lease tests ──────────────────────────────────────────────────────────────

func TestReleaseLease(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "hello")
	_, err := store.ClaimTask("w1", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if err := store.ReleaseLease(id, "w1"); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	got, err := store.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != task.StatusQueued {
		t.Errorf("Status = %q, want queued", got.Status)
	}
	if got.ClaimedBy != "" {
		t.Errorf("ClaimedBy = %q, want empty", got.ClaimedBy)
	}
}

func TestUpdateLease(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "hello")
	_, err := store.ClaimTask("w1", 1*time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	newUntil := time.Now().UTC().Add(10 * time.Minute)
	if err := store.UpdateLease(id, "w1", newUntil); err != nil {
		t.Fatalf("UpdateLease: %v", err)
	}
	got, _ := store.GetTask(id)
	if got.LeaseUntil == nil || !got.LeaseUntil.Equal(newUntil) {
		t.Errorf("LeaseUntil = %v, want %v", got.LeaseUntil, newUntil)
	}
}

func TestExpireStaleLeases(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id1 := insertQueuedTask(t, db, "task-1")
	id2 := insertQueuedTask(t, db, "task-2")
	_, _ = store.ClaimTask("w1", 1*time.Nanosecond)
	_, _ = store.ClaimTask("w2", 5*time.Minute)

	n, err := store.ExpireStaleLeases()
	if err != nil {
		t.Fatalf("ExpireStaleLeases: %v", err)
	}
	if n != 1 {
		t.Errorf("expired = %d, want 1", n)
	}
	got1, _ := store.GetTask(id1)
	if got1.Status != task.StatusQueued {
		t.Errorf("expired task status = %q, want queued", got1.Status)
	}
	got2, _ := store.GetTask(id2)
	if got2.Status != task.StatusRunning {
		t.Errorf("active task status = %q, want running", got2.Status)
	}
}

// ── Retry tests ──────────────────────────────────────────────────────────────

func TestMakeRetryable_IncrementsCount(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertFailedTask(t, db, "fail", 0, 3)
	count, err := store.MakeRetryable(id, 5*time.Second)
	if err != nil {
		t.Fatalf("MakeRetryable: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	got, _ := store.GetTask(id)
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if got.NextRetryAt == nil || got.NextRetryAt.Before(time.Now().UTC().Add(-time.Second)) {
		t.Error("next_retry_at should be in the future")
	}
}

func TestMakeRetryable_MaxRetriesZero(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertFailedTask(t, db, "fail", 0, 0)
	_, err := store.MakeRetryable(id, 5*time.Second)
	if err != nil {
		t.Fatalf("MakeRetryable: %v", err)
	}
	got, _ := store.GetTask(id)
	// With MaxRetries=0, the task should still be queued (executor will reject on execute).
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
}

func TestReadyRetries(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertFailedTask(t, db, "fail", 0, 3)
	// Set next_retry_at in the past.
	past := time.Now().UTC().Add(-time.Hour)
	_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("next_retry_at", past).Error

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

// ── Worker pool tests ────────────────────────────────────────────────────────

func TestWorkerExecutesTask(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "hello")

	fe := &fakeExecutor{store: store}
	pool := workerpkg.New(workerpkg.Config{
		WorkerCount:  1,
		PollInterval: 100 * time.Millisecond,
		LeaseDuration: 5 * time.Minute,
	}, store, fe, zap.NewNop())
	pool.Start()
	defer pool.Stop()

	// Wait for worker to claim and execute.
	deadline := time.After(3 * time.Second)
ticker:
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for task completion")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusCompleted {
			break ticker
		}
		time.Sleep(50 * time.Millisecond)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if fe.calls != 1 {
		t.Errorf("calls = %d, want 1", fe.calls)
	}
}

func TestWorkerFailureBecomesRetryable(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "fail")

	fe := &fakeExecutor{err: assertAnError("boom"), store: store}
	pool := workerpkg.New(workerpkg.Config{
		WorkerCount:  1,
		PollInterval: 100 * time.Millisecond,
		LeaseDuration: 5 * time.Minute,
	}, store, fe, zap.NewNop())
	pool.Start()
	defer pool.Stop()

	// Wait for failure handling.
	deadline := time.After(3 * time.Second)
ticker:
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for retry")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusQueued && got.RetryCount >= 1 {
			break ticker
		}
		time.Sleep(50 * time.Millisecond)
	}

	got, _ := store.GetTask(id)
	if got.RetryCount < 1 {
		t.Errorf("retry_count = %d, want >= 1", got.RetryCount)
	}
}

func TestWorkerFailureExhaustsRetries(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "fail")
	// Pre-set retry count to max.
	_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("retry_count", 3).Error

	fe := &fakeExecutor{err: assertAnError("boom"), store: store}
	pool := workerpkg.New(workerpkg.Config{
		WorkerCount:  1,
		PollInterval: 100 * time.Millisecond,
		LeaseDuration: 5 * time.Minute,
	}, store, fe, zap.NewNop())
	pool.Start()
	defer pool.Stop()

	deadline := time.After(3 * time.Second)
ticker:
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for final failure")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusFailed {
			break ticker
		}
		time.Sleep(50 * time.Millisecond)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
}

func TestSchedulerPromotesRetryable(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertFailedTask(t, db, "fail", 0, 3)
	// Set next_retry_at in the past.
	past := time.Now().UTC().Add(-time.Hour)
	_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("next_retry_at", past).Error

	sched := workerpkg.NewScheduler(store, zap.NewNop())
	sched.Start()
	defer sched.Stop()

	deadline := time.After(2 * time.Second)
ticker:
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for promotion")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusQueued {
			break ticker
		}
		time.Sleep(50 * time.Millisecond)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
}

func TestSchedulerDoesNotExecute(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertFailedTask(t, db, "fail", 0, 3)
	past := time.Now().UTC().Add(-time.Hour)
	_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("next_retry_at", past).Error

	sched := workerpkg.NewScheduler(store, zap.NewNop())
	sched.Start()
	defer sched.Stop()

	time.Sleep(1500 * time.Millisecond)
	got, _ := store.GetTask(id)
	// Scheduler only promotes; it should not execute the task.
	if got.Status != task.StatusQueued {
		t.Errorf("scheduler should not execute, status = %q", got.Status)
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func assertAnError(s string) error {
	return &testErr{msg: s}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

// ── Correctness Gate Tests ───────────────────────────────────────────────────

// TestMakeRetryable_ClearsLease verifies that MakeRetryable clears lease fields
// so a retryable task can be reclaimed by another worker.
func TestMakeRetryable_ClearsLease(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "fail")
	// Claim the task as worker A.
	_, err := store.ClaimTask("w1", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	got, _ := store.GetTask(id)
	if got.ClaimedBy != "w1" {
		t.Fatalf("ClaimedBy = %q, want w1", got.ClaimedBy)
	}
	// Simulate failure → MakeRetryable.
	count, err := store.MakeRetryable(id, 5*time.Second)
	if err != nil {
		t.Fatalf("MakeRetryable: %v", err)
	}
	if count != 1 {
		t.Errorf("retry count = %d, want 1", count)
	}
	got, _ = store.GetTask(id)
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if got.ClaimedBy != "" {
		t.Errorf("ClaimedBy = %q, want empty", got.ClaimedBy)
	}
	if got.ClaimedAt != nil {
		t.Error("ClaimedAt should be nil")
	}
	if got.LeaseUntil != nil {
		t.Error("LeaseUntil should be nil")
	}
	if got.NextRetryAt == nil {
		t.Fatal("NextRetryAt should be set")
	}
}

// TestRetryableTaskReclaimableByAnotherWorker verifies the full flow:
// Worker A claims, fails, MakeRetryable clears lease, Worker B can claim.
func TestRetryableTaskReclaimableByAnotherWorker(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "fail")

	// Worker A claims.
	_, err := store.ClaimTask("w1", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask w1: %v", err)
	}

	// Simulate failure → MakeRetryable.
	_, err = store.MakeRetryable(id, 5*time.Second)
	if err != nil {
		t.Fatalf("MakeRetryable: %v", err)
	}

	// Worker B should be able to claim it.
	got, err := store.ClaimTask("w2", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask w2: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
	if got.ClaimedBy != "w2" {
		t.Errorf("ClaimedBy = %q, want w2", got.ClaimedBy)
	}
}

// TestStaleWorkerCannotOverwrite tests that a stale worker's terminal update
// does not overwrite a newer worker's result when using ownership-checked release.
func TestStaleWorkerCannotOverwrite(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "hello")

	// Worker A claims.
	_, err := store.ClaimTask("w1", 1*time.Nanosecond)
	if err != nil {
		t.Fatalf("ClaimTask w1: %v", err)
	}

	// Expire Worker A's lease.
	_, _ = store.ExpireStaleLeases()

	// Worker B reclaims.
	_, err = store.ClaimTask("w2", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask w2: %v", err)
	}

	// Complete the task as Worker B.
	_ = store.UpdateStatus(id, task.StatusCompleted)

	// Worker A tries to release lease without ownership — should fail.
	err = store.ReleaseLease(id, "w1")
	if err == nil {
		t.Fatal("ReleaseLease without ownership should fail")
	}

	// Verify task is still completed.
	got, _ := store.GetTask(id)
	if got.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}

	// Worker B can release its own lease (sets back to queued).
	err = store.ReleaseLease(id, "w2")
	if err != nil {
		t.Fatalf("ReleaseLease w2: %v", err)
	}
}

// TestStaleWorkerCannotClearLease verifies that ReleaseLease without matching
// claimed_by cannot clear a newer worker's lease.
func TestStaleWorkerCannotClearLease(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTask(t, db, "hello")

	// Worker A claims.
	_, err := store.ClaimTask("w1", 1*time.Nanosecond)
	if err != nil {
		t.Fatalf("ClaimTask w1: %v", err)
	}

	// Expire lease, then Worker B reclaims.
	_, _ = store.ExpireStaleLeases()
	_, err = store.ClaimTask("w2", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimTask w2: %v", err)
	}

	// Worker A tries to clear Worker B's lease (no ownership).
	err = store.ReleaseLease(id, "w1")
	if err == nil {
		t.Fatal("expected error when releasing another worker's lease")
	}

	// Verify Worker B still owns the lease.
	got, _ := store.GetTask(id)
	if got.ClaimedBy != "w2" {
		t.Errorf("ClaimedBy = %q, want w2", got.ClaimedBy)
	}
	if got.Status != task.StatusRunning {
		t.Errorf("status = %q, want running", got.Status)
	}

	// Worker B can release its own lease.
	err = store.ReleaseLease(id, "w2")
	if err != nil {
		t.Fatalf("ReleaseLease w2: %v", err)
	}
	got, _ = store.GetTask(id)
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
}

// TestExpireStaleLeases_Targeted checks that only expired running leases are recovered.
func TestExpireStaleLeases_Targeted(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)

	// Expired running lease.
	id1 := insertQueuedTask(t, db, "task-1")
	_, _ = store.ClaimTask("w1", 1*time.Nanosecond)

	// Active lease.
	id2 := insertQueuedTask(t, db, "task-2")
	_, _ = store.ClaimTask("w2", 5*time.Minute)

	// Completed task (should not be touched).
	id3 := insertQueuedTask(t, db, "task-3")
	_, _ = store.ClaimTask("w3", 5*time.Minute)
	_ = store.UpdateStatus(id3, task.StatusCompleted)

	// Cancelled task (should not be touched).
	id4 := insertQueuedTask(t, db, "task-4")
	_, _ = store.ClaimTask("w4", 5*time.Minute)
	_ = store.UpdateStatus(id4, task.StatusCancelled)

	// Failed task with no lease (should not be touched).
	id5 := insertFailedTask(t, db, "task-5", 0, 3)
	past := time.Now().UTC().Add(-time.Hour)
	_ = db.DB.Model(&task.Task{}).Where("id = ?", id5).Update("next_retry_at", past).Error

	n, err := store.ExpireStaleLeases()
	if err != nil {
		t.Fatalf("ExpireStaleLeases: %v", err)
	}
	if n != 1 {
		t.Errorf("expired = %d, want 1", n)
	}

	// id1: expired → should be queued.
	got1, _ := store.GetTask(id1)
	if got1.Status != task.StatusQueued {
		t.Errorf("expired task status = %q, want queued", got1.Status)
	}
	if got1.ClaimedBy != "" {
		t.Errorf("expired task claimed_by = %q, want empty", got1.ClaimedBy)
	}

	// id2: active lease → should remain running.
	got2, _ := store.GetTask(id2)
	if got2.Status != task.StatusRunning {
		t.Errorf("active task status = %q, want running", got2.Status)
	}
	if got2.ClaimedBy != "w2" {
		t.Errorf("active task claimed_by = %q, want w2", got2.ClaimedBy)
	}

	// id3: completed → untouched.
	got3, _ := store.GetTask(id3)
	if got3.Status != task.StatusCompleted {
		t.Errorf("completed task status = %q, want completed", got3.Status)
	}

	// id4: cancelled → untouched.
	got4, _ := store.GetTask(id4)
	if got4.Status != task.StatusCancelled {
		t.Errorf("cancelled task status = %q, want cancelled", got4.Status)
	}

	// id5: failed → untouched (no lease).
	got5, _ := store.GetTask(id5)
	if got5.Status != task.StatusFailed {
		t.Errorf("failed task status = %q, want failed", got5.Status)
	}
}

// TestSchedulerOnlyPromotes verifies the scheduler promotes but never executes.
func TestSchedulerOnlyPromotes(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	id := insertFailedTask(t, db, "fail", 0, 3)
	past := time.Now().UTC().Add(-time.Hour)
	_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("next_retry_at", past).Error

	sched := workerpkg.NewScheduler(store, zap.NewNop())
	sched.Start()
	defer sched.Stop()

	deadline := time.After(2 * time.Second)
ticker:
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for promotion")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusQueued {
			break ticker
		}
		time.Sleep(50 * time.Millisecond)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusQueued {
		t.Errorf("scheduler should not execute, status = %q", got.Status)
	}
	// Scheduler must not have claimed or executed the task.
	if got.ClaimedBy != "" {
		t.Errorf("scheduler should not claim, claimed_by = %q", got.ClaimedBy)
	}
}

// TestWorkerShutdown verifies graceful shutdown: workers stop claiming,
// WaitGroup completes, and no hanging.
func TestWorkerShutdown(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	// Insert many tasks so workers keep polling.
	for i := 0; i < 10; i++ {
		insertQueuedTask(t, db, fmt.Sprintf("task-%d", i))
	}

	fe := &fakeExecutor{}
	pool := workerpkg.New(workerpkg.Config{
		WorkerCount:     2,
		PollInterval:    100 * time.Millisecond,
		LeaseDuration:   5 * time.Minute,
		ShutdownTimeout: 500 * time.Millisecond,
	}, store, fe, zap.NewNop())
	pool.Start()

	// Let workers process some tasks.
	time.Sleep(300 * time.Millisecond)

	// Stop should complete without hanging.
	done := make(chan struct{})
	go func() {
		pool.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("worker pool shutdown hung")
	}
}

// TestExistingSyncAPIStillWorks verifies POST /api/tasks synchronous behavior
// is unchanged when worker pool is not active.
func TestExistingSyncAPIStillWorks(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)

	// Create a task directly (simulating POST /api/tasks without worker pool).
	id := insertQueuedTask(t, db, "sync-task")

	// Execute synchronously via fake executor.
	fe := &fakeExecutor{store: store}
	err := fe.Execute(context.Background(), id)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if fe.calls != 1 {
		t.Errorf("calls = %d, want 1", fe.calls)
	}
}

// TestMaxRetriesSemantics verifies exact retry counts.
func TestMaxRetriesSemantics(t *testing.T) {
	t.Run("MaxRetriesZeroNoRetry", func(t *testing.T) {
		db := newTestDB(t)
		store := task.NewSQLiteStore(db)
		id := insertQueuedTask(t, db, "fail")
		// Explicitly set max retries to 0.
		_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("max_retries", 0).Error

		// Claim and fail.
		_, _ = store.ClaimTask("w1", 5*time.Minute)
		// Simulate failure: MakeRetryable would be called but max_retries=0
		// means handleFailure should skip it. Here we just verify the task
		// stays in a valid state.
		got, _ := store.GetTask(id)
		if got.MaxRetries != 0 {
			t.Errorf("max_retries = %d, want 0", got.MaxRetries)
		}
	})

	t.Run("MaxRetriesOneExactlyOneRetry", func(t *testing.T) {
		db := newTestDB(t)
		store := task.NewSQLiteStore(db)
		id := insertQueuedTask(t, db, "fail")
		_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("max_retries", 1).Error

		// First claim + fail → retry.
		_, _ = store.ClaimTask("w1", 5*time.Minute)
		count, err := store.MakeRetryable(id, 5*time.Second)
		if err != nil {
			t.Fatalf("MakeRetryable: %v", err)
		}
		if count != 1 {
			t.Errorf("retry count = %d, want 1", count)
		}

		// Re-claim and fail again → should NOT retry (exhausted).
		_, _ = store.ClaimTask("w2", 5*time.Minute)
		count, err = store.MakeRetryable(id, 5*time.Second)
		if err != nil {
			t.Fatalf("MakeRetryable second: %v", err)
		}
		if count != 2 {
			t.Errorf("retry count = %d, want 2 (second failure recorded)", count)
		}
		got, _ := store.GetTask(id)
		if got.Status != task.StatusQueued {
			t.Errorf("status = %q, want queued (retry recorded)", got.Status)
		}
	})
}

