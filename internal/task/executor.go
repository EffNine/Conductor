package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/EffNine/conductor/internal/agent"
	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/orchestration"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/tool"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Executor is the interface for task execution.
type Executor interface {
	Execute(ctx context.Context, taskID string) error
}

// TaskExecutor handles the lifecycle of a task, delegating multi-step execution
// to the Agent. It optionally runs an orchestration pipeline before execution.
type TaskExecutor struct {
	store        Store
	router       *router.Engine
	agent        agent.Agent
	catalog      *catalog.Catalog
	usageTracker *usage.Tracker
	orchPipeline *orchestration.OrchestrationPipeline
	logger       *zap.Logger
	toolReg      *tool.Registry
}

// NewTaskExecutor creates a new task executor backed by an agent.
func NewTaskExecutor(
	s Store,
	r *router.Engine,
	a agent.Agent,
	cat *catalog.Catalog,
	ut *usage.Tracker,
	l *zap.Logger,
) *TaskExecutor {
	return &TaskExecutor{
		store:    s,
		router:   r,
		agent:    a,
		catalog:  cat,
		usageTracker: ut,
		logger:   l,
	}
}

// WithOrchestration wires the orchestration pipeline for intelligent task planning.
func (e *TaskExecutor) WithOrchestration(reg *provider.Registry, toolReg *tool.Registry, re *router.RouterEngine, eb *eventbus.EventBus) {
	e.toolReg = toolReg
	e.orchPipeline = orchestration.NewPipeline(orchestration.PipelineConfig{
		Registry: reg,
		Engine:   re,
		EventBus: eb,
		Logger:   e.logger,
		Verify:   orchestration.DefaultVerifier,
	})
}

// Execute loads a task by ID, transitions it through running, runs the agent
// loop, and updates the task on completion or failure.
func (e *TaskExecutor) Execute(ctx context.Context, taskID string) error {
	task, err := e.store.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("execute task: %w", err)
	}

	// Validate task can execute.
	if task.Status.IsTerminal() {
		return fmt.Errorf("task %s is already terminal: %s", taskID, task.Status)
	}

	// Transition to running only if not already running (idempotent for worker pool).
	if task.Status != StatusRunning {
		if err := e.store.UpdateStatus(taskID, StatusQueued); err != nil {
			return fmt.Errorf("transition →queued: %w", err)
		}
		if err := e.store.UpdateStatus(taskID, StatusRunning); err != nil {
			return fmt.Errorf("transition →running: %w", err)
		}
		// Refresh the task from DB so we have the latest state.
		task, _ = e.store.GetTask(taskID)
	}

	// Ensure a model is set before handing off to the agent.
	if task.Model == "" {
		model, merr := e.defaultModel()
		if merr != nil {
			e.failTask(ctx, taskID, fmt.Errorf("no model specified and no default model available: %w", merr))
			return merr
		}
		task.Model = model
		_ = e.store.UpdateTask(task)
	}

	// Run orchestration if pipeline is configured and no plan exists yet.
	var selectedProvider string
	var selectedModel string
	if e.orchPipeline != nil && task.PlanID == "" {
		oc, err := e.orchPipeline.Execute(ctx, taskID, task.Input, func() []string {
			if e.toolReg != nil {
				return e.toolReg.Names()
			}
			return nil
		}())
		if err == nil && oc != nil && oc.Plan != nil {
			plan := oc.Plan
			// Persist the plan through PlanStore.
			dbPlan := &Plan{
				ID:           plan.ID,
				TaskID:       taskID,
				Intent:       plan.Intent,
				Capabilities: plan.Capabilities,
				StepsJSON:    mustMarshal(plan.Steps),
				Status:       orchestration.PlanRunning,
				CurrentStep:  0,
			}
			if ps, ok := e.store.(PlanStore); ok {
				if err := ps.CreatePlan(dbPlan); err != nil {
					e.logger.Warn("failed to persist plan", zap.String("plan_id", plan.ID), zap.Error(err))
				}
			}

			// Apply selected candidate to task fields.
			if oc.Selected != nil {
				selectedProvider = oc.Selected.ProviderName
				// Resolve actual model from the selected candidate.
				selectedModel = orchestration.ResolveCandidateModel(ctx, e.router, selectedProvider, task.Model)
				if selectedModel == "" {
					selectedModel = task.Model
				}
			}

			task.PlanID = plan.ID
			task.Intent = plan.Intent
			task.CurrentPlanStep = 0
			if selectedModel != "" {
				task.Model = selectedModel
			}
			if selectedProvider != "" {
				task.Provider = selectedProvider
			}
			_ = e.store.UpdateTask(task)

			_ = e.store.CreateTaskEvent(&TaskEvent{
				ID:        uuid.New().String(),
				TaskID:    taskID,
				EventType: "plan.created",
				EventData: mustMarshal(map[string]any{
					"plan_id":     plan.ID,
					"intent":      plan.Intent,
					"steps":       len(plan.Steps),
					"provider":    selectedProvider,
					"model":       selectedModel,
					"candidates":  len(oc.Candidates),
				}),
			})
			e.logger.Info("orchestration plan created",
				zap.String("task_id", taskID),
				zap.String("plan_id", plan.ID),
				zap.String("intent", plan.Intent),
				zap.Int("steps", len(plan.Steps)),
				zap.String("selected_provider", selectedProvider),
				zap.String("selected_model", selectedModel),
			)
		}
	}

	// Emit task.started event.
	startEventData := map[string]any{"step": 0}
	if task.PlanID != "" {
		startEventData["plan_id"] = task.PlanID
		startEventData["intent"] = task.Intent
	}
	if selectedProvider != "" {
		startEventData["provider"] = selectedProvider
		startEventData["model"] = selectedModel
	}
	_ = e.store.CreateTaskEvent(&TaskEvent{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		EventType: "task.started",
		EventData: mustMarshal(startEventData),
	})

	// Run the agent loop. The agent returns the updated task or an error.
	ref := toAgentRef(task)
	result, runErr := e.agent.Execute(ctx, ref)
	if runErr != nil {
		e.logger.Error("agent execution failed",
			zap.String("task_id", taskID),
			zap.Error(runErr),
		)
		updated, _ := e.store.GetTask(taskID)
		if updated != nil && !updated.Status.IsTerminal() {
			// Distinguish cancellation from other failures.
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				if transErr := e.store.UpdateStatus(taskID, StatusCancelled); transErr != nil {
					e.logger.Error("failed to transition task to cancelled",
						zap.String("task_id", taskID), zap.Error(transErr))
				}
				_ = e.store.CreateTaskEvent(&TaskEvent{
					ID:        uuid.New().String(),
					TaskID:    taskID,
					EventType: "task.cancelled",
					EventData: mustMarshal(map[string]any{"error": runErr.Error()}),
				})
			} else if IsWorkerContext(ctx) {
				// Worker-executed: leave task in running state so the caller
				// (worker pool) can apply retry policy instead of us hard-failing.
				e.logger.Warn("task execution failed in worker context; retry policy applied by caller",
					zap.String("task_id", taskID), zap.Error(runErr))
			} else {
				e.failTask(ctx, taskID, fmt.Errorf("agent: %w", runErr))
			}
		}
		// Update plan status on failure.
		if task.PlanID != "" {
			_ = e.updatePlanStatus(taskID, task.PlanID, orchestration.PlanFailed)
		}
		return runErr
	}

	if result != nil {
		fromAgentRef(task, result)
		task.Status = Status(result.Status)
		if err := e.store.UpdateTask(task); err != nil {
			e.logger.Error("failed to persist agent result",
				zap.String("task_id", taskID), zap.Error(err))
		}

		// Update plan status to completed.
		if task.PlanID != "" {
			_ = e.updatePlanStatus(taskID, task.PlanID, orchestration.PlanCompleted)
		}

		// Run verification if orchestration pipeline is configured.
		if e.orchPipeline != nil && task.PlanID != "" {
			verifyOC := &orchestration.Context{
				TaskID: taskID,
				Input:  task.Input,
				Intent: &orchestration.Intent{TaskType: task.Intent},
			}
			if vResult, vErr := e.orchPipeline.Verify(ctx, verifyOC, task.Output); vErr == nil && vResult != nil {
				verifyEvent := map[string]any{
					"success": vResult.Success,
					"message": vResult.Message,
				}
				_ = e.store.CreateTaskEvent(&TaskEvent{
					ID:        uuid.New().String(),
					TaskID:    taskID,
					EventType: "verification.completed",
					EventData: mustMarshal(verifyEvent),
				})
				e.logger.Info("verification completed",
					zap.String("task_id", taskID),
					zap.Bool("success", vResult.Success),
					zap.String("message", vResult.Message),
				)
				// Verification failure is a warning, not a task failure.
				// Task completes successfully; verification result is recorded.
				if !vResult.Success {
					e.logger.Warn("verification failed for task",
						zap.String("task_id", taskID),
						zap.String("message", vResult.Message),
					)
				}
			}
		}

		e.logger.Info("task completed via agent",
			zap.String("task_id", taskID),
			zap.String("status", string(result.Status)),
			zap.Int("steps", result.StepCount),
			zap.String("intent", task.Intent),
			zap.String("provider", task.Provider),
			zap.String("model", task.Model),
		)
	}
	return nil
}

// updatePlanStatus transitions a plan to the given status and updates the task.
func (e *TaskExecutor) updatePlanStatus(taskID, planID, status string) error {
	ps, ok := e.store.(PlanStore)
	if !ok {
		return nil
	}
	plan, err := ps.GetPlan(planID)
	if err != nil {
		return err
	}
	plan.Status = status
	if status == orchestration.PlanCompleted || status == orchestration.PlanFailed {
		plan.CurrentStep = len(parsePlanSteps(plan.StepsJSON))
	}
	return ps.UpdatePlan(plan)
}

func parsePlanSteps(stepsJSON []byte) []orchestration.Step {
	if len(stepsJSON) == 0 {
		return nil
	}
	var steps []orchestration.Step
	if err := json.Unmarshal(stepsJSON, &steps); err != nil {
		return nil
	}
	return steps
}

func (e *TaskExecutor) defaultModel() (string, error) {
	entries, err := e.catalog.List(context.Background())
	if err != nil || len(entries) == 0 {
		return "", fmt.Errorf("no models available in catalog")
	}
	return entries[0].ModelID, nil
}

func (e *TaskExecutor) failTask(ctx context.Context, taskID string, err error) {
	if updateErr := e.store.UpdateStatus(taskID, StatusFailed); updateErr != nil {
		e.logger.Error("failed to transition task to failed",
			zap.String("task_id", taskID), zap.Error(updateErr))
	}
	if updateErr := e.store.FailTask(taskID, err.Error()); updateErr != nil {
		e.logger.Error("failed to update task error",
			zap.String("task_id", taskID), zap.Error(updateErr))
	}
	_ = e.store.CreateTaskEvent(&TaskEvent{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		EventType: "task.failed",
		EventData: mustMarshal(map[string]any{"error": err.Error()}),
	})
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
