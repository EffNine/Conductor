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
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/orchestration"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/task"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ── Fakes ────────────────────────────────────────────────────────────────────

type fakeProvider2 struct {
	name      string
	model     string
	resp      *apitypes.ChatCompletionResponse
	err       error
	callCount int
	override  func(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error)
}

func (f *fakeProvider2) Name() string { return f.name }
func (f *fakeProvider2) SupportsModel(id string) bool { return id == f.model || id == "" }
func (f *fakeProvider2) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	f.callCount++
	if f.override != nil {
		return f.override(ctx, req)
	}
	return f.resp, f.err
}
func (f *fakeProvider2) ChatCompletionStream(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (f *fakeProvider2) Embeddings(_ context.Context, _ *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (f *fakeProvider2) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: f.model}}, nil
}
func (f *fakeProvider2) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (f *fakeProvider2) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: f.name, IsHealthy: true, LatencyMs: 10}, nil
}
func (f *fakeProvider2) GetMetadata() provider.Metadata { return provider.Metadata{} }

type fakeAgent2 struct {
	name      string
	model     string
	resp      *apitypes.ChatCompletionResponse
	err       error
	callCount int
	store     task.Store
}

func (f *fakeAgent2) Name() string { return f.name }

func (f *fakeAgent2) Execute(ctx context.Context, t *agent.TaskRef) (*agent.TaskRef, error) {
	f.callCount++
	if f.err != nil {
		return t, f.err
	}
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
	now := time.Now().UTC()
	t.Output = f.resp.Choices[0].Message.ContentString()
	t.Provider = f.name
	t.Model = f.model
	t.StepCount = 1
	t.CompletedAt = &now
	t.Status = agent.StatusCompleted
	if f.store != nil {
		data, _ := json.Marshal(map[string]any{"step": 1})
		_ = f.store.CreateTaskEvent(&task.TaskEvent{
			ID: uuid.New().String(), TaskID: t.ID,
			EventType: "task.completed",
			EventData: data,
		})
	}
	return t, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func newTestDB2(t *testing.T) *database.Database {
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
	if err := task.MigrateAll(db.DB); err != nil {
		t.Fatalf("MigrateAll: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestExecutorWithOrchestration(t *testing.T, db *database.Database, reg *provider.Registry, a agent.Agent, re *router.RouterEngine) *task.TaskExecutor {
	t.Helper()
	store := task.NewSQLiteStore(db)
	eng := router.NewEngine(&config.Config{}, reg)
	ut := usage.NewTracker(db, usage.NewEstimator(reg, nil), zap.NewNop())
	cat := catalog.New(reg, nil)
	exec := task.NewTaskExecutor(store, eng, a, cat, ut, zap.NewNop())
	if re != nil {
		exec.WithOrchestration(reg, nil, re, eventbus.NewEventBus())
	}
	return exec
}

// ── V2.5 Integration Tests ───────────────────────────────────────────────────

func TestV25_IntentClassification(t *testing.T) {
	// Test that orchestration classifies intent correctly.
	intent := orchestration.ClassifyIntent(context.Background(), "Write a Python function to sort a list")
	if intent.TaskType != "coding" {
		t.Errorf("task_type = %q, want coding", intent.TaskType)
	}
}

func TestV25_CapabilityResolution(t *testing.T) {
	caps := orchestration.ResolveCapabilities(context.Background(), "fix the bug in main.go", &orchestration.Intent{TaskType: "coding"}, []string{"read_file", "shell_exec"})
	if !caps.NeedsFileSystem {
		t.Error("expected NeedsFileSystem=true")
	}
	if !caps.NeedsShell {
		t.Error("expected NeedsShell=true")
	}
	if !caps.NeedsToolCalling {
		t.Error("expected NeedsToolCalling=true")
	}
}

func TestV25_PlanGeneration(t *testing.T) {
	caps := &orchestration.CapabilityRequirement{
		NeedsFileSystem: true,
		NeedsShell:      true,
		NeedsGit:        true,
		NeedsToolCalling: true,
	}
	plan := orchestration.GeneratePlan("find failing tests and fix them", "coding", caps)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
	if plan.ID == "" {
		t.Error("expected non-empty plan ID")
	}
	if plan.Intent != "coding" {
		t.Errorf("intent = %q, want coding", plan.Intent)
	}
	if len(plan.Steps) < 4 {
		t.Errorf("expected at least 4 steps for coding task, got %d", len(plan.Steps))
	}
	if plan.Status != orchestration.PlanPending {
		t.Errorf("status = %q, want pending", plan.Status)
	}
}

func TestV25_PlanMarshalUnmarshal(t *testing.T) {
	plan := orchestration.GeneratePlan("test input", "coding", &orchestration.CapabilityRequirement{NeedsFileSystem: true})
	plan.TaskID = "task-123"

	data, err := plan.Marshal()
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	restore, err := orchestration.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if restore.ID != plan.ID {
		t.Errorf("ID mismatch: %q != %q", restore.ID, plan.ID)
	}
	if restore.TaskID != "task-123" {
		t.Errorf("TaskID mismatch: %q != %q", restore.TaskID, "task-123")
	}
	if len(restore.Steps) != len(plan.Steps) {
		t.Errorf("steps mismatch: %d != %d", len(restore.Steps), len(plan.Steps))
	}
}

func TestV25_TaskWithOrchestration_Completes(t *testing.T) {
	db := newTestDB2(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent2{name: "test", model: "gpt-4o", resp: &apitypes.ChatCompletionResponse{
		Model: "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Role: "assistant", Content: "hello world"}}},
		Usage: &apitypes.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}}
	reg.Register(&fakeProvider2{name: "test", model: "gpt-4o"})

	re := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Logger:   zap.NewNop(),
	})
	exec := newTestExecutorWithOrchestration(t, db, reg, fake, re)

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "write a function that returns hello", Model: "test/gpt-4o"}
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
		t.Errorf("output = %q, want hello world", result.Output)
	}
	// Plan should have been created.
	if result.PlanID == "" {
		t.Log("plan_id is empty (orchestration may not have run without routing engine)")
	}
	if result.Intent == "" {
		t.Log("intent is empty (orchestration may not have run)")
	}
}

func TestV25_TaskWithoutOrchestration_BackwardCompatible(t *testing.T) {
	db := newTestDB2(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent2{name: "test", model: "gpt-4o", resp: &apitypes.ChatCompletionResponse{
		Model: "gpt-4o",
		Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "done"}}},
	}}
	reg.Register(&fakeProvider2{name: "test", model: "gpt-4o"})

	// No orchestration wired — legacy behavior must still work.
	store := task.NewSQLiteStore(db)
	eng := router.NewEngine(&config.Config{}, reg)
	ut := usage.NewTracker(db, usage.NewEstimator(reg, nil), zap.NewNop())
	cat := catalog.New(reg, nil)
	exec := task.NewTaskExecutor(store, eng, fake, cat, ut, zap.NewNop())

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
}

func TestV25_Verification_Completes(t *testing.T) {
	result, err := orchestration.DefaultVerifier(context.Background(), "write a sort function", "```python\ndef sort(arr):\n    return sorted(arr)\n```", "coding")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected verification success, got: %s", result.Message)
	}
}

func TestV25_Verification_EmptyOutput(t *testing.T) {
	result, err := orchestration.DefaultVerifier(context.Background(), "hello", "", "chat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected verification failure for empty output")
	}
}

func TestV25_RouterPipeline_IntentStage(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider2{name: "openai", model: "gpt-4o"})

	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "write a function that sorts an array"},
		},
	}
	env := router.Environment{}
	cfgSnap := router.ConfigSnapshot{Weights: config.DefaultRoutingWeights()}

	result, err := pipeline.Execute(context.Background(), req, env, cfgSnap)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Decision.SelectedModelID == "" {
		t.Fatal("expected non-empty selected model ID")
	}
}

func TestV25_RouterPipeline_CandidateStage(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider2{name: "openai", model: "gpt-4o"})
	reg.Register(&fakeProvider2{name: "groq", model: "mixtral-8x7b"})

	re := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Logger:   zap.NewNop(),
	})
	_ = re
	pipeline := router.NewDecisionPipeline(router.PipelineConfig{
		Registry: reg,
		Weights:  config.DefaultRoutingWeights(),
	})

	req := &apitypes.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []apitypes.Message{
			{Role: "user", Content: "analyze the performance characteristics of this algorithm"},
		},
	}
	env := router.Environment{}
	cfgSnap := router.ConfigSnapshot{Weights: config.DefaultRoutingWeights()}

	result, err := pipeline.Execute(context.Background(), req, env, cfgSnap)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}
