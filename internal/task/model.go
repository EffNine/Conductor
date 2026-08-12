package task

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Status represents the lifecycle state of a task.
type Status string

const (
	StatusPending    Status = "pending"
	StatusQueued     Status = "queued"
	StatusRunning    Status = "running"
	StatusPaused     Status = "paused"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
)

// IsTerminal returns true if the status is a final state.
func (s Status) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// validTransitions maps each status to the set of statuses it can transition to.
var validTransitions = map[Status][]Status{
	StatusPending:   {StatusQueued},
	StatusQueued:    {StatusRunning},
	StatusRunning:   {StatusPaused, StatusCompleted, StatusFailed, StatusCancelled},
	StatusPaused:    {StatusQueued},
	StatusFailed:    {StatusQueued},
	StatusCancelled: {},
}

// TransitionError is returned when an invalid status transition is attempted.
type TransitionError struct {
	From Status
	To   Status
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("invalid task status transition: %s → %s", e.From, e.To)
}

// IsTransitionError reports whether err is a TransitionError.
func IsTransitionError(err error) bool {
	var te *TransitionError
	return errors.As(err, &te)
}

// ValidateTransition checks whether from → to is a valid transition.
func ValidateTransition(from, to Status) error {
	valid, ok := validTransitions[from]
	if !ok {
		return &TransitionError{From: from, To: to}
	}
	for _, s := range valid {
		if s == to {
			return nil
		}
	}
	return &TransitionError{From: from, To: to}
}

// Task represents a persistent unit of work in Conductor.
type Task struct {
	// Identity
	ID       string `gorm:"primaryKey;type:text" json:"id"`
	ParentID *string `gorm:"type:text" json:"parent_id,omitempty"`
	RootID   string `gorm:"type:text;index" json:"root_id"`

	// State
	Status   Status  `gorm:"type:text;index" json:"status"`
	Priority int     `gorm:"default:0" json:"priority"`

	// Input
	Input     string `gorm:"type:text" json:"input"`
	InputJSON []byte `gorm:"type:blob" json:"input_json,omitempty"`

	// Output
	Output    string `gorm:"type:text" json:"output,omitempty"`
	OutputJSON []byte `gorm:"type:blob" json:"output_json,omitempty"`

	// Error
	Error     *string `gorm:"type:text" json:"error,omitempty"`
	ErrorCode *string `gorm:"type:text" json:"error_code,omitempty"`

	// Retry
	RetryCount  int        `gorm:"default:0" json:"retry_count"`
	MaxRetries  int        `gorm:"default:0" json:"max_retries"`
	NextRetryAt *time.Time `gorm:"index" json:"next_retry_at,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `gorm:"autoCreateTime;index" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	StartedAt   *time.Time `gorm:"index" json:"started_at,omitempty"`
	CompletedAt *time.Time `gorm:"index" json:"completed_at,omitempty"`

	// Checkpoint (agent state for pause/resume)
	Checkpoint []byte `gorm:"type:blob" json:"checkpoint,omitempty"`

	// Provider/model execution metadata
	Provider string `gorm:"type:text" json:"provider,omitempty"`
	Model    string `gorm:"type:text" json:"model,omitempty"`
	StepCount int   `gorm:"default:0" json:"step_count"`
	// MaxSteps limits the number of LLM iterations in a single agent execution.
	// 0 means use the global agent default (10).
	MaxSteps int `gorm:"default:0" json:"max_steps"`

	// Lease fields for worker pool ownership (V2.4-F).
	ClaimedBy  string     `gorm:"type:text;default:''" json:"claimed_by,omitempty"`
	ClaimedAt  *time.Time `gorm:"index" json:"claimed_at,omitempty"`
	LeaseUntil *time.Time `gorm:"index" json:"lease_until,omitempty"`
}

// BeforeCreate is a GORM hook to set RootID from ParentID if empty.
func (t *Task) BeforeCreate(_ *gorm.DB) error {
	if t.RootID == "" {
		if t.ParentID != nil && *t.ParentID != "" {
			t.RootID = *t.ParentID
		} else {
			t.RootID = t.ID
		}
	}
	return nil
}

// TaskStep records a single LLM call within a task.
type TaskStep struct {
	ID         string `gorm:"primaryKey;type:text" json:"id"`
	TaskID     string `gorm:"type:text;index" json:"task_id"`
	StepNumber int    `gorm:"default:0" json:"step_number"`

	// Provider info
	Provider string `gorm:"type:text" json:"provider,omitempty"`
	Model    string `gorm:"type:text" json:"model,omitempty"`

	// Request / response
	Request   []byte `gorm:"type:blob" json:"request,omitempty"`
	Response  []byte `gorm:"type:blob" json:"response,omitempty"`

	// Tool call data
	ToolCalls   []byte `gorm:"type:blob" json:"tool_calls,omitempty"`
	ToolResults []byte `gorm:"type:blob" json:"tool_results,omitempty"`

	// Status
	Status string `gorm:"type:text" json:"status"`
	Error  *string `gorm:"type:text" json:"error,omitempty"`

	// Timing
	LatencyMs int64 `gorm:"default:0" json:"latency_ms"`

	// Token usage
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// TaskEvent records a state transition or notable occurrence for a task.
type TaskEvent struct {
	ID         string `gorm:"primaryKey;type:text" json:"id"`
	TaskID     string `gorm:"type:text;index" json:"task_id"`
	EventType  string `gorm:"type:text" json:"event_type"`
	EventData  []byte `gorm:"type:blob" json:"event_data,omitempty"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// TaskToolCall records a single tool invocation within a task step.
type TaskToolCall struct {
	ID         string `gorm:"primaryKey;type:text" json:"id"`
	TaskID     string `gorm:"type:text;index" json:"task_id"`
	StepID     *string `gorm:"type:text" json:"step_id,omitempty"`
	CallID     string `gorm:"type:text" json:"call_id"`
	ToolName   string `gorm:"type:text" json:"tool_name"`
	Arguments  []byte `gorm:"type:blob" json:"arguments,omitempty"`
	Result     *string `gorm:"type:text" json:"result,omitempty"`
	Error      *string `gorm:"type:text" json:"error,omitempty"`
	Status     string `gorm:"type:text" json:"status"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
