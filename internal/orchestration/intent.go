package orchestration

import (
	"context"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/policy"
	"github.com/EffNine/conductor/internal/router"
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
// It delegates to the canonical router.ClassifyRequest.
func ClassifyIntent(ctx context.Context, input string) *Intent {
	_ = ctx
	profile := router.ClassifyRequest(input)
	desc := profile.Description

	intent := &Intent{
		TaskType:     string(profile.Mode),
		Confidence:   computeConfidence(input, profile.Mode),
		Description:  desc,
		Metadata:     map[string]any{"classifier": "keyword_heuristics"},
		ClassifiedAt: time.Now().UTC(),
	}
	return intent
}

func computeConfidence(input string, mode router.Mode) float64 {
	if mode == router.ModeDefault {
		return 0.3
	}
	lower := strings.ToLower(input)
	matched := 0
	total := 0
	switch mode {
	case router.ModeElite:
		for _, kw := range []string{"implement", "refactor", "build", "create"} {
			total++
			if strings.Contains(lower, kw) {
				matched++
			}
		}
	case router.ModeCoding:
		for _, kw := range []string{"code", "debug", "fix", "function", "test"} {
			total++
			if strings.Contains(lower, kw) {
				matched++
			}
		}
	case router.ModeReasoning:
		for _, kw := range []string{"analyze", "compare", "explain", "why", "how"} {
			total++
			if strings.Contains(lower, kw) {
				matched++
			}
		}
	case router.ModeVision:
		for _, kw := range []string{"image", "picture", "screenshot", "describe"} {
			total++
			if strings.Contains(lower, kw) {
				matched++
			}
		}
	case router.ModeFast:
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
	case string(router.ModeElite), string(router.ModeCoding):
		tt = policy.TaskTypeCode
	case string(router.ModeReasoning):
		tt = policy.TaskTypeReasoning
	case string(router.ModeVision):
		tt = policy.TaskTypeVision
	case string(router.ModeFast):
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
