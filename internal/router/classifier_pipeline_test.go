package router

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/policy"
	"github.com/EffNine/conductor/internal/runtime"
)

func TestIntentStageUsesCanonicalClassifier(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "write a function to sort an array"}},
	}
	dc := NewDecisionContext(req, runtime.RuntimeSnapshot{}, ConfigSnapshot{}, TaskMetadata{}, Environment{}, nil, nil)
	defer dc.Close()

	stage := NewIntentStage()
	if err := stage.Execute(context.Background(), dc); err != nil {
		t.Fatalf("IntentStage.Execute: %v", err)
	}

	intent := dc.Intent()
	if intent == nil {
		t.Fatal("expected non-nil intent")
	}
	if intent.TaskType != policy.TaskTypeCode {
		t.Errorf("TaskType = %q, want %q", intent.TaskType, policy.TaskTypeCode)
	}
	if intent.Confidence != 0.7 {
		t.Errorf("Confidence = %f, want 0.7", intent.Confidence)
	}
}

func TestIntentStageVision(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "what is in this image"}},
	}
	dc := NewDecisionContext(req, runtime.RuntimeSnapshot{}, ConfigSnapshot{}, TaskMetadata{}, Environment{}, nil, nil)
	defer dc.Close()

	stage := NewIntentStage()
	if err := stage.Execute(context.Background(), dc); err != nil {
		t.Fatalf("IntentStage.Execute: %v", err)
	}

	intent := dc.Intent()
	if intent.TaskType != policy.TaskTypeVision {
		t.Errorf("TaskType = %q, want %q", intent.TaskType, policy.TaskTypeVision)
	}
}

func TestIntentStageReasoning(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "analyze the tradeoffs"}},
	}
	dc := NewDecisionContext(req, runtime.RuntimeSnapshot{}, ConfigSnapshot{}, TaskMetadata{}, Environment{}, nil, nil)
	defer dc.Close()

	stage := NewIntentStage()
	if err := stage.Execute(context.Background(), dc); err != nil {
		t.Fatalf("IntentStage.Execute: %v", err)
	}

	intent := dc.Intent()
	if intent.TaskType != policy.TaskTypeReasoning {
		t.Errorf("TaskType = %q, want %q", intent.TaskType, policy.TaskTypeReasoning)
	}
}

func TestIntentStageFast(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "hi quick hello"}},
	}
	dc := NewDecisionContext(req, runtime.RuntimeSnapshot{}, ConfigSnapshot{}, TaskMetadata{}, Environment{}, nil, nil)
	defer dc.Close()

	stage := NewIntentStage()
	if err := stage.Execute(context.Background(), dc); err != nil {
		t.Fatalf("IntentStage.Execute: %v", err)
	}

	intent := dc.Intent()
	if intent.TaskType != policy.TaskTypeChat {
		t.Errorf("TaskType = %q, want %q", intent.TaskType, policy.TaskTypeChat)
	}
}

func TestIntentStageDefault(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []apitypes.Message{{Role: "user", Content: "tell me about the weather"}},
	}
	dc := NewDecisionContext(req, runtime.RuntimeSnapshot{}, ConfigSnapshot{}, TaskMetadata{}, Environment{}, nil, nil)
	defer dc.Close()

	stage := NewIntentStage()
	if err := stage.Execute(context.Background(), dc); err != nil {
		t.Fatalf("IntentStage.Execute: %v", err)
	}

	intent := dc.Intent()
	if intent.TaskType != policy.TaskTypeChat {
		t.Errorf("TaskType = %q, want %q", intent.TaskType, policy.TaskTypeChat)
	}
}

func TestCapabilityStageExtractsHints(t *testing.T) {
	req := &apitypes.ChatCompletionRequest{
		Model:    "gpt-4o",
		Stream:   true,
		Messages: []apitypes.Message{{Role: "user", Content: "hello"}},
		Tools: []apitypes.Tool{{
			Type:     "function",
			Function: apitypes.FunctionDef{Name: "get_weather"},
		}},
		ResponseFormat: map[string]interface{}{"type": "json_object"},
	}
	dc := NewDecisionContext(req, runtime.RuntimeSnapshot{}, ConfigSnapshot{}, TaskMetadata{}, Environment{}, nil, nil)
	defer dc.Close()

	stage := NewCapabilityStage()
	if err := stage.Execute(context.Background(), dc); err != nil {
		t.Fatalf("CapabilityStage.Execute: %v", err)
	}

	cr := dc.Capability()
	if cr == nil {
		t.Fatal("expected non-nil capability requirement")
	}
	if !cr.NeedsStreaming {
		t.Error("expected streaming to be true")
	}
	if !cr.NeedsToolCalling {
		t.Error("expected tool calling to be true")
	}
	if !cr.NeedsStructured {
		t.Error("expected structured to be true")
	}
}

func TestCapabilityStageNilRequest(t *testing.T) {
	dc := NewDecisionContext(nil, runtime.RuntimeSnapshot{}, ConfigSnapshot{}, TaskMetadata{}, Environment{}, nil, nil)
	defer dc.Close()

	stage := NewCapabilityStage()
	if err := stage.Execute(context.Background(), dc); err != nil {
		t.Fatalf("CapabilityStage.Execute: %v", err)
	}

	cr := dc.Capability()
	if cr == nil {
		t.Fatal("expected non-nil capability requirement for nil request")
	}
}
