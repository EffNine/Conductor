package agent_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EffNine/conductor/internal/agent"
	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/task"
	"github.com/google/uuid"
)

func newTestDBForContext(t *testing.T) *database.Database {
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

func insertTaskForContext(t *testing.T, db *database.Database, input string) string {
	t.Helper()
	id := uuid.New().String()
	tsk := &task.Task{
		ID:     id,
		Status: task.StatusRunning,
		Input:  input,
	}
	if err := db.DB.Create(tsk).Error; err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return id
}

// TestCheckpointTruncation verifies that Save() truncates large message histories
// to fit within the configured MaxContextBytes bound.
func TestCheckpointTruncation(t *testing.T) {
	db := newTestDBForContext(t)
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	taskID := insertTaskForContext(t, db, "original user instruction")

	// Build a context with many large messages.
	messages := []apitypes.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "original user instruction"},
	}
	// Add 15 steps worth of tool calls and results (~50KB each).
	for i := 0; i < 15; i++ {
		messages = append(messages, apitypes.Message{
			Role:    "assistant",
			Content: "assistant response step " + string(rune('0'+i)),
		})
		messages = append(messages, apitypes.Message{
			Role:       "tool",
			Content:    strings.Repeat("x", 50000), // 50KB tool output
			ToolCallID: "tc-" + string(rune('0'+i)),
		})
	}

	ctx := agent.NewContext(taskID, messages).WithMaxBytes(100_000)

	// Save should truncate.
	if err := agent.Save(context.Background(), store, ctx); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the checkpoint is within bounds.
	loaded, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(loaded.Checkpoint) > 100_000 {
		t.Errorf("checkpoint size = %d, want <= 100000", len(loaded.Checkpoint))
	}

	// Verify original user instruction is preserved (first 2 messages kept).
	var cp agent.Checkpoint
	if err := json.Unmarshal(loaded.Checkpoint, &cp); err != nil {
		t.Fatalf("unmarshal checkpoint: %v", err)
	}
	if len(cp.Messages) < 2 {
		t.Fatalf("expected at least 2 messages after truncation, got %d", len(cp.Messages))
	}
	if cp.Messages[0].Role != "system" {
		t.Errorf("first message role = %q, want system", cp.Messages[0].Role)
	}
	if cp.Messages[1].Content != "original user instruction" {
		t.Errorf("second message content = %q, want 'original user instruction'", cp.Messages[1].Content)
	}
	// Total messages should be fewer than the original 32 (2 + 15*2).
	if len(cp.Messages) >= len(messages) {
		t.Errorf("expected truncation: messages %d, want fewer than %d", len(cp.Messages), len(messages))
	}
}

// TestCheckpointNoTruncationWhenUnlimited verifies backward compatibility:
// when maxBytes is 0, no truncation occurs.
func TestCheckpointNoTruncationWhenUnlimited(t *testing.T) {
	db := newTestDBForContext(t)
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	taskID := insertTaskForContext(t, db, "hello")

	messages := []apitypes.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "tool", Content: "result", ToolCallID: "tc1"},
	}
	ctx := agent.NewContext(taskID, messages).WithMaxBytes(0) // unlimited

	if err := agent.Save(context.Background(), store, ctx); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	var cp agent.Checkpoint
	if err := json.Unmarshal(loaded.Checkpoint, &cp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cp.Messages) != 4 {
		t.Errorf("messages len = %d, want 4 (no truncation)", len(cp.Messages))
	}
}

// TestCheckpointRestoreWorksAfterTruncation verifies that a truncated checkpoint
// can be restored correctly.
func TestCheckpointRestoreWorksAfterTruncation(t *testing.T) {
	db := newTestDBForContext(t)
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	taskID := insertTaskForContext(t, db, "restore test")

	messages := []apitypes.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "tool", Content: strings.Repeat("x", 50000), ToolCallID: "tc1"},
	}
	ctx := agent.NewContext(taskID, messages).WithMaxBytes(50_000)

	if err := agent.Save(context.Background(), store, ctx); err != nil {
		t.Fatalf("Save: %v", err)
	}

	restored, err := agent.Restore(store, taskID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.TaskID != taskID {
		t.Errorf("TaskID = %q, want %q", restored.TaskID, taskID)
	}
	if len(restored.Messages) != len(messages) {
		// Restored messages should match what was saved (truncated).
		t.Logf("restored messages len = %d (may differ from original if truncated)", len(restored.Messages))
	}
}

// TestCheckpointPreservesFirstTwoMessages verifies that even with aggressive
// truncation, the first two messages (system + original user) are always kept.
func TestCheckpointPreservesFirstTwoMessages(t *testing.T) {
	db := newTestDBForContext(t)
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	taskID := insertTaskForContext(t, db, "preserve test")

	messages := []apitypes.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "original request"},
		{Role: "assistant", Content: "response 1"},
		{Role: "tool", Content: strings.Repeat("y", 100000), ToolCallID: "tc1"},
		{Role: "assistant", Content: "response 2"},
		{Role: "tool", Content: strings.Repeat("z", 100000), ToolCallID: "tc2"},
	}
	ctx := agent.NewContext(taskID, messages).WithMaxBytes(500) // very tight budget

	if err := agent.Save(context.Background(), store, ctx); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	var cp agent.Checkpoint
	if err := json.Unmarshal(loaded.Checkpoint, &cp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cp.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(cp.Messages))
	}
	if cp.Messages[0].Content != "system prompt" {
		t.Errorf("first message = %q, want 'system prompt'", cp.Messages[0].Content)
	}
	if cp.Messages[1].Content != "original request" {
		t.Errorf("second message = %q, want 'original request'", cp.Messages[1].Content)
	}
}

// TestCheckpointTooManyStepsLargeOutputs verifies bounding with many steps
// and large tool outputs.
func TestCheckpointTooManyStepsLargeOutputs(t *testing.T) {
	db := newTestDBForContext(t)
	store := task.NewStoreAdapter(task.NewSQLiteStore(db))
	taskID := insertTaskForContext(t, db, "multi-step test")

	messages := []apitypes.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "do the thing"},
	}
	// Simulate 15 steps with 50KB tool outputs.
	for i := 0; i < 15; i++ {
		messages = append(messages, apitypes.Message{
			Role:    "assistant",
			Content: "assistant output " + string(rune('A'+i%26)),
		})
		messages = append(messages, apitypes.Message{
			Role:       "tool",
			Content:    strings.Repeat("tool-output-", 5000), // ~60KB
			ToolCallID: "tc-" + string(rune('0'+i)),
		})
	}

	ctx := agent.NewContext(taskID, messages).WithMaxBytes(200_000) // 200KB
	ctx.Step = 15

	if err := agent.Save(context.Background(), store, ctx); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(loaded.Checkpoint) > 200_000 {
		t.Errorf("checkpoint size = %d, want <= 200000", len(loaded.Checkpoint))
	}
	// Should have fewer messages than original (32).
	var cp agent.Checkpoint
	json.Unmarshal(loaded.Checkpoint, &cp)
	if len(cp.Messages) >= len(messages) {
		t.Errorf("expected truncation: got %d messages, want fewer than %d", len(cp.Messages), len(messages))
	}
}

// TestAgentContextNewContext verifies NewContext still works correctly.
func TestAgentContextNewContext(t *testing.T) {
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
