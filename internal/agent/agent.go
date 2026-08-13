package agent

import (
	"context"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
)

// AgentStore is the minimal persistence interface the agent loop requires.
type AgentStore interface {
	GetTask(id string) (*TaskRef, error)
	UpdateStatus(id string, status TaskStatus) error
	SaveCheckpoint(id string, data []byte) error
	CreateTaskEvent(evt *Event) error
	CreateTaskStep(step *Step) error
	CreateTaskToolCall(tc *ToolCall) error
	FailTask(id string, errMsg string) error
	UpdateTask(task *TaskRef) error
}

// TaskRef is a minimal representation of a task used by the agent.
type TaskRef struct {
	ID          string
	Status      TaskStatus
	Input       string
	InputJSON   []byte
	Output      string
	OutputJSON  []byte
	Provider    string
	Model       string
	StepCount   int
	Checkpoint  []byte
	Error       *string
	RetryCount  int
	MaxRetries  int
	MaxSteps    int
	CompletedAt *time.Time

	// V2.5 orchestration fields
	PlanID          string
	Intent          string
	CurrentPlanStep int

	// V2.6 role field
	RoleDefinition string
}

// TaskStatus mirrors task.Status values used by the agent.
type TaskStatus string

const (
	StatusPending   TaskStatus = "pending"
	StatusQueued    TaskStatus = "queued"
	StatusRunning   TaskStatus = "running"
	StatusPaused    TaskStatus = "paused"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusCancelled TaskStatus = "cancelled"
)

func (s TaskStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// Step records a single LLM call within a task.
type Step struct {
	ID               string
	TaskID           string
	StepNumber       int
	Provider         string
	Model            string
	Request          []byte
	Response         []byte
	ToolCalls        []byte
	ToolResults      []byte
	Status           string
	Error            *string
	LatencyMs        int64
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Event records a state transition or notable occurrence for a task.
type Event struct {
	ID        string
	TaskID    string
	EventType string
	EventData []byte
}

// ToolCall records a single tool invocation within a task step.
type ToolCall struct {
	ID       string
	TaskID   string
	StepID   *string
	CallID   string
	ToolName string
	Arguments []byte
	Result   *string
	Error    *string
	Status   string
}

// Checkpoint serializes enough state to resume an interrupted agent run.
type Checkpoint struct {
	TaskID    string               `json:"task_id"`
	Step      int                  `json:"step"`
	Messages  []apitypes.Message   `json:"messages"`
	ToolState map[string]any       `json:"tool_state,omitempty"`
	SavedAt   time.Time            `json:"saved_at"`
}

// DefaultMaxSteps is the fallback when cfg.MaxSteps <= 0.
const DefaultMaxSteps = 10

// Agent is the minimum abstraction for a single-agent multi-step executor.
type Agent interface {
	Execute(ctx context.Context, task *TaskRef) (*TaskRef, error)
	Name() string
}

// Config holds parameters that control the agent loop.
type Config struct {
	MaxSteps        int
	WorkspaceRoot   string
	MaxOutputBytes  int
	MaxWriteBytes   int
	MaxContextBytes int // hard cap on checkpoint size. 0 = unlimited (backward compatible). Default 1MiB.
	ShellEnabled    bool
	ShellWorkingDir string
	ShellTimeout    time.Duration
	ShellMaxOutput  int
	ShellAllowList  []string
	ShellDenied     []string
	ShellEnvWhite   []string
	GitEnabled      bool
	GitRepoRoot     string
}
