package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/google/uuid"
)

// Save persists the current agent context as a checkpoint on the task.
// Returns an error if persistence fails; callers should stop execution.
func Save(ctx context.Context, store AgentStore, tc *AgentContext) error {
	truncated := truncateContext(tc)
	cp := Checkpoint{
		TaskID:    truncated.TaskID,
		Step:      truncated.Step,
		Messages:  truncated.Messages,
		ToolState: truncated.ToolState,
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

// truncateContext bounds the context messages so the checkpoint fits within
// maxBytes. It preserves the first two messages (system + original user
// instruction) and keeps as many trailing messages as the budget allows.
// When maxBytes <= 0 or the context is already small enough, the original
// context is returned unchanged.
func truncateContext(tc *AgentContext) *AgentContext {
	if tc.maxBytes > 0 {
		return tc.truncate(tc.maxBytes)
	}
	return tc
}

func (tc *AgentContext) truncate(maxBytes int) *AgentContext {
	if maxBytes <= 0 || len(tc.Messages) <= 2 {
		return tc
	}

	fullCP := Checkpoint{
		TaskID:    tc.TaskID,
		Step:      tc.Step,
		Messages:  tc.Messages,
		ToolState: tc.ToolState,
	}
	fullData, _ := json.Marshal(fullCP)
	if len(fullData) <= maxBytes {
		return tc
	}

	// Always preserve first 2 messages (system + original user).
	preserved := make([]apitypes.Message, 2)
	copy(preserved, tc.Messages[:2])

	// Try keeping last N messages from the tail.
	remaining := tc.Messages[2:]
	for i := len(remaining); i > 0; i-- {
		candidate := append(preserved, remaining[len(remaining)-i:]...)
		cp := Checkpoint{
			TaskID:    tc.TaskID,
			Step:      tc.Step,
			Messages:  candidate,
			ToolState: tc.ToolState,
		}
		data, _ := json.Marshal(cp)
		if len(data) <= maxBytes {
			result := *tc
			result.Messages = candidate
			return &result
		}
	}

	// Fallback: keep only preserved + last message.
	if len(remaining) > 0 {
		candidate := append(preserved, remaining[len(remaining)-1])
		result := *tc
		result.Messages = candidate
		return &result
	}
	return tc
}
