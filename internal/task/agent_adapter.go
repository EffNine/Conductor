package task

import (
	"github.com/EffNine/conductor/internal/agent"
)

// toAgentRef converts a persisted Task to the agent's minimal TaskRef.
func toAgentRef(t *Task) *agent.TaskRef {
	return &agent.TaskRef{
		ID:          t.ID,
		Status:      agent.TaskStatus(t.Status),
		Input:       t.Input,
		InputJSON:   t.InputJSON,
		Output:      t.Output,
		OutputJSON:  t.OutputJSON,
		Provider:    t.Provider,
		Model:       t.Model,
		StepCount:   t.StepCount,
		Checkpoint:  t.Checkpoint,
		Error:       t.Error,
		RetryCount:  t.RetryCount,
		MaxRetries:  t.MaxRetries,
		MaxSteps:    t.MaxSteps,
		CompletedAt: t.CompletedAt,
	}
}

// fromAgentRef writes agent result fields back onto a persisted Task.
func fromAgentRef(t *Task, ref *agent.TaskRef) {
	t.Output = ref.Output
	t.OutputJSON = ref.OutputJSON
	t.Provider = ref.Provider
	t.Model = ref.Model
	t.StepCount = ref.StepCount
	if ref.CompletedAt != nil {
		t.CompletedAt = ref.CompletedAt
	}
}

// storeAdapter wraps task.Store so it satisfies agent.AgentStore without
// introducing an import cycle.
type storeAdapter struct {
	store Store
}

func newStoreAdapter(s Store) *storeAdapter {
	return &storeAdapter{store: s}
}

// NewStoreAdapter creates an exported agent.AgentStore that wraps a task.Store.
func NewStoreAdapter(s Store) agent.AgentStore {
	return newStoreAdapter(s)
}

func (a *storeAdapter) GetTask(id string) (*agent.TaskRef, error) {
	t, err := a.store.GetTask(id)
	if err != nil {
		return nil, err
	}
	return toAgentRef(t), nil
}

func (a *storeAdapter) UpdateStatus(id string, status agent.TaskStatus) error {
	return a.store.UpdateStatus(id, Status(status))
}

func (a *storeAdapter) SaveCheckpoint(id string, data []byte) error {
	return a.store.SaveCheckpoint(id, data)
}

func (a *storeAdapter) CreateTaskEvent(evt *agent.Event) error {
	return a.store.CreateTaskEvent(&TaskEvent{
		ID:        evt.ID,
		TaskID:    evt.TaskID,
		EventType: evt.EventType,
		EventData: evt.EventData,
	})
}

func (a *storeAdapter) CreateTaskStep(step *agent.Step) error {
	return a.store.CreateTaskStep(&TaskStep{
		ID:               step.ID,
		TaskID:           step.TaskID,
		StepNumber:       step.StepNumber,
		Provider:         step.Provider,
		Model:            step.Model,
		Request:          step.Request,
		Response:         step.Response,
		ToolCalls:        step.ToolCalls,
		ToolResults:      step.ToolResults,
		Status:           step.Status,
		Error:            step.Error,
		LatencyMs:        step.LatencyMs,
		PromptTokens:     step.PromptTokens,
		CompletionTokens: step.CompletionTokens,
		TotalTokens:      step.TotalTokens,
	})
}

func (a *storeAdapter) CreateTaskToolCall(tc *agent.ToolCall) error {
	return a.store.CreateTaskToolCall(&TaskToolCall{
		ID:        tc.ID,
		TaskID:    tc.TaskID,
		StepID:    tc.StepID,
		CallID:    tc.CallID,
		ToolName:  tc.ToolName,
		Arguments: tc.Arguments,
		Result:    tc.Result,
		Error:     tc.Error,
		Status:    tc.Status,
	})
}

func (a *storeAdapter) FailTask(id string, errMsg string) error {
	return a.store.FailTask(id, errMsg)
}

func (a *storeAdapter) UpdateTask(ref *agent.TaskRef) error {
	// Find the existing task, update mutable fields, persist.
	t, err := a.store.GetTask(ref.ID)
	if err != nil {
		return err
	}
	t.Output = ref.Output
	t.OutputJSON = ref.OutputJSON
	t.Provider = ref.Provider
	t.Model = ref.Model
	t.StepCount = ref.StepCount
	t.CompletedAt = ref.CompletedAt
	return a.store.UpdateTask(t)
}
