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

// slowExecutor blocks until the context is cancelled, simulating a long-running task.
type slowExecutor struct {
	store  task.Store
	doneCh chan struct{}
	mu     sync.Mutex
	calls  int
}

func (e *slowExecutor) Execute(ctx context.Context, taskID string) error {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	if e.store != nil {
		_ = e.store.UpdateStatus(taskID, task.StatusRunning)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.doneCh:
		if e.store != nil {
			_ = e.store.UpdateStatus(taskID, task.StatusCompleted)
		}
		return nil
	}
}

func (e *slowExecutor) signalDone() { close(e.doneCh) }

func newTestDBForCancellation(t *testing.T) *database.Database {
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

func insertQueuedTaskForCancellation(t *testing.T, db *database.Database) string {
	t.Helper()
	id := uuid.New().String()
	tsk := &task.Task{
		ID:         id,
		Status:     task.StatusQueued,
		Input:      "slow task",
		MaxRetries: 0,
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return id
}

func TestTaskTimeoutCausesCancellation(t *testing.T) {
	db := newTestDBForCancellation(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTaskForCancellation(t, db)

	exec := &slowExecutor{store: store}
	pool := workerpkg.New(workerpkg.Config{
		WorkerCount:   1,
		PollInterval:  50 * time.Millisecond,
		LeaseDuration: 5 * time.Minute,
		TaskTimeout:   500 * time.Millisecond,
	}, store, exec, zap.NewNop())
	pool.Start()
	defer pool.Stop()

	// Wait for timeout + status transition.
	deadline := time.After(3 * time.Second)
ticker:
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for task cancellation")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusCancelled {
			break ticker
		}
		time.Sleep(50 * time.Millisecond)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
	// Verify no retry happened.
	if got.RetryCount != 0 {
		t.Errorf("retry_count = %d, want 0 (cancellation should not retry)", got.RetryCount)
	}
}

func TestCancelTaskNeverRetries(t *testing.T) {
	db := newTestDBForCancellation(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTaskForCancellation(t, db)

	exec := &slowExecutor{store: store}
	pool := workerpkg.New(workerpkg.Config{
		WorkerCount:   1,
		PollInterval:  50 * time.Millisecond,
		LeaseDuration: 5 * time.Minute,
		TaskTimeout:   5 * time.Second, // long enough that we cancel manually
	}, store, exec, zap.NewNop())
	pool.Start()

	// Wait for task to be claimed and running.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for task to start")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Cancel via the pool.
	if err := pool.CancelTask(id); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	// Wait for status to become cancelled.
	deadline = time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for cancelled status")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusCancelled {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	pool.Stop()

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
}

func TestDeadlineExceededNeverRetries(t *testing.T) {
	db := newTestDBForCancellation(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTaskForCancellation(t, db)

	exec := &slowExecutor{store: store}
	pool := workerpkg.New(workerpkg.Config{
		WorkerCount:   1,
		PollInterval:  50 * time.Millisecond,
		LeaseDuration: 5 * time.Minute,
		TaskTimeout:   200 * time.Millisecond,
	}, store, exec, zap.NewNop())
	pool.Start()
	defer pool.Stop()

	// Wait for timeout-triggered cancellation.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for deadline exceeded")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.Status == task.StatusCancelled {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	got, _ := store.GetTask(id)
	if got.Status != task.StatusCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
	// Must not have retried.
	if got.RetryCount != 0 {
		t.Errorf("retry_count = %d, want 0", got.RetryCount)
	}
}

// Verify that non-cancellation errors still go through retry path.
func TestNonCancellationErrorStillRetries(t *testing.T) {
	db := newTestDBForCancellation(t)
	store := task.NewSQLiteStore(db)
	id := insertQueuedTaskForCancellation(t, db)
	// Set MaxRetries > 0 so failures trigger the retry path.
	_ = db.DB.Model(&task.Task{}).Where("id = ?", id).Update("max_retries", 3).Error

	failingExec := &countingFailingExecutor{store: store, err: fmt.Errorf("random boom")}
	pool := workerpkg.New(workerpkg.Config{
		WorkerCount:   1,
		PollInterval:  50 * time.Millisecond,
		LeaseDuration: 5 * time.Minute,
		TaskTimeout:   5 * time.Second,
	}, store, failingExec, zap.NewNop())
	pool.Start()
	defer pool.Stop()

	// Wait for retry to be registered.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for retry")
		default:
		}
		got, _ := store.GetTask(id)
		if got != nil && got.RetryCount >= 1 && got.NextRetryAt != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	got, _ := store.GetTask(id)
	if got.RetryCount < 1 {
		t.Errorf("retry_count = %d, want >= 1", got.RetryCount)
	}
}

// countingFailingExecutor always returns a non-context error.
type countingFailingExecutor struct {
	store task.Store
	err   error
	calls int
	mu    sync.Mutex
}

func (e *countingFailingExecutor) Execute(_ context.Context, taskID string) error {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	if e.store != nil {
		_ = e.store.UpdateStatus(taskID, task.StatusRunning)
	}
	return e.err
}

// --- Fake provider for coordinator cancellation test ---
type noopProvider struct {
	name string
}

func (f *noopProvider) Name() string                 { return f.name }
func (f *noopProvider) SupportsModel(id string) bool { return true }
func (f *noopProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (f *noopProvider) ChatCompletionStream(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (f *noopProvider) Embeddings(_ context.Context, _ *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (f *noopProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: "test"}}, nil
}
func (f *noopProvider) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (f *noopProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: f.name, IsHealthy: true, LatencyMs: 10}, nil
}
func (f *noopProvider) GetMetadata() provider.Metadata { return provider.Metadata{} }
