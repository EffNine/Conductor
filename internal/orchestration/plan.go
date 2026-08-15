package orchestration

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Step describes a single executable unit in a plan.
type Step struct {
	ID          string `json:"id"`
	Number      int    `json:"number"`
	Description string `json:"description"`
	ToolCall    string `json:"tool_call,omitempty"` // tool name if this step is a tool invocation
	Verified    bool   `json:"verified"`
	Error       string `json:"error,omitempty"`
}

// Plan is a bounded sequence of steps derived from task understanding.
type Plan struct {
	ID           string    `json:"id"`
	TaskID       string    `json:"task_id"`
	Intent       string    `json:"intent"`
	Capabilities string    `json:"capabilities,omitempty"`
	Steps        []Step    `json:"steps"`
	Status       string    `json:"status"` // "pending", "running", "completed", "failed"
	CurrentStep  int       `json:"current_step"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PlanStatus values.
const (
	PlanPending   = "pending"
	PlanRunning   = "running"
	PlanCompleted = "completed"
	PlanFailed    = "failed"
)

// GeneratePlan produces a plan from task input, intent, and capabilities.
func GeneratePlan(taskInput, intent string, caps *CapabilityRequirement) *Plan {
	id := uuid.New().String()
	now := time.Now().UTC()
	steps := deriveSteps(taskInput, intent, caps)
	p := &Plan{
		ID:           id,
		TaskID:       "",
		Intent:       intent,
		Capabilities: capabilitySummary(caps),
		Steps:        steps,
		Status:       PlanPending,
		CurrentStep:  0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return p
}

func deriveSteps(input, intent string, caps *CapabilityRequirement) []Step {
	var steps []Step
	switch intent {
	case "coding", "elite":
		steps = append(steps, Step{ID: uuid.New().String(), Number: 1, Description: "understand the task and repository structure"})
		if caps != nil && caps.NeedsFileSystem {
			steps = append(steps, Step{ID: uuid.New().String(), Number: 2, Description: "inspect relevant files"})
		}
		if caps != nil && caps.NeedsShell {
			steps = append(steps, Step{ID: uuid.New().String(), Number: 3, Description: "run tests or build"})
		}
		if caps != nil && caps.NeedsGit {
			steps = append(steps, Step{ID: uuid.New().String(), Number: 4, Description: "check git status and diff"})
		}
		steps = append(steps, Step{ID: uuid.New().String(), Number: 5, Description: "implement the solution"})
		if caps != nil && caps.NeedsShell {
			steps = append(steps, Step{ID: uuid.New().String(), Number: 6, Description: "verify the solution"})
		}
	default:
		steps = append(steps, Step{ID: uuid.New().String(), Number: 1, Description: "process the request"})
	}

	if len(steps) == 0 {
		steps = append(steps, Step{ID: uuid.New().String(), Number: 1, Description: "execute task"})
	}
	return steps
}

func capabilitySummary(caps *CapabilityRequirement) string {
	if caps == nil {
		return "basic"
	}
	parts := []string{}
	if caps.NeedsFileSystem {
		parts = append(parts, "filesystem")
	}
	if caps.NeedsShell {
		parts = append(parts, "shell")
	}
	if caps.NeedsGit {
		parts = append(parts, "git")
	}
	if caps.NeedsReasoning {
		parts = append(parts, "reasoning")
	}
	if caps.NeedsVision {
		parts = append(parts, "vision")
	}
	if caps.NeedsToolCalling {
		parts = append(parts, "tool_calling")
	}
	if len(parts) == 0 {
		return "basic"
	}
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += ","
		}
		result += p
	}
	return result
}

// Marshal serializes a plan to JSON bytes.
func (p *Plan) Marshal() ([]byte, error) {
	return json.Marshal(p)
}

// Unmarshal deserializes a plan from JSON bytes.
func Unmarshal(data []byte) (*Plan, error) {
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}
	return &p, nil
}
