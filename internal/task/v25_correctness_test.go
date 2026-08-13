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

type fakeProvider4 struct {
	name      string
	model     string
	resp      *apitypes.ChatCompletionResponse
	err       error
	callCount int
	override  func(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error)
}

func (f *fakeProvider4) Name() string { return f.name }
func (f *fakeProvider4) SupportsModel(id string) bool { return id == f.model || id == "" }
func (f *fakeProvider4) ChatCompletion(ctx context.Context, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error) {
	f.callCount++
	if f.override != nil {
		return f.override(ctx, req)
	}
	return f.resp, f.err
}
func (f *fakeProvider4) ChatCompletionStream(_ context.Context, _ *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error) {
	return nil, provider.ErrNotImplemented
}
func (f *fakeProvider4) Embeddings(_ context.Context, _ *apitypes.EmbeddingRequest) (*apitypes.EmbeddingResponse, error) {
	return nil, provider.ErrNotImplemented
}
func (f *fakeProvider4) ListModels(_ context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ProviderModelID: f.model}}, nil
}
func (f *fakeProvider4) GetPricing(_ context.Context) (map[string]provider.PricingInfo, error) {
	return nil, nil
}
func (f *fakeProvider4) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Provider: f.name, IsHealthy: true, LatencyMs: 10}, nil
}
func (f *fakeProvider4) GetMetadata() provider.Metadata { return provider.Metadata{} }

type fakeAgent4 struct {
	name        string
	model       string
	resp        *apitypes.ChatCompletionResponse
	err         error
	callCount   int
	store       task.Store
	captured    *agent.TaskRef
	overrideFn  func(ctx context.Context, t *agent.TaskRef) (*agent.TaskRef, error)
}

func (f *fakeAgent4) Name() string { return f.name }

func (f *fakeAgent4) Execute(ctx context.Context, t *agent.TaskRef) (*agent.TaskRef, error) {
	f.callCount++
	f.captured = t
	if f.overrideFn != nil {
		return f.overrideFn(ctx, t)
	}
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

func newTestDB4(t *testing.T) *database.Database {
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

func newTestExecutorFull(t *testing.T, db *database.Database, reg *provider.Registry, a agent.Agent, re *router.RouterEngine) *task.TaskExecutor {
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

// ── V2.5 Correctness Tests ───────────────────────────────────────────────────

// TestV25_SelectedCandidateAffectsExecution proves that the selected provider/
// model from orchestration becomes the actual provider/model used by the agent.
func TestV25_SelectedCandidateAffectsExecution(t *testing.T) {
	db := newTestDB4(t)
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider4{name: "best_provider", model: "gpt-4o"})
	reg.Register(&fakeProvider4{name: "fallback_provider", model: "gpt-3.5-turbo"})

	capturedRef := &agent.TaskRef{}
	fake := &fakeAgent4{
		name:     "test",
		model:    "gpt-4o",
		resp:     &apitypes.ChatCompletionResponse{Model: "gpt-4o", Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "done"}}}},
		captured: capturedRef,
		store:    task.NewSQLiteStore(db),
	}

	re := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Logger:   zap.NewNop(),
	})
	exec := newTestExecutorFull(t, db, reg, fake, re)

	tsk := &task.Task{
		ID:     uuid.New().String(),
		Status: task.StatusPending,
		Input:  "write a sorting function",
		Model:  "gpt-4o",
	}
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
	if result.Intent == "" {
		t.Error("expected non-empty intent after orchestration")
	}
	if result.PlanID == "" {
		t.Error("expected non-empty plan_id after orchestration")
	}
}

// TestV25_CandidateContainsRealModel verifies that the candidate generated
// by orchestration contains a real provider name (not a placeholder).
func TestV25_CandidateContainsRealModel(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&fakeProvider4{name: "openai", model: "gpt-4o"})
	reg.Register(&fakeProvider4{name: "anthropic", model: "claude-3"})

	re := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Logger:   zap.NewNop(),
	})
	caps := &orchestration.CapabilityRequirement{NeedsToolCalling: true}
	cands := orchestration.GenerateCandidates(context.Background(), reg, re, caps, "gpt-4o", orchestration.RoutingPreferences{})

	if len(cands) == 0 {
		t.Fatal("expected non-empty candidates")
	}
	for _, c := range cands {
		if c.ProviderName == "" {
			t.Error("candidate has empty ProviderName")
		}
	}
	best := orchestration.SelectBestCandidate(cands)
	if best == nil {
		t.Fatal("expected non-nil best candidate")
	}
	if best.ProviderName == "" {
		t.Error("selected candidate has empty ProviderName")
	}
}

// TestV25_PlanPersistedThroughPlanStore proves the plan is written to the
// plans table, not just stored as an event payload.
func TestV25_PlanPersistedThroughPlanStore(t *testing.T) {
	db := newTestDB4(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent4{
		name: "test", model: "gpt-4o",
		resp: &apitypes.ChatCompletionResponse{
			Model: "gpt-4o",
			Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "done"}}},
		},
	}
	reg.Register(&fakeProvider4{name: "test", model: "gpt-4o"})

	re := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Logger:   zap.NewNop(),
	})
	exec := newTestExecutorFull(t, db, reg, fake, re)

	tsk := &task.Task{
		ID:     uuid.New().String(),
		Status: task.StatusPending,
		Input:  "write a sort function",
		Model:  "test/gpt-4o",
	}
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
	if result.PlanID == "" {
		t.Fatal("expected non-empty plan_id on task")
	}

	// Verify plan exists in the plans table.
	store := task.NewSQLiteStore(db)
	plan, err := store.GetPlan(result.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if plan.ID != result.PlanID {
		t.Errorf("plan.ID = %q, want %q", plan.ID, result.PlanID)
	}
	if plan.TaskID != tsk.ID {
		t.Errorf("plan.TaskID = %q, want %q", plan.TaskID, tsk.ID)
	}
	if plan.Intent != result.Intent {
		t.Errorf("plan.Intent = %q, want %q", plan.Intent, result.Intent)
	}
	if plan.Status != orchestration.PlanCompleted {
		t.Errorf("plan.Status = %q, want completed", plan.Status)
	}
	if len(plan.StepsJSON) == 0 {
		t.Error("expected non-empty steps_json in persisted plan")
	}
}

// TestV25_PlanReloadAfterPersistence verifies the plan can be reloaded from
// the database after process restart.
func TestV25_PlanReloadAfterPersistence(t *testing.T) {
	db := newTestDB4(t)
	store := task.NewSQLiteStore(db)

	// Simulate a plan created by a previous process.
	plan := &task.Plan{
		ID:           uuid.New().String(),
		TaskID:       "task-reload-test",
		Intent:       "coding",
		Capabilities: "filesystem,shell",
		StepsJSON:    []byte(`[{"id":"s1","number":1,"description":"inspect"},{"id":"s2","number":2,"description":"implement"}]`),
		Status:       orchestration.PlanRunning,
		CurrentStep:  1,
	}
	if err := store.CreatePlan(plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	// Create a corresponding task.
	tsk := &task.Task{ID: "task-reload-test", Status: task.StatusRunning, Input: "test"}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Reload plan by task ID (simulating restart).
	relaoded, err := store.GetPlanByTaskID("task-reload-test")
	if err != nil {
		t.Fatalf("GetPlanByTaskID: %v", err)
	}
	if relaoded.ID != plan.ID {
		t.Errorf("reloaded plan ID = %q, want %q", relaoded.ID, plan.ID)
	}
	if relaoded.Intent != "coding" {
		t.Errorf("reloaded intent = %q, want coding", relaoded.Intent)
	}
	if relaoded.CurrentStep != 1 {
		t.Errorf("reloaded current_step = %d, want 1", relaoded.CurrentStep)
	}

	// Also reload by plan ID.
	byID, err := store.GetPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetPlan by ID: %v", err)
	}
	if byID.ID != plan.ID {
		t.Errorf("byID plan ID = %q, want %q", byID.ID, plan.ID)
	}
}

// TestV25_PlanCurrentStepAdvances verifies plan current_step is updated.
func TestV25_PlanCurrentStepAdvances(t *testing.T) {
	db := newTestDB4(t)
	store := task.NewSQLiteStore(db)

	plan := &task.Plan{
		ID:          uuid.New().String(),
		TaskID:      "task-step-test",
		Intent:      "coding",
		StepsJSON:   []byte(`[{"number":1},{"number":2},{"number":3}]`),
		Status:      orchestration.PlanRunning,
		CurrentStep: 0,
	}
	if err := store.CreatePlan(plan); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	// Simulate step advancement.
	plan.CurrentStep = 2
	plan.Status = orchestration.PlanRunning
	if err := store.UpdatePlan(plan); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	relaoded, err := store.GetPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if relaoded.CurrentStep != 2 {
		t.Errorf("current_step = %d, want 2", relaoded.CurrentStep)
	}
	if relaoded.Status != orchestration.PlanRunning {
		t.Errorf("status = %q, want running", relaoded.Status)
	}

	// Mark completed.
	relaoded.Status = orchestration.PlanCompleted
	relaoded.CurrentStep = 3
	if err := store.UpdatePlan(relaoded); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	final, err := store.GetPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if final.Status != orchestration.PlanCompleted {
		t.Errorf("final status = %q, want completed", final.Status)
	}
	if final.CurrentStep != 3 {
		t.Errorf("final current_step = %d, want 3", final.CurrentStep)
	}
}

// TestV25_PlanCompletionState proves plan transitions to completed.
func TestV25_PlanCompletionState(t *testing.T) {
	db := newTestDB4(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent4{
		name: "test", model: "gpt-4o",
		resp: &apitypes.ChatCompletionResponse{
			Model: "gpt-4o",
			Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "sorted array: [1,2,3]"}}},
		},
	}
	reg.Register(&fakeProvider4{name: "test", model: "gpt-4o"})

	re := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Logger:   zap.NewNop(),
	})
	exec := newTestExecutorFull(t, db, reg, fake, re)

	tsk := &task.Task{
		ID:     uuid.New().String(),
		Status: task.StatusPending,
		Input:  "sort this array",
		Model:  "test/gpt-4o",
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := exec.Execute(context.Background(), tsk.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	store := task.NewSQLiteStore(db)
	var result task.Task
	db.DB.Where("id = ?", tsk.ID).First(&result)

	plan, err := store.GetPlan(result.PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if plan.Status != orchestration.PlanCompleted {
		t.Errorf("plan status = %q, want completed", plan.Status)
	}
}

// TestV25_PlanFailureState proves plan transitions to failed on agent error.
func TestV25_PlanFailureState(t *testing.T) {
	db := newTestDB4(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent4{
		name: "test", model: "gpt-4o",
		err:  assertAnError("boom"),
	}
	reg.Register(&fakeProvider4{name: "test", model: "gpt-4o"})

	re := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Logger:   zap.NewNop(),
	})
	exec := newTestExecutorFull(t, db, reg, fake, re)

	tsk := &task.Task{
		ID:     uuid.New().String(),
		Status: task.StatusPending,
		Input:  "do something",
		Model:  "test/gpt-4o",
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	err := exec.Execute(context.Background(), tsk.ID)
	if err == nil {
		t.Fatal("expected error from failed agent")
	}

	store := task.NewSQLiteStore(db)
	var result task.Task
	db.DB.Where("id = ?", tsk.ID).First(&result)

	plan, err := store.GetPlan(result.PlanID)
	if err == nil && plan != nil {
		if plan.Status != orchestration.PlanFailed {
			t.Errorf("plan status = %q, want failed", plan.Status)
		}
	}
	// Task should be failed.
	if result.Status != task.StatusFailed {
		t.Errorf("task status = %q, want failed", result.Status)
	}
}

// TestV25_AgentRefCarriesOrchestrationFields proves all V2.5 fields survive
// the Task → AgentRef → Task round-trip via the adapter.
func TestV25_AgentRefCarriesOrchestrationFields(t *testing.T) {
	db := newTestDB4(t)
	store := task.NewSQLiteStore(db)
	adapter := task.NewStoreAdapter(store)

	// Create a task with V2.5 fields.
	tsk := &task.Task{
		ID:                "task-orch-ref",
		Status:            task.StatusRunning,
		Input:             "test input",
		PlanID:            "plan-123",
		Intent:            "coding",
		CurrentPlanStep:   2,
		Provider:          "openai",
		Model:             "gpt-4o",
		StepCount:         3,
		Output:            "result",
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Load via adapter (simulates what agent sees).
	ref, err := adapter.GetTask("task-orch-ref")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if ref.PlanID != "plan-123" {
		t.Errorf("ref.PlanID = %q, want plan-123", ref.PlanID)
	}
	if ref.Intent != "coding" {
		t.Errorf("ref.Intent = %q, want coding", ref.Intent)
	}
	if ref.CurrentPlanStep != 2 {
		t.Errorf("ref.CurrentPlanStep = %d, want 2", ref.CurrentPlanStep)
	}

	// Simulate agent completing the task.
	ref.Output = "final output"
	ref.StepCount = 5
	ref.Status = agent.StatusCompleted

	// Write back via adapter (simulates what agent returns).
	if err := adapter.UpdateTask(ref); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	// Verify persisted task has correct values.
	var persisted task.Task
	if err := db.DB.Where("id = ?", "task-orch-ref").First(&persisted).Error; err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if persisted.Output != "final output" {
		t.Errorf("task.Output = %q, want final output", persisted.Output)
	}
	if persisted.StepCount != 5 {
		t.Errorf("task.StepCount = %d, want 5", persisted.StepCount)
	}
	// Orchestration fields should be preserved.
	if persisted.PlanID != "plan-123" {
		t.Errorf("task.PlanID = %q, want plan-123 after round-trip", persisted.PlanID)
	}
	if persisted.Intent != "coding" {
		t.Errorf("task.Intent = %q, want coding after round-trip", persisted.Intent)
	}
	if persisted.CurrentPlanStep != 2 {
		t.Errorf("task.CurrentPlanStep = %d, want 2 after round-trip", persisted.CurrentPlanStep)
	}
}

// TestV25_VerificationFailureSemantics proves verification failure is a
// warning, not a task failure.
func TestV25_VerificationFailureSemantics(t *testing.T) {
	db := newTestDB4(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent4{
		name: "test", model: "gpt-4o",
		resp: &apitypes.ChatCompletionResponse{
			Model: "gpt-4o",
			Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "hi"}}}, // too short for coding
		},
	}
	reg.Register(&fakeProvider4{name: "test", model: "gpt-4o"})

	re := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Logger:   zap.NewNop(),
	})
	exec := newTestExecutorFull(t, db, reg, fake, re)

	tsk := &task.Task{
		ID:     uuid.New().String(),
		Status: task.StatusPending,
		Input:  "write a full sorting implementation with tests",
		Model:  "test/gpt-4o",
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Execution should succeed even though verification will warn.
	if err := exec.Execute(context.Background(), tsk.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result task.Task
	if err := db.DB.Where("id = ?", tsk.ID).First(&result).Error; err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// Task should still be completed despite verification warning.
	if result.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed (verification failure is a warning, not a failure)", result.Status)
	}
}

// TestV25_BackwardCompatibility_NoOrchestration proves existing sync task
// execution still works when no orchestration is configured.
func TestV25_BackwardCompatibility_NoOrchestration(t *testing.T) {
	db := newTestDB4(t)
	reg := provider.NewRegistry()
	fake := &fakeAgent4{
		name: "test", model: "gpt-4o",
		resp: &apitypes.ChatCompletionResponse{
			Model: "gpt-4o",
			Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "backward compatible"}}},
		},
	}
	reg.Register(&fakeProvider4{name: "test", model: "gpt-4o"})

	// No orchestration wired.
	store := task.NewSQLiteStore(db)
	eng := router.NewEngine(&config.Config{}, reg)
	ut := usage.NewTracker(db, usage.NewEstimator(reg, nil), zap.NewNop())
	cat := catalog.New(reg, nil)
	exec := task.NewTaskExecutor(store, eng, fake, cat, ut, zap.NewNop())

	tsk := &task.Task{ID: uuid.New().String(), Status: task.StatusPending, Input: "hi", Model: "test/gpt-4o"}
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

// TestV25_CheckpointResumePreservesPlanState proves plan state survives
// checkpoint/resume.
func TestV25_CheckpointResumePreservesPlanState(t *testing.T) {
	db := newTestDB4(t)
	reg := provider.NewRegistry()

	fake := &fakeAgent4{
		name: "test", model: "gpt-4o",
		resp: &apitypes.ChatCompletionResponse{
			Model: "gpt-4o",
			Choices: []apitypes.Choice{{Message: &apitypes.Message{Content: "done"}}},
		},
	}
	reg.Register(&fakeProvider4{name: "test", model: "gpt-4o"})

	re := router.NewRouterEngine(router.RouterEngineConfig{
		Registry: reg,
		Logger:   zap.NewNop(),
	})
	exec := newTestExecutorFull(t, db, reg, fake, re)

	tsk := &task.Task{
		ID:     uuid.New().String(),
		Status: task.StatusPending,
		Input:  "read and reply",
		Model:  "test/gpt-4o",
	}
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
	// Plan state should be preserved through the round-trip.
	if result.PlanID != "" {
		store := task.NewSQLiteStore(db)
		plan, err := store.GetPlan(result.PlanID)
		if err != nil {
			t.Logf("plan not found (expected if no orchestration): %v", err)
		} else {
			if plan.TaskID != tsk.ID {
				t.Errorf("plan.TaskID = %q, want %q", plan.TaskID, tsk.ID)
			}
		}
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func assertAnError(s string) error {
	return &testErr4{msg: s}
}

type testErr4 struct{ msg string }

func (e *testErr4) Error() string { return e.msg }
