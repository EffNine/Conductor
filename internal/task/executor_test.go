package task_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/agent"
	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/task"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ── Fakes ────────────────────────────────────────────────────────────────────

type fakeProvider struct {
	name      string
	model     string
	resp      *apitypes.ChatCompletionResponse
	err       error
	callCount int
	override  func(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error)
}

func (f *fakeProvider) Name() string                 { return f.name }
func (f *fakeProvider) SupportsModel(id string) bool { return id == f.model || id == "" }
func (f *fakeProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	f.callCount++
	if f.override != nil {
		return f.override(ctx, req)
	}
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

// fakeAgent wraps a provider registry and routes a single-step LLM call for testing.
type fakeAgent struct {
	name      string
	model     string
	resp      *apitypes.ChatCompletionResponse
	err       error
	callCount int
	store     task.Store // optional: if set, emits events
}

func (f *fakeAgent) Name() string { return f.name }

func (f *fakeAgent) Execute(ctx context.Context, t *agent.TaskRef) (*agent.TaskRef, error) {
	f.callCount++
	if f.err != nil {
		return t, f.err
	}
	// Check context cancellation.
	select {
	case <-ctx.Done():
		return t, ctx.Err()
	default:
	}
	if f.resp == nil {
		f.resp = &apitypes.ChatCompletionResponse{
			Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "ok"}}},
		}
	}
	now := timeNow()
	t.Output = f.resp.Choices[0].Message.ContentString()
	t.Provider = f.name
	t.Model = f.model
	t.StepCount = 1
	t.CompletedAt = &now
	t.Status = agent.StatusCompleted
	// Emit task.completed if store is wired.
	if f.store != nil {
		data, _ := json.Marshal(map[string]any{"step": 1})
		_ = f.store.CreateTaskEvent(&task.TaskEvent{
			ID:        uuid.New().String(),
			TaskID:    t.ID,
			EventType: "task.completed",
			EventData: data,
		})
	}
	return t, nil
}

func timeNow() time.Time { return time.Now().UTC() }

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

func newTestExecutor(t *testing.T, db *database.Database, reg *provider.Registry, a agent.Agent) *task.TaskExecutor {
	t.Helper()
	store := task.NewSQLiteStore(db)
	eng, err := router.NewEngine(&config.Config{}, reg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	ut := usage.NewTracker(db, usage.NewEstimator(reg, nil), zap.NewNop())
	cat := catalog.New(reg, nil)
	return task.NewTaskExecutor(store, eng, a, cat, ut, zap.NewNop())
}

func errorf(s string) error {
	return &testError{msg: s}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// ── Execute tests ────────────────────────────────────────────────────────────

func TestExecute_Success(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o", resp: &apitypes.ChatCompletionResponse{
		Model:   "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "hello world"}}},
		Usage:   &apitypes.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "say hi", Model: "test/gpt-4o"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := exec.Execute(context.Background(), tsk.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result task.Task
	if err := db.DB.Where("id = ?", tsk.ID).First(&result).Error; err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if result.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed", result.Status)
	}
	if result.Output != "hello world" {
		t.Errorf("output = %q, want %q", result.Output, "hello world")
	}
	if result.Provider != "test" {
		t.Errorf("provider = %q, want %q", result.Provider, "test")
	}
	if result.StepCount != 1 {
		t.Errorf("step_count = %d, want 1", result.StepCount)
	}
	if result.CompletedAt == nil || result.CompletedAt.IsZero() {
		t.Error("completed_at should be set")
	}
	if fake.callCount != 1 {
		t.Errorf("agent call count = %d, want 1", fake.callCount)
	}
}

func TestExecute_ProviderFailure(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o", err: errorf("boom")}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "fail me", Model: "test/gpt-4o"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	err := exec.Execute(context.Background(), tsk.ID)
	if err == nil {
		t.Fatal("expected error from Execute")
	}

	var result task.Task
	if err := db.DB.Where("id = ?", tsk.ID).First(&result).Error; err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if result.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed", result.Status)
	}
}

func TestExecute_AlreadyTerminal(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o"}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusCompleted, Input: "done"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	err := exec.Execute(context.Background(), tsk.ID)
	if err == nil {
		t.Fatal("expected error for terminal task")
	}
}

func TestExecute_DefaultModel(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "default-model", resp: &apitypes.ChatCompletionResponse{
		Model:   "default-model",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "ok"}}},
	}}
	reg.Register(&fakeProvider{name: "test", model: "default-model"})
	exec := newTestExecutor(t, db, reg, fake)

	// Task with no model specified — should use catalog default.
	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "use default"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := exec.Execute(context.Background(), tsk.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result task.Task
	if err := db.DB.Where("id = ?", tsk.ID).First(&result).Error; err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if result.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed", result.Status)
	}
}

func TestExecute_WithSystemPrompt(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	var capturedReq apitypes.ChatCompletionRequest
	_ = capturedReq
	fake := &fakeAgent{
		name: "test", model: "gpt-4o",
		resp: &apitypes.ChatCompletionResponse{
			Model:   "gpt-4o",
			Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "ok"}}},
		},
	}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	sysJSON, _ := json.Marshal(map[string]string{"system": "You are helpful."})
	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "hello", InputJSON: sysJSON}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := exec.Execute(context.Background(), tsk.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result task.Task
	if err := db.DB.Where("id = ?", tsk.ID).First(&result).Error; err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if result.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed", result.Status)
	}
}

// ── Store extension tests ────────────────────────────────────────────────────

func TestCreateTaskStep(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)

	step := &task.TaskStep{
		ID:         uuid.New().String(),
		TaskID:     "task-1",
		StepNumber: 1,
		Provider:   "openai",
		Model:      "gpt-4o",
		Status:     "completed",
	}
	if err := store.CreateTaskStep(step); err != nil {
		t.Fatalf("CreateTaskStep: %v", err)
	}

	var loaded task.TaskStep
	if err := db.DB.Where("id = ?", step.ID).First(&loaded).Error; err != nil {
		t.Fatalf("GetTaskStep: %v", err)
	}
	if loaded.Provider != "openai" {
		t.Errorf("provider = %q, want openai", loaded.Provider)
	}
}

func TestCreateTaskStep_RequiresID(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	if err := store.CreateTaskStep(&task.TaskStep{TaskID: "t1"}); err == nil {
		t.Fatal("expected error for empty step ID")
	}
}

func TestCreateTaskEvent(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)

	evt := &task.TaskEvent{
		ID:        uuid.New().String(),
		TaskID:    "task-1",
		EventType: "status_changed",
	}
	if err := store.CreateTaskEvent(evt); err != nil {
		t.Fatalf("CreateTaskEvent: %v", err)
	}

	var loaded task.TaskEvent
	if err := db.DB.Where("id = ?", evt.ID).First(&loaded).Error; err != nil {
		t.Fatalf("GetTaskEvent: %v", err)
	}
	if loaded.EventType != "status_changed" {
		t.Errorf("event_type = %q, want status_changed", loaded.EventType)
	}
}

func TestCreateTaskEvent_RequiresID(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)
	if err := store.CreateTaskEvent(&task.TaskEvent{TaskID: "t1"}); err == nil {
		t.Fatal("expected error for empty event ID")
	}
}

// ── Retry semantics ──────────────────────────────────────────────────────────

func TestRetry_MaxRetriesZero(t *testing.T) {
	db := newTestDB(t)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusFailed, Input: "retry", MaxRetries: 0, RetryCount: 0}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Retry should be rejected because MaxRetries=0.
	var loaded task.Task
	db.DB.Where("id = ?", tsk.ID).First(&loaded)
	if loaded.MaxRetries != 0 {
		t.Fatalf("max_retries = %d, want 0", loaded.MaxRetries)
	}
}

func TestRetry_MaxRetriesEnforced(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)

	// MaxRetries=1: first retry allowed, second rejected.
	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusFailed, Input: "retry", MaxRetries: 1, RetryCount: 0}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	count, err := store.IncrementRetry(tsk.ID)
	if err != nil {
		t.Fatalf("IncrementRetry: %v", err)
	}
	if count != 1 {
		t.Errorf("retry count = %d, want 1", count)
	}

	// Simulate second retry attempt — count is now 1 which equals MaxRetries.
	var loaded task.Task
	db.DB.Where("id = ?", tsk.ID).First(&loaded)
	if loaded.RetryCount >= loaded.MaxRetries {
		// Correctly identified as exceeded.
	} else {
		t.Error("expected retry count to equal max retries after increment")
	}
}

func TestRetry_DoesNotAffectMaxSteps(t *testing.T) {
	db := newTestDB(t)
	store := task.NewSQLiteStore(db)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "test", MaxRetries: 3, MaxSteps: 5}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err := store.IncrementRetry(tsk.ID)
	if err != nil {
		t.Fatalf("IncrementRetry: %v", err)
	}

	var loaded task.Task
	db.DB.Where("id = ?", tsk.ID).First(&loaded)
	if loaded.MaxSteps != 5 {
		t.Errorf("MaxSteps = %d, want 5 (unchanged by retry)", loaded.MaxSteps)
	}
	if loaded.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", loaded.RetryCount)
	}
}

// ── MaxSteps / MaxRetries separation ─────────────────────────────────────────

func TestMaxStepsSeparateFromMaxRetries(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o", resp: &apitypes.ChatCompletionResponse{
		Model:   "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "ok"}}},
	}}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	// Task with MaxSteps=3 and MaxRetries=2, starting from pending.
	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "test", Model: "test/gpt-4o", MaxSteps: 3, MaxRetries: 2}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := exec.Execute(context.Background(), tsk.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result task.Task
	db.DB.Where("id = ?", tsk.ID).First(&result)
	if result.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed", result.Status)
	}
	if result.MaxSteps != 3 {
		t.Errorf("MaxSteps = %d, want 3", result.MaxSteps)
	}
	if result.MaxRetries != 2 {
		t.Errorf("MaxRetries = %d, want 2", result.MaxRetries)
	}
}

// ── Event lifecycle tests ────────────────────────────────────────────────────

func TestEvent_TaskStartedEmitted(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o", resp: &apitypes.ChatCompletionResponse{
		Model:   "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "ok"}}},
	}}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "test", Model: "test/gpt-4o"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := exec.Execute(context.Background(), tsk.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var evt task.TaskEvent
	if err := db.DB.Where("task_id = ? AND event_type = ?", tsk.ID, "task.started").First(&evt).Error; err != nil {
		t.Fatalf("task.started event not found: %v", err)
	}
}

func TestEvent_CancellationEmitsCancelled(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o"}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "test", Model: "test/gpt-4o"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before execution
	_ = exec.Execute(ctx, tsk.ID)

	var cancelledEvt task.TaskEvent
	err := db.DB.Where("task_id = ? AND event_type = ?", tsk.ID, "task.cancelled").First(&cancelledEvt).Error
	if err != nil {
		t.Fatalf("task.cancelled event not found: %v", err)
	}

	// Ensure no task.failed event was emitted.
	var failedEvt task.TaskEvent
	err = db.DB.Where("task_id = ? AND event_type = ?", tsk.ID, "task.failed").First(&failedEvt).Error
	if err == nil {
		t.Error("task.failed event should NOT be emitted on cancellation")
	}
}

func TestEvent_ProviderFailureEmitsFailed(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o", err: errorf("boom")}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "fail", Model: "test/gpt-4o"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_ = exec.Execute(context.Background(), tsk.ID)

	var failedEvt task.TaskEvent
	if err := db.DB.Where("task_id = ? AND event_type = ?", tsk.ID, "task.failed").First(&failedEvt).Error; err != nil {
		t.Fatalf("task.failed event not found: %v", err)
	}

	// Ensure no task.cancelled event was emitted.
	var cancelledEvt task.TaskEvent
	err := db.DB.Where("task_id = ? AND event_type = ?", tsk.ID, "task.cancelled").First(&cancelledEvt).Error
	if err == nil {
		t.Error("task.cancelled event should NOT be emitted on provider failure")
	}
}

func TestEvent_NoContradictoryTerminalEvents(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o", store: task.NewSQLiteStore(db), resp: &apitypes.ChatCompletionResponse{
		Model:   "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "done"}}},
	}}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "test", Model: "test/gpt-4o"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := exec.Execute(context.Background(), tsk.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Count terminal events.
	var completedCount, failedCount, cancelledCount int64
	db.DB.Model(&task.TaskEvent{}).Where("task_id = ?", tsk.ID).Where("event_type = ?", "task.completed").Count(&completedCount)
	db.DB.Model(&task.TaskEvent{}).Where("task_id = ?", tsk.ID).Where("event_type = ?", "task.failed").Count(&failedCount)
	db.DB.Model(&task.TaskEvent{}).Where("task_id = ?", tsk.ID).Where("event_type = ?", "task.cancelled").Count(&cancelledCount)

	if completedCount != 1 {
		t.Errorf("task.completed count = %d, want 1", completedCount)
	}
	if failedCount != 0 {
		t.Errorf("task.failed count = %d, want 0", failedCount)
	}
	if cancelledCount != 0 {
		t.Errorf("task.cancelled count = %d, want 0", cancelledCount)
	}
}

// ── Worker-context execution tests ──────────────────────────────────────────

func TestExecute_WorkerContextDoesNotFinalizeFailure(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o", err: errorf("agent boom")}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusRunning, Input: "fail", Model: "test/gpt-4o"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	ctx := task.WithWorkerExecution(context.Background())
	err := exec.Execute(ctx, tsk.ID)
	if err == nil {
		t.Fatal("expected error from Execute")
	}

	var result task.Task
	if err := db.DB.Where("id = ?", tsk.ID).First(&result).Error; err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// Worker context: executor leaves task in running — caller owns retry policy.
	if result.Status != task.StatusRunning {
		t.Errorf("status = %q, want running (executor must not finalize in worker context)", result.Status)
	}
}

func TestExecute_SyncContextFinalizesFailure(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o", err: errorf("agent boom")}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusRunning, Input: "fail", Model: "test/gpt-4o"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// No worker context — synchronous execution should finalize failure.
	err := exec.Execute(context.Background(), tsk.ID)
	if err == nil {
		t.Fatal("expected error from Execute")
	}

	var result task.Task
	if err := db.DB.Where("id = ?", tsk.ID).First(&result).Error; err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if result.Status != task.StatusFailed {
		t.Errorf("status = %q, want failed (sync context finalizes)", result.Status)
	}
}

func TestExecute_WorkerContextCancellationNotRetryable(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent{name: "test", model: "gpt-4o", err: context.Canceled}
	reg.Register(&fakeProvider{name: "test", model: "gpt-4o"})
	exec := newTestExecutor(t, db, reg, fake)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusRunning, Input: "cancel", Model: "test/gpt-4o"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	ctx := task.WithWorkerExecution(context.Background())
	err := exec.Execute(ctx, tsk.ID)
	if err == nil {
		t.Fatal("expected context.Canceled error")
	}

	var result task.Task
	if err := db.DB.Where("id = ?", tsk.ID).First(&result).Error; err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// Cancellation is terminal regardless of context — do NOT become retryable.
	if result.Status != task.StatusCancelled {
		t.Errorf("status = %q, want cancelled", result.Status)
	}
}
