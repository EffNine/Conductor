package orchestration_test

import (
	"context"
	"testing"

	"github.com/EffNine/conductor/internal/orchestration"
)

func TestClassifyIntent_Coding(t *testing.T) {
	intent := orchestration.ClassifyIntent(context.Background(), "Write a function that sorts an array")
	if intent.TaskType != "coding" {
		t.Errorf("task_type = %q, want coding", intent.TaskType)
	}
}

func TestClassifyIntent_Vision(t *testing.T) {
	intent := orchestration.ClassifyIntent(context.Background(), "Describe what is in this image")
	if intent.TaskType != "vision" {
		t.Errorf("task_type = %q, want vision", intent.TaskType)
	}
}

func TestClassifyIntent_Reasoning(t *testing.T) {
	intent := orchestration.ClassifyIntent(context.Background(), "Analyze the tradeoffs between microservices and monoliths")
	if intent.TaskType != "reasoning" {
		t.Errorf("task_type = %q, want reasoning", intent.TaskType)
	}
}

func TestClassifyIntent_Fast(t *testing.T) {
	intent := orchestration.ClassifyIntent(context.Background(), "hi quick question")
	if intent.TaskType != "fast" {
		t.Errorf("task_type = %q, want fast", intent.TaskType)
	}
}

func TestClassifyIntent_Default(t *testing.T) {
	intent := orchestration.ClassifyIntent(context.Background(), "the weather is nice today")
	if intent.TaskType != "default" {
		t.Errorf("task_type = %q, want default", intent.TaskType)
	}
	if intent.Confidence != 0.3 {
		t.Errorf("confidence = %v, want 0.3", intent.Confidence)
	}
}

func TestGeneratePlan_Coding(t *testing.T) {
	caps := &orchestration.CapabilityRequirement{
		NeedsFileSystem: true,
		NeedsShell:      true,
		NeedsGit:        true,
		NeedsToolCalling: true,
	}
	plan := orchestration.GeneratePlan("fix the failing tests in this repo", "coding", caps)
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
	if plan.ID == "" {
		t.Error("expected non-empty plan ID")
	}
	if plan.Intent != "coding" {
		t.Errorf("intent = %q, want coding", plan.Intent)
	}
	if len(plan.Steps) < 3 {
		t.Errorf("expected at least 3 steps, got %d", len(plan.Steps))
	}
	if plan.Status != orchestration.PlanPending {
		t.Errorf("status = %q, want pending", plan.Status)
	}
}

func TestGeneratePlan_Default(t *testing.T) {
	plan := orchestration.GeneratePlan("hello", "default", nil)
	if len(plan.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(plan.Steps))
	}
}

func TestCapabilitySummary(t *testing.T) {
	caps := &orchestration.CapabilityRequirement{
		NeedsFileSystem: true,
		NeedsShell:      true,
		NeedsGit:        true,
	}
	summary := orchestration.GeneratePlan("", "coding", caps).Capabilities
	if summary == "" {
		t.Error("expected non-empty capability summary")
	}
}

func TestResolveCapabilities_WithTools(t *testing.T) {
	caps := orchestration.ResolveCapabilities(context.Background(), "fix the bug", &orchestration.Intent{TaskType: "coding"}, []string{"read_file", "shell_exec", "git_commit"})
	if !caps.NeedsFileSystem {
		t.Error("expected NeedsFileSystem=true")
	}
	if !caps.NeedsShell {
		t.Error("expected NeedsShell=true")
	}
	if !caps.NeedsGit {
		t.Error("expected NeedsGit=true")
	}
	if !caps.NeedsToolCalling {
		t.Error("expected NeedsToolCalling=true")
	}
}

func TestResolveCapabilities_NoTools(t *testing.T) {
	caps := orchestration.ResolveCapabilities(context.Background(), "hello", &orchestration.Intent{TaskType: "fast"}, nil)
	if caps.NeedsToolCalling {
		t.Error("expected NeedsToolCalling=false when no tools")
	}
}

func TestDefaultVerifier_Coding(t *testing.T) {
	result, err := orchestration.DefaultVerifier(context.Background(), "write a function", "```python\ndef foo(): pass\n```", "coding")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %s", result.Message)
	}
}

func TestDefaultVerifier_EmptyOutput(t *testing.T) {
	result, err := orchestration.DefaultVerifier(context.Background(), "hello", "", "chat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for empty output")
	}
}

func TestPlanMarshalUnmarshal(t *testing.T) {
	plan := orchestration.GeneratePlan("test input", "coding", &orchestration.CapabilityRequirement{NeedsFileSystem: true})
	plan.TaskID = "task-123"

	data, err := plan.Marshal()
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	restore, err := orchestration.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if restore.ID != plan.ID {
		t.Errorf("ID mismatch: %q != %q", restore.ID, plan.ID)
	}
	if restore.TaskID != "task-123" {
		t.Errorf("TaskID mismatch: %q != %q", restore.TaskID, "task-123")
	}
	if len(restore.Steps) != len(plan.Steps) {
		t.Errorf("steps mismatch: %d != %d", len(restore.Steps), len(plan.Steps))
	}
}
