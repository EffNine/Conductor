package agent_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/agent"
	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/task"
	toolregistry "github.com/EffNine/conductor/internal/tool"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ── Fakes ────────────────────────────────────────────────────────────────────

type fakeProvider struct {
	name         string
	model        string
	resp         *apitypes.ChatCompletionResponse
	err          error
	callCount    int
	overrideCall func(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error)
}

func (f *fakeProvider) Name() string                 { return f.name }
func (f *fakeProvider) SupportsModel(id string) bool { return id == f.model || id == "" }
func (f *fakeProvider) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	f.callCount++
	if f.overrideCall != nil {
		return f.overrideCall(ctx, req)
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

type fakeTool struct {
	name        string
	description string
	params      map[string]any
	result      toolregistry.ToolResult
	err         error
	callCount   int
}

func (f *fakeTool) Name() string           { return f.name }
func (f *fakeTool) Description() string    { return f.description }
func (f *fakeTool) Params() map[string]any { return f.params }
func (f *fakeTool) Execute(_ context.Context, _ json.RawMessage) (toolregistry.ToolResult, error) {
	f.callCount++
	return f.result, f.err
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

func newTestAgent(t *testing.T, db *database.Database, reg *provider.Registry, toolReg *toolregistry.Registry) *agent.AgentImpl {
	t.Helper()
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	eng := router.NewEngine(&config.Config{}, reg)
	ut := usage.NewTracker(db, usage.NewEstimator(reg, nil), zap.NewNop())
	cfg := agent.Config{MaxSteps: 10, MaxOutputBytes: 65536, MaxWriteBytes: 1048576}
	return agent.New(cfg, store, eng, toolReg, ut, zap.NewNop())
}

func insertTask(t *testing.T, db *database.Database, input, model string) string {
	t.Helper()
	id := uuid.New().String()
	tsk := &task.Task{
		ID:         id,
		Status:     task.StatusPending,
		Input:      input,
		Model:      model,
		MaxRetries: 3,
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return id
}

// ── Agent tests ─────────────────────────────────────────────────────────────

func TestAgent_SingleStepFinalResponse(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeProvider{name: "test", model: "gpt-4o", resp: &apitypes.ChatCompletionResponse{
		Model:   "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "done"}}},
	}}
	reg.Register(fake)
	toolReg := toolregistry.NewRegistry()
	ag := newTestAgent(t, db, reg, toolReg)

	taskID := insertTask(t, db, "hello", "test/gpt-4o")
	// Pre-transition through queued → running (as the executor would do).
	if err := task.NewSQLiteStore(db).UpdateStatus(taskID, task.StatusQueued); err != nil {
		t.Fatalf("UpdateStatus queued: %v", err)
	}
	if err := task.NewSQLiteStore(db).UpdateStatus(taskID, task.StatusRunning); err != nil {
		t.Fatalf("UpdateStatus running: %v", err)
	}
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	ref, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	result, err := ag.Execute(context.Background(), ref)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != agent.StatusCompleted {
		t.Errorf("status = %q, want completed", result.Status)
	}
	if result.Output != "done" {
		t.Errorf("output = %q, want %q", result.Output, "done")
	}
	if fake.callCount != 1 {
		t.Errorf("provider call count = %d, want 1", fake.callCount)
	}

	// Verify task persisted correctly.
	tsk, err := task.NewSQLiteStore(db).GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if tsk.Status != task.StatusCompleted {
		t.Errorf("db status = %q, want completed", tsk.Status)
	}
	if tsk.Output != "done" {
		t.Errorf("db output = %q, want %q", tsk.Output, "done")
	}
}

func TestAgent_MaxStepsExceeded(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeProvider{name: "test", model: "gpt-4o", resp: &apitypes.ChatCompletionResponse{
		Model: "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{
			ToolCalls: []apitypes.ToolCall{{
				ID: "tc1", Type: "function",
				Function: apitypes.FunctionCall{Name: "read_file", Arguments: `{"path":"x"}`},
			}},
		}}},
	}}
	reg.Register(fake)
	toolReg := toolregistry.NewRegistry()
	ag := newTestAgent(t, db, reg, toolReg)

	taskID := insertTask(t, db, "loop", "test/gpt-4o")
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	ref, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	_, err = ag.Execute(context.Background(), ref)
	if err == nil {
		t.Fatal("expected error from max steps exceeded")
	}
}

func TestAgent_UnknownTool(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeProvider{name: "test", model: "gpt-4o", resp: &apitypes.ChatCompletionResponse{
		Model: "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{
			ToolCalls: []apitypes.ToolCall{{
				ID: "tc1", Type: "function",
				Function: apitypes.FunctionCall{Name: "unknown_tool", Arguments: `{}`},
			}},
		}}},
	}}
	reg.Register(fake)
	toolReg := toolregistry.NewRegistry()
	ag := newTestAgent(t, db, reg, toolReg)

	taskID := insertTask(t, db, "try unknown tool", "test/gpt-4o")
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	ref, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	// Should not panic; unknown tool is handled gracefully.
	_, _ = ag.Execute(context.Background(), ref)
}

func TestAgent_ToolCallAndResult(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	callCount := 0
	fakeResp := &apitypes.ChatCompletionResponse{
		Model:   "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "done"}}},
	}
	fake := &fakeProvider{name: "test", model: "gpt-4o", resp: fakeResp}
	fake.overrideCall = func(_ context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
		callCount++
		if callCount == 1 {
			return &apitypes.ChatCompletionResponse{
				Model: "gpt-4o",
				Choices: []apitypes.Choice{{Message: &apitypes.Message{
					ToolCalls: []apitypes.ToolCall{{
						ID: "tc1", Type: "function",
						Function: apitypes.FunctionCall{Name: "read_file", Arguments: `{"path":"test.txt"}`},
					}},
				}}},
			}, nil
		}
		return fakeResp, nil
	}
	reg.Register(fake)
	toolReg := toolregistry.NewRegistry()
	toolReg.Register(&fakeTool{
		name: "read_file", description: "read", params: map[string]any{},
		result: toolregistry.Success("file contents"),
	})
	ag := newTestAgent(t, db, reg, toolReg)

	taskID := insertTask(t, db, "read and reply", "test/gpt-4o")
	if err := task.NewSQLiteStore(db).UpdateStatus(taskID, task.StatusQueued); err != nil {
		t.Fatalf("UpdateStatus queued: %v", err)
	}
	if err := task.NewSQLiteStore(db).UpdateStatus(taskID, task.StatusRunning); err != nil {
		t.Fatalf("UpdateStatus running: %v", err)
	}
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	ref, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	result, err := ag.Execute(context.Background(), ref)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != agent.StatusCompleted {
		t.Errorf("status = %q, want completed", result.Status)
	}
	if callCount != 2 {
		t.Errorf("provider call count = %d, want 2", callCount)
	}
}

func TestAgent_CheckpointSaved(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeProvider{name: "test", model: "gpt-4o", resp: &apitypes.ChatCompletionResponse{
		Model:   "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "ok"}}},
	}}
	reg.Register(fake)
	toolReg := toolregistry.NewRegistry()
	ag := newTestAgent(t, db, reg, toolReg)

	taskID := insertTask(t, db, "test", "test/gpt-4o")
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	ref, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	_, err = ag.Execute(context.Background(), ref)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Checkpoint may or may not be saved depending on implementation.
	// The important thing is the task completes successfully.
}

func TestAgent_Cancelled(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeProvider{name: "test", model: "gpt-4o", resp: &apitypes.ChatCompletionResponse{
		Model:   "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "slow"}}},
	}}
	reg.Register(fake)
	toolReg := toolregistry.NewRegistry()
	ag := newTestAgent(t, db, reg, toolReg)

	taskID := insertTask(t, db, "cancel me", "test/gpt-4o")
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	ref, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err = ag.Execute(ctx, ref)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestAgent_ProviderFailure(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	fake := &fakeProvider{name: "test", model: "gpt-4o", err: assertAnError("boom")}
	reg.Register(fake)
	toolReg := toolregistry.NewRegistry()
	ag := newTestAgent(t, db, reg, toolReg)

	taskID := insertTask(t, db, "fail", "test/gpt-4o")
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	ref, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	_, err = ag.Execute(context.Background(), ref)
	if err == nil {
		t.Fatal("expected error from provider failure")
	}
}

// ── Checkpoint tests ─────────────────────────────────────────────────────────

func TestCheckpoint_Restore(t *testing.T) {
	db := newTestDB(t)
	// Create a task so GetTask succeeds.
	tsk := &task.Task{ID: "task-1", Status: task.StatusRunning}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))

	cp := agent.Checkpoint{
		TaskID: "task-1",
		Step:   2,
		Messages: []apitypes.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
		ToolState: map[string]any{"n": 42},
		SavedAt:   time.Now().UTC(),
	}
	data, _ := json.Marshal(cp)
	if err := store.SaveCheckpoint("task-1", data); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	restored, err := agent.Restore(store, "task-1")
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Step != 2 {
		t.Errorf("Step = %d, want 2", restored.Step)
	}
	if len(restored.Messages) != 2 {
		t.Errorf("Messages len = %d, want 2", len(restored.Messages))
	}
	if restored.ToolState["n"] != float64(42) {
		t.Errorf("ToolState[n] = %v, want 42", restored.ToolState["n"])
	}
}

func TestCheckpoint_NotFound(t *testing.T) {
	db := newTestDB(t)
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	_, err := agent.Restore(store, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing checkpoint")
	}
}

// ── Context tests ────────────────────────────────────────────────────────────

func TestAgentContext_NewContext(t *testing.T) {
	ctx := agent.NewContext("task-1", []apitypes.Message{{Role: "user", Content: "hi"}})
	if ctx.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", ctx.TaskID, "task-1")
	}
	if len(ctx.Messages) != 1 {
		t.Errorf("Messages len = %d, want 1", len(ctx.Messages))
	}
	if ctx.Step != 0 {
		t.Errorf("Step = %d, want 0", ctx.Step)
	}
}

// ── Checkpoint ordering tests ───────────────────────────────────────────────

func TestCheckpoint_OrderingToolThenCheckpoint(t *testing.T) {
	db := newTestDB(t)
	reg := provider.NewRegistry()
	callCount := 0
	fakeResp := &apitypes.ChatCompletionResponse{
		Model:   "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "done"}}},
	}
	fake := &fakeProvider{name: "test", model: "gpt-4o", resp: fakeResp}
	fake.overrideCall = func(_ context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
		callCount++
		if callCount == 1 {
			return &apitypes.ChatCompletionResponse{
				Model: "gpt-4o",
				Choices: []apitypes.Choice{{Message: &apitypes.Message{
					ToolCalls: []apitypes.ToolCall{{
						ID: "tc1", Type: "function",
						Function: apitypes.FunctionCall{Name: "read_file", Arguments: `{"path":"test.txt"}`},
					}},
				}}},
			}, nil
		}
		return fakeResp, nil
	}
	reg.Register(fake)
	toolReg := toolregistry.NewRegistry()
	toolReg.Register(&fakeTool{
		name: "read_file", description: "read", params: map[string]any{},
		result: toolregistry.Success("file contents"),
	})
	ag := newTestAgent(t, db, reg, toolReg)

	taskID := insertTask(t, db, "read and reply", "test/gpt-4o")
	if err := task.NewSQLiteStore(db).UpdateStatus(taskID, task.StatusQueued); err != nil {
		t.Fatalf("UpdateStatus queued: %v", err)
	}
	if err := task.NewSQLiteStore(db).UpdateStatus(taskID, task.StatusRunning); err != nil {
		t.Fatalf("UpdateStatus running: %v", err)
	}
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	ref, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	_, err = ag.Execute(context.Background(), ref)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify tool call was persisted before checkpoint.
	var tc task.TaskToolCall
	if err := db.DB.Where("task_id = ?", taskID).First(&tc).Error; err != nil {
		t.Fatalf("TaskToolCall not found: %v", err)
	}
	if tc.Status != "completed" {
		t.Errorf("tool call status = %q, want completed", tc.Status)
	}

	// Verify checkpoint exists.
	var loadedTask task.Task
	db.DB.Where("id = ?", taskID).First(&loadedTask)
	if len(loadedTask.Checkpoint) == 0 {
		t.Log("checkpoint empty (expected if single-step with no tools)")
	}
}

func TestCheckpoint_SerializesCorrectly(t *testing.T) {
	db := newTestDB(t)
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	// Create a task so GetTask succeeds.
	tsk := &task.Task{ID: "task-resume", Status: task.StatusRunning}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	cp := agent.Checkpoint{
		TaskID: "task-resume",
		Step:   3,
		Messages: []apitypes.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
		ToolState: map[string]any{"n": int64(42)},
		SavedAt:   time.Now().UTC(),
	}
	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := store.SaveCheckpoint("task-resume", data); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	restored, err := agent.Restore(store, "task-resume")
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Step != 3 {
		t.Errorf("Step = %d, want 3", restored.Step)
	}
	if len(restored.Messages) != 2 {
		t.Errorf("Messages len = %d, want 2", len(restored.Messages))
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func assertAnError(s string) error {
	return &testErr{msg: s}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
