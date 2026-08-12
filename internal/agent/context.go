package agent

import "github.com/EffNine/conductor/internal/apitypes"

// AgentContext represents the in-memory execution state for one task.
type AgentContext struct {
	TaskID    string
	Messages  []apitypes.Message
	ToolState map[string]any
	Step      int
}

// NewContext creates an AgentContext initialized with the provided task input.
func NewContext(taskID string, messages []apitypes.Message) *AgentContext {
	return &AgentContext{
		TaskID:    taskID,
		Messages:  messages,
		ToolState: make(map[string]any),
		Step:      0,
	}
}
