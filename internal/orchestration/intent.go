package orchestration

import (
	"context"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/automode"
	"github.com/EffNine/conductor/internal/policy"
	"github.com/google/uuid"
)

// Intent represents the classified purpose of a task.
type Intent struct {
	TaskType     string         `json:"task_type"`
	Confidence   float64        `json:"confidence"`
	Description  string         `json:"description,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	ClassifiedAt time.Time      `json:"classified_at"`
}

// ClassifyIntent analyzes task input and returns the best-matching intent.
func ClassifyIntent(ctx context.Context, input string) *Intent {
	_ = ctx
	profile := automode.ClassifyTask(input)
	desc := profileDescription(profile)

	intent := &Intent{
		TaskType:     profile,
		Confidence:   computeConfidence(input, profile),
		Description:  desc,
		Metadata:     map[string]any{"classifier": "keyword_heuristics"},
		ClassifiedAt: time.Now().UTC(),
	}
	return intent
}

func profileDescription(profile string) string {
	switch profile {
	case string(automode.TaskElite):
		return "complex agentic coding task requiring multi-step execution"
	case string(automode.TaskCoding):
		return "code generation, debugging, or refactoring"
	case string(automode.TaskReasoning):
		return "analysis, comparison, or multi-step logical reasoning"
	case string(automode.TaskVision):
		return "image or visual content understanding"
	case string(automode.TaskFast):
		return "short, simple request requiring minimal processing"
	default:
		return "general task with no strong signal"
	}
}

func computeConfidence(input, profile string) float64 {
	if profile == string(automode.TaskDefault) {
		return 0.3
	}
	lower := strings.ToLower(input)
	matched := 0
	total := 0
	switch profile {
	case string(automode.TaskElite):
		for _, kw := range []string{"implement", "refactor", "build", "create"} {
			total++
			if strings.Contains(lower, kw) {
				matched++
			}
		}
	case string(automode.TaskCoding):
		for _, kw := range []string{"code", "debug", "fix", "function", "test"} {
			total++
			if strings.Contains(lower, kw) {
				matched++
			}
		}
	case string(automode.TaskReasoning):
		for _, kw := range []string{"analyze", "compare", "explain", "why", "how"} {
			total++
			if strings.Contains(lower, kw) {
				matched++
			}
		}
	case string(automode.TaskVision):
		for _, kw := range []string{"image", "picture", "screenshot", "describe"} {
			total++
			if strings.Contains(lower, kw) {
				matched++
			}
		}
	case string(automode.TaskFast):
		for _, kw := range []string{"hi", "hello", "quick", "simple"} {
			total++
			if strings.Contains(lower, kw) {
				matched++
			}
		}
	}
	if total == 0 {
		return 0.5
	}
	conf := float64(matched) / float64(total)
	if conf < 0.3 {
		conf = 0.3
	}
	if conf > 1.0 {
		conf = 1.0
	}
	return conf
}

// ToPolicyIntent converts an orchestration Intent to a policy.Intent.
func ToPolicyIntent(i *Intent) *policy.Intent {
	var tt policy.TaskType
	switch i.TaskType {
	case string(automode.TaskElite), string(automode.TaskCoding):
		tt = policy.TaskTypeCode
	case string(automode.TaskReasoning):
		tt = policy.TaskTypeReasoning
	case string(automode.TaskVision):
		tt = policy.TaskTypeVision
	case string(automode.TaskFast):
		tt = policy.TaskTypeChat
	default:
		tt = policy.TaskTypeChat
	}
	return &policy.Intent{
		TaskType:    tt,
		Confidence:  i.Confidence,
		Description: i.Description,
		Metadata:    i.Metadata,
	}
}

// NewIntentID generates a unique intent ID.
func NewIntentID() string {
	return uuid.New().String()
}
