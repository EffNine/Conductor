package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Save persists the current agent context as a checkpoint on the task.
// Returns an error if persistence fails; callers should stop execution.
func Save(ctx context.Context, store AgentStore, tc *AgentContext) error {
	cp := Checkpoint{
		TaskID:    tc.TaskID,
		Step:      tc.Step,
		Messages:  tc.Messages,
		ToolState: tc.ToolState,
		SavedAt:   time.Now().UTC(),
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	if err := store.SaveCheckpoint(tc.TaskID, data); err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}
	_ = store.CreateTaskEvent(&Event{
		ID:        uuid.New().String(),
		TaskID:    tc.TaskID,
		EventType: "task.checkpoint.created",
		EventData: data,
	})
	return nil
}

// Restore loads a checkpoint from the task and returns a new AgentContext.
func Restore(store AgentStore, taskID string) (*AgentContext, error) {
	t, err := store.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("restore checkpoint: get task: %w", err)
	}
	if len(t.Checkpoint) == 0 {
		return nil, fmt.Errorf("no checkpoint found for task %s", taskID)
	}
	var cp Checkpoint
	if err := json.Unmarshal(t.Checkpoint, &cp); err != nil {
		return nil, fmt.Errorf("restore checkpoint: unmarshal: %w", err)
	}
	if cp.TaskID != taskID {
		return nil, fmt.Errorf("checkpoint task_id mismatch: got %q, want %q", cp.TaskID, taskID)
	}
	return &AgentContext{
		TaskID:    cp.TaskID,
		Messages:  cp.Messages,
		ToolState: cp.ToolState,
		Step:      cp.Step,
	}, nil
}
