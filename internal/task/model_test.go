package task_test

import (
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/task"
)

func TestStatusIsTerminal(t *testing.T) {
	tests := []struct {
		status   task.Status
		expected bool
	}{
		{task.StatusPending, false},
		{task.StatusQueued, false},
		{task.StatusRunning, false},
		{task.StatusPaused, false},
		{task.StatusCompleted, true},
		{task.StatusFailed, true},
		{task.StatusCancelled, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.expected {
				t.Errorf("IsTerminal(%s) = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestValidateTransition(t *testing.T) {
	valid := []struct {
		from, to task.Status
	}{
		{task.StatusPending, task.StatusQueued},
		{task.StatusQueued, task.StatusRunning},
		{task.StatusRunning, task.StatusPaused},
		{task.StatusRunning, task.StatusCompleted},
		{task.StatusRunning, task.StatusFailed},
		{task.StatusRunning, task.StatusCancelled},
		{task.StatusPaused, task.StatusQueued},
		{task.StatusFailed, task.StatusQueued},
	}
	for _, tt := range valid {
		t.Run(string(tt.from)+"_"+string(tt.to), func(t *testing.T) {
			if err := task.ValidateTransition(tt.from, tt.to); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}

	invalid := []struct {
		from, to task.Status
	}{
		{task.StatusPending, task.StatusRunning},
		{task.StatusQueued, task.StatusCompleted},
		{task.StatusRunning, task.StatusPending},
		{task.StatusCompleted, task.StatusRunning},
		{task.StatusCancelled, task.StatusQueued},
	}
	for _, tt := range invalid {
		t.Run(string(tt.from)+"_"+string(tt.to), func(t *testing.T) {
			if err := task.ValidateTransition(tt.from, tt.to); err == nil {
				t.Errorf("expected error for %s → %s", tt.from, tt.to)
			} else if !task.IsTransitionError(err) {
				t.Errorf("expected TransitionError, got %T", err)
			}
		})
	}
}

func TestTaskBeforeCreate(t *testing.T) {
	t.Run("no parent sets root to id", func(t *testing.T) {
		tsk := &task.Task{ID: "abc-123"}
		if err := tsk.BeforeCreate(nil); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if tsk.RootID != "abc-123" {
			t.Errorf("RootID = %q, want %q", tsk.RootID, "abc-123")
		}
	})

	t.Run("parent sets root to parent id", func(t *testing.T) {
		parent := "parent-456"
		tsk := &task.Task{ID: "child-789", ParentID: &parent}
		if err := tsk.BeforeCreate(nil); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if tsk.RootID != "parent-456" {
			t.Errorf("RootID = %q, want %q", tsk.RootID, "parent-456")
		}
	})

	t.Run("explicit root is preserved", func(t *testing.T) {
		tsk := &task.Task{ID: "abc", RootID: "root-xyz"}
		if err := tsk.BeforeCreate(nil); err != nil {
			t.Fatalf("BeforeCreate: %v", err)
		}
		if tsk.RootID != "root-xyz" {
			t.Errorf("RootID = %q, want %q", tsk.RootID, "root-xyz")
		}
	})
}

func TestTaskTimestamps(t *testing.T) {
	// autoCreateTime is set by GORM on Create, not on struct init.
	// We verify that UpdatedAt is zero before DB write, and that
	// the struct accepts valid time values.
	tsk := &task.Task{
		ID:         "tsk-1",
		Status:     task.StatusPending,
		Input:      "test input",
		Priority:   5,
		MaxRetries: 3,
	}
	if tsk.CreatedAt != (time.Time{}) {
		t.Errorf("CreatedAt should be zero before GORM Create, got %v", tsk.CreatedAt)
	}
	if tsk.UpdatedAt != (time.Time{}) {
		t.Errorf("UpdatedAt should be zero before GORM Create, got %v", tsk.UpdatedAt)
	}
	if tsk.Priority != 5 {
		t.Errorf("Priority = %d, want 5", tsk.Priority)
	}
	if tsk.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", tsk.MaxRetries)
	}
}
