package task_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/task"
	"github.com/google/uuid"
)

func openTestDB(t *testing.T) *database.Database {
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

func newStore(t *testing.T, db *database.Database) task.Store {
	t.Helper()
	return task.NewSQLiteStore(db)
}

func newTask(id, input string) *task.Task {
	return &task.Task{
		ID:         id,
		Status:     task.StatusPending,
		Input:      input,
		Priority:   0,
		MaxRetries: 3,
	}
}

// ── Task CRUD ────────────────────────────────────────────────────────────────

func TestCreateTask(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	tsk := newTask(uuid.New().String(), "write a poem")
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := store.GetTask(tsk.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.ID != tsk.ID {
		t.Errorf("ID = %q, want %q", got.ID, tsk.ID)
	}
	if got.Input != "write a poem" {
		t.Errorf("Input = %q, want %q", got.Input, "write a poem")
	}
	if got.Status != task.StatusPending {
		t.Errorf("Status = %q, want %q", got.Status, task.StatusPending)
	}
	if got.RootID != tsk.ID {
		t.Errorf("RootID = %q, want %q", got.RootID, tsk.ID)
	}
}

func TestCreateTask_RequiresID(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)
	if err := store.CreateTask(&task.Task{Status: task.StatusPending}); err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestGetTask_NotFound(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)
	_, err := store.GetTask("nonexistent")
	if err != task.ErrTaskNotFound {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestUpdateTask(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	tsk := newTask(uuid.New().String(), "original")
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	tsk.Input = "updated input"
	tsk.Provider = "openai"
	tsk.Model = "gpt-4o"
	tsk.StepCount = 42
	if err := store.UpdateTask(tsk); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	got, err := store.GetTask(tsk.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Input != "updated input" {
		t.Errorf("Input = %q, want %q", got.Input, "updated input")
	}
	if got.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", got.Provider, "openai")
	}
	if got.StepCount != 42 {
		t.Errorf("StepCount = %d, want 42", got.StepCount)
	}
}

func TestDeleteTask(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	tsk := newTask(uuid.New().String(), "delete me")
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := store.DeleteTask(tsk.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	_, err := store.GetTask(tsk.ID)
	if err != task.ErrTaskNotFound {
		t.Fatalf("expected ErrTaskNotFound after delete, got %v", err)
	}
}

// ── Listing ─────────────────────────────────────────────────────────────────

func TestListTasks(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	for i := 0; i < 5; i++ {
		tsk := newTask(uuid.New().String(), "task-"+string(rune('0'+i)))
		if err := store.CreateTask(tsk); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
	}

	tasks, err := store.ListTasks(3, 0)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("len(tasks) = %d, want 3", len(tasks))
	}
}

func TestListTasksByStatus(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	for _, s := range []task.Status{task.StatusPending, task.StatusRunning, task.StatusPending} {
		tsk := newTask(uuid.New().String(), "task")
		tsk.Status = s
		if err := store.CreateTask(tsk); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
	}

	pending, err := store.ListTasksByStatus(task.StatusPending, 10, 0)
	if err != nil {
		t.Fatalf("ListTasksByStatus: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("len(pending) = %d, want 2", len(pending))
	}

	running, err := store.ListTasksByStatus(task.StatusRunning, 10, 0)
	if err != nil {
		t.Fatalf("ListTasksByStatus: %v", err)
	}
	if len(running) != 1 {
		t.Errorf("len(running) = %d, want 1", len(running))
	}
}

// ── Status transitions ───────────────────────────────────────────────────────

func TestUpdateStatus_ValidTransitions(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	// pending → queued → running → completed
	tsk := newTask(uuid.New().String(), "transition test")
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := store.UpdateStatus(tsk.ID, task.StatusQueued); err != nil {
		t.Fatalf("pending→queued: %v", err)
	}
	got, _ := store.GetTask(tsk.ID)
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}

	if err := store.UpdateStatus(tsk.ID, task.StatusRunning); err != nil {
		t.Fatalf("queued→running: %v", err)
	}
	got, _ = store.GetTask(tsk.ID)
	if got.Status != task.StatusRunning {
		t.Errorf("status = %q, want running", got.Status)
	}
	if got.StartedAt == nil || got.StartedAt.IsZero() {
		t.Error("StartedAt should be set when transitioning to running")
	}

	if err := store.UpdateStatus(tsk.ID, task.StatusCompleted); err != nil {
		t.Fatalf("running→completed: %v", err)
	}
	got, _ = store.GetTask(tsk.ID)
	if got.Status != task.StatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Error("CompletedAt should be set when transitioning to completed")
	}
}

func TestUpdateStatus_RunningToPausedAndBack(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	tsk := newTask(uuid.New().String(), "pause test")
	tsk.Status = task.StatusRunning
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := store.UpdateStatus(tsk.ID, task.StatusPaused); err != nil {
		t.Fatalf("running→paused: %v", err)
	}
	got, _ := store.GetTask(tsk.ID)
	if got.Status != task.StatusPaused {
		t.Errorf("status = %q, want paused", got.Status)
	}

	if err := store.UpdateStatus(tsk.ID, task.StatusQueued); err != nil {
		t.Fatalf("paused→queued: %v", err)
	}
	got, _ = store.GetTask(tsk.ID)
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
}

func TestUpdateStatus_FailedToQueued(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	tsk := newTask(uuid.New().String(), "retry test")
	tsk.Status = task.StatusFailed
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := store.UpdateStatus(tsk.ID, task.StatusQueued); err != nil {
		t.Fatalf("failed→queued: %v", err)
	}
	got, _ := store.GetTask(tsk.ID)
	if got.Status != task.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
}

func TestUpdateStatus_InvalidTransition(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	tsk := newTask(uuid.New().String(), "invalid test")
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// pending → completed is invalid
	err := store.UpdateStatus(tsk.ID, task.StatusCompleted)
	if err == nil {
		t.Fatal("expected error for pending→completed")
	}
	if !task.IsTransitionError(err) {
		t.Errorf("expected TransitionError, got %T: %v", err, err)
	}

	// verify status unchanged
	got, _ := store.GetTask(tsk.ID)
	if got.Status != task.StatusPending {
		t.Errorf("status = %q, want unchanged pending", got.Status)
	}
}

func TestUpdateStatus_NotFound(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)
	err := store.UpdateStatus("nonexistent", task.StatusQueued)
	if err != task.ErrTaskNotFound {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

// ── Checkpoint ───────────────────────────────────────────────────────────────

func TestSaveCheckpoint(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	tsk := newTask(uuid.New().String(), "checkpoint test")
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	data := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	if err := store.SaveCheckpoint(tsk.ID, data); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	got, err := store.GetTask(tsk.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(got.Checkpoint) != len(data) {
		t.Errorf("checkpoint len = %d, want %d", len(got.Checkpoint), len(data))
	}
	if string(got.Checkpoint) != string(data) {
		t.Errorf("checkpoint content mismatch")
	}
}

// ── Retry ────────────────────────────────────────────────────────────────────

func TestIncrementRetry(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	tsk := newTask(uuid.New().String(), "retry test")
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	count, err := store.IncrementRetry(tsk.ID)
	if err != nil {
		t.Fatalf("IncrementRetry: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	count, err = store.IncrementRetry(tsk.ID)
	if err != nil {
		t.Fatalf("IncrementRetry: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestIncrementRetry_NotFound(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)
	_, err := store.IncrementRetry("nonexistent")
	if err != task.ErrTaskNotFound {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

// ── TaskStep persistence ─────────────────────────────────────────────────────

func TestTaskStepPersistence(t *testing.T) {
	db := openTestDB(t)
	// Access the raw db for step inserts (no dedicated store method yet).
	if err := db.DB.AutoMigrate(&task.TaskStep{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	tsk := newTask(uuid.New().String(), "step test")
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	reqJSON, _ := json.Marshal(map[string]string{"model": "gpt-4o"})
	respJSON, _ := json.Marshal(map[string]string{"content": "hello"})

	step := task.TaskStep{
		ID:               uuid.New().String(),
		TaskID:           tsk.ID,
		StepNumber:       1,
		Provider:         "openai",
		Model:            "gpt-4o",
		Request:          reqJSON,
		Response:         respJSON,
		Status:           "completed",
		LatencyMs:        1200,
		PromptTokens:     10,
		CompletionTokens: 20,
		TotalTokens:      30,
	}
	if err := db.DB.Create(&step).Error; err != nil {
		t.Fatalf("Create TaskStep: %v", err)
	}

	var loaded task.TaskStep
	if err := db.DB.Where("id = ?", step.ID).First(&loaded).Error; err != nil {
		t.Fatalf("Get TaskStep: %v", err)
	}
	if loaded.TaskID != tsk.ID {
		t.Errorf("TaskID = %q, want %q", loaded.TaskID, tsk.ID)
	}
	if loaded.StepNumber != 1 {
		t.Errorf("StepNumber = %d, want 1", loaded.StepNumber)
	}
	if loaded.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", loaded.PromptTokens)
	}
	if loaded.LatencyMs != 1200 {
		t.Errorf("LatencyMs = %d, want 1200", loaded.LatencyMs)
	}
}

// ── TaskEvent persistence ────────────────────────────────────────────────────

func TestTaskEventPersistence(t *testing.T) {
	db := openTestDB(t)
	if err := db.DB.AutoMigrate(&task.TaskEvent{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	eventData, _ := json.Marshal(map[string]string{"from": "pending", "to": "queued"})
	evt := task.TaskEvent{
		ID:        uuid.New().String(),
		TaskID:    "task-1",
		EventType: "status_changed",
		EventData: eventData,
	}
	if err := db.DB.Create(&evt).Error; err != nil {
		t.Fatalf("Create TaskEvent: %v", err)
	}

	var loaded task.TaskEvent
	if err := db.DB.Where("id = ?", evt.ID).First(&loaded).Error; err != nil {
		t.Fatalf("Get TaskEvent: %v", err)
	}
	if loaded.EventType != "status_changed" {
		t.Errorf("EventType = %q, want %q", loaded.EventType, "status_changed")
	}
}

// ── TaskToolCall persistence ─────────────────────────────────────────────────

func TestTaskToolCallPersistence(t *testing.T) {
	db := openTestDB(t)
	if err := db.DB.AutoMigrate(&task.TaskToolCall{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	argsJSON, _ := json.Marshal(map[string]string{"path": "/etc/hosts"})
	tc := task.TaskToolCall{
		ID:        uuid.New().String(),
		TaskID:    "task-1",
		StepID:    strPtr("step-1"),
		CallID:    "call-abc",
		ToolName:  "read_file",
		Arguments: argsJSON,
		Result:    strPtr("root:x:0:0:"),
		Status:    "completed",
	}
	if err := db.DB.Create(&tc).Error; err != nil {
		t.Fatalf("Create TaskToolCall: %v", err)
	}

	var loaded task.TaskToolCall
	if err := db.DB.Where("id = ?", tc.ID).First(&loaded).Error; err != nil {
		t.Fatalf("Get TaskToolCall: %v", err)
	}
	if loaded.ToolName != "read_file" {
		t.Errorf("ToolName = %q, want %q", loaded.ToolName, "read_file")
	}
	if loaded.CallID != "call-abc" {
		t.Errorf("CallID = %q, want %q", loaded.CallID, "call-abc")
	}
	if loaded.Result == nil || *loaded.Result != "root:x:0:0:" {
		t.Errorf("Result = %v, want %q", loaded.Result, "root:x:0:0:")
	}
}

func strPtr(s string) *string { return &s }

// ── Delete cascades ──────────────────────────────────────────────────────────

func TestDeleteTask_Cascades(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	tsk := newTask(uuid.New().String(), "cascade test")
	if err := store.CreateTask(tsk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Insert related records directly.
	step := task.TaskStep{ID: uuid.New().String(), TaskID: tsk.ID, StepNumber: 1}
	if err := db.DB.Create(&step).Error; err != nil {
		t.Fatalf("Create step: %v", err)
	}
	evt := task.TaskEvent{ID: uuid.New().String(), TaskID: tsk.ID, EventType: "test"}
	if err := db.DB.Create(&evt).Error; err != nil {
		t.Fatalf("Create event: %v", err)
	}
	tc := task.TaskToolCall{ID: uuid.New().String(), TaskID: tsk.ID, ToolName: "ls"}
	if err := db.DB.Create(&tc).Error; err != nil {
		t.Fatalf("Create tool call: %v", err)
	}

	if err := store.DeleteTask(tsk.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	var stepCount, evtCount, tcCount int64
	db.DB.Model(&task.TaskStep{}).Where("task_id = ?", tsk.ID).Count(&stepCount)
	db.DB.Model(&task.TaskEvent{}).Where("task_id = ?", tsk.ID).Count(&evtCount)
	db.DB.Model(&task.TaskToolCall{}).Where("task_id = ?", tsk.ID).Count(&tcCount)

	if stepCount != 0 {
		t.Errorf("remaining steps = %d, want 0", stepCount)
	}
	if evtCount != 0 {
		t.Errorf("remaining events = %d, want 0", evtCount)
	}
	if tcCount != 0 {
		t.Errorf("remaining tool calls = %d, want 0", tcCount)
	}
}

// ── UpdateTask requires non-empty ID ────────────────────────────────────────

func TestUpdateTask_RequiresID(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)
	if err := store.UpdateTask(&task.Task{Status: task.StatusPending}); err == nil {
		t.Fatal("expected error for empty ID")
	}
}

// ── UpdateStatus requires non-empty ID and status ────────────────────────────

func TestUpdateStatus_Validation(t *testing.T) {
	db := openTestDB(t)
	store := newStore(t, db)

	if err := store.UpdateStatus("", task.StatusQueued); err == nil {
		t.Fatal("expected error for empty ID")
	}
}
