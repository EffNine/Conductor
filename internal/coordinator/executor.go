package coordinator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/task"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Executor wraps a task.Executor and adds multi-agent coordination support.
// When a task has Role=="coordinator", it delegates subtask creation and
// aggregation to the Coordinator before falling back to single-agent execution.
type Executor struct {
	store       task.Store
	executor    task.Executor
	coordinator *Coordinator
	logger      *zap.Logger
}

// NewExecutor creates an Executor that handles both coordinator and leaf tasks.
func NewExecutor(store task.Store, exec task.Executor, coord *Coordinator, logger *zap.Logger) *Executor {
	return &Executor{
		store:       store,
		executor:    exec,
		coordinator: coord,
		logger:      logger,
	}
}

// Execute handles both single-agent and coordinator tasks.
func (e *Executor) Execute(ctx context.Context, taskID string) error {
	t, err := e.store.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("execute task: %w", err)
	}

	if t.Role == "coordinator" {
		return e.executeCoordinator(ctx, t)
	}

	// Fall through to standard single-agent execution.
	return e.executor.Execute(ctx, taskID)
}

func (e *Executor) executeCoordinator(ctx context.Context, parent *task.Task) error {
	// Transition to running if needed.
	if parent.Status != task.StatusRunning {
		if parent.Status != task.StatusPending && parent.Status != task.StatusQueued {
			return fmt.Errorf("coordinator task %s has invalid status %s", parent.ID, parent.Status)
		}
		if err := e.store.UpdateStatus(parent.ID, task.StatusQueued); err != nil {
			return err
		}
		if err := e.store.UpdateStatus(parent.ID, task.StatusRunning); err != nil {
			return err
		}
		parent, _ = e.store.GetTask(parent.ID)
	}

	info := taskToCoordInfo(parent)

	// Delegate: create or resume child tasks.
	childIDs, err := e.coordinator.Delegate(ctx, info)
	if err != nil {
		e.logger.Error("coordinator delegation failed",
			zap.String("parent_id", parent.ID), zap.Error(err))
		return fmt.Errorf("delegate: %w", err)
	}

	if len(childIDs) == 0 {
		// No children created — treat as trivially completed.
		now := time.Now().UTC()
		parent.Status = task.StatusCompleted
		parent.Output = "coordinator: no subtasks required"
		parent.CompletedAt = &now
		_ = e.store.UpdateTask(parent)
		_ = e.store.UpdateStatus(parent.ID, task.StatusCompleted)
		return nil
	}

	// Wait for all children to complete.
	agg, err := e.coordinator.WaitForChildren(ctx, info, childIDs)
	if err != nil {
		e.logger.Error("coordinator wait failed",
			zap.String("parent_id", parent.ID), zap.Error(err))
		// If the context was cancelled, finalize the parent as cancelled.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if transErr := e.store.UpdateStatus(parent.ID, task.StatusCancelled); transErr != nil {
				e.logger.Error("failed to cancel coordinator parent",
					zap.String("parent_id", parent.ID), zap.Error(transErr))
				return transErr
			}
			_ = e.store.CreateTaskEvent(&task.TaskEvent{
				ID:        uuid.New().String(),
				TaskID:    parent.ID,
				EventType: "task.cancelled",
				EventData: mustMarshal(map[string]any{"reason": "coordinator context cancelled"}),
			})
			// Release lease so the worker pool cleans up.
			_ = e.store.ReleaseLease(parent.ID)
			return fmt.Errorf("coordinator cancelled: %w", err)
		}
		return fmt.Errorf("wait for children: %w", err)
	}

	// Mark parent final state based on aggregation.
	if markErr := e.coordinator.MarkParentFinal(ctx, info, agg); markErr != nil {
		e.logger.Error("coordinator mark final failed",
			zap.String("parent_id", parent.ID), zap.Error(markErr))
		return markErr
	}

	e.logger.Info("coordinator completed",
		zap.String("parent_id", parent.ID),
		zap.Int("children", len(childIDs)),
		zap.Bool("all_succeeded", agg.AllSucceeded),
	)
	return nil
}

// taskToCoordInfo converts a persisted Task to a coordinator TaskInfo.
func taskToCoordInfo(t *task.Task) *TaskInfo {
	return &TaskInfo{
		ID:              t.ID,
		ParentID:        t.ParentID,
		RootID:          t.RootID,
		Status:          string(t.Status),
		Input:           t.Input,
		InputJSON:       t.InputJSON,
		Output:          t.Output,
		OutputJSON:      t.OutputJSON,
		Error:           t.Error,
		Provider:        t.Provider,
		Model:           t.Model,
		StepCount:       t.StepCount,
		PlanID:          t.PlanID,
		Intent:          t.Intent,
		CurrentPlanStep: t.CurrentPlanStep,
		Role:            t.Role,
		DependsOn:       t.DependsOn,
		ChildrenJSON:    t.ChildrenJSON,
		CompletedAt:     t.CompletedAt,
		RetryCount:      t.RetryCount,
		MaxRetries:      t.MaxRetries,
	}
}

// coordInfoToTask converts a coordinator TaskInfo back to a persisted Task.
func coordInfoToTask(info *TaskInfo) *task.Task {
	return &task.Task{
		ID:              info.ID,
		ParentID:        info.ParentID,
		RootID:          info.RootID,
		Status:          task.Status(info.Status),
		Input:           info.Input,
		InputJSON:       info.InputJSON,
		Output:          info.Output,
		OutputJSON:      info.OutputJSON,
		Error:           info.Error,
		Provider:        info.Provider,
		Model:           info.Model,
		StepCount:       info.StepCount,
		PlanID:          info.PlanID,
		Intent:          info.Intent,
		CurrentPlanStep: info.CurrentPlanStep,
		Role:            info.Role,
		DependsOn:       info.DependsOn,
		ChildrenJSON:    info.ChildrenJSON,
		CompletedAt:     info.CompletedAt,
	}
}
