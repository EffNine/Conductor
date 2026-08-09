package policy

import (
	"testing"
)

func TestIntentConstants(t *testing.T) {
	expected := []TaskType{
		TaskTypeChat,
		TaskTypeCompletion,
		TaskTypeEmbedding,
		TaskTypeVision,
		TaskTypeReasoning,
		TaskTypeToolCalling,
		TaskTypeCode,
		TaskTypeCreative,
	}

	for _, intent := range expected {
		if intent == "" {
			t.Error("expected non-empty task type constant")
		}
	}
}

func TestIntent(t *testing.T) {
	intent := Intent{
		TaskType:    TaskTypeChat,
		Confidence:  0.95,
		Description: "General chat assistance",
		Metadata: map[string]any{
			"language":   "en",
			"complexity": "medium",
		},
	}

	if intent.TaskType != TaskTypeChat {
		t.Errorf("expected TaskTypeChat, got %s", intent.TaskType)
	}
	if intent.Confidence != 0.95 {
		t.Errorf("expected 0.95 confidence, got %f", intent.Confidence)
	}
	if intent.Metadata["language"] != "en" {
		t.Error("expected language to be 'en'")
	}
}

func TestCapabilityRequirement(t *testing.T) {
	req := CapabilityRequirement{
		NeedsStreaming:   true,
		NeedsVision:      false,
		NeedsReasoning:   true,
		NeedsToolCalling: false,
		NeedsStructured:  false,
		MaxTokens:        2048,
		MinLatencyMs:     100,
		CostCeiling:      0.01,
	}

	if !req.NeedsStreaming {
		t.Error("expected streaming to be required")
	}
	if req.NeedsVision {
		t.Error("expected vision to not be required")
	}
	if req.MaxTokens != 2048 {
		t.Errorf("expected 2048 max tokens, got %d", req.MaxTokens)
	}
}

func TestPolicyResult(t *testing.T) {
	result := PolicyResult{
		Allowed: true,
		Reason:  "policy passed",
	}

	if !result.Allowed {
		t.Error("expected policy to be allowed")
	}
	if result.Reason != "policy passed" {
		t.Errorf("expected 'policy passed', got %s", result.Reason)
	}
}

func TestRequestModifications(t *testing.T) {
	maxTokens := 1024
	mod := RequestModifications{
		ModelID:   "gpt-4o",
		MaxTokens: &maxTokens,
	}

	if mod.ModelID != "gpt-4o" {
		t.Errorf("expected 'gpt-4o', got %s", mod.ModelID)
	}
	if mod.MaxTokens == nil || *mod.MaxTokens != 1024 {
		t.Error("expected max tokens to be 1024")
	}
}
