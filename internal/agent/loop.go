package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/EffNine/conductor/internal/apitypes"
	"github.com/EffNine/conductor/internal/router"
	"github.com/EffNine/conductor/internal/tool"
	"github.com/EffNine/conductor/internal/usage"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// defaultMaxSteps is the fallback when cfg.MaxSteps <= 0.
const defaultMaxSteps = DefaultMaxSteps

// AgentImpl is the concrete single-agent implementation.
type AgentImpl struct {
	cfg          Config
	store        AgentStore
	router       *router.Engine
	registry     *tool.Registry
	usageTracker *usage.Tracker
	logger       *zap.Logger
}

// New creates a new AgentImpl.
func New(cfg Config, store AgentStore, r *router.Engine, reg *tool.Registry, ut *usage.Tracker, l *zap.Logger) *AgentImpl {
	return &AgentImpl{
		cfg:          cfg,
		store:        store,
		router:       r,
		registry:     reg,
		usageTracker: ut,
		logger:       l,
	}
}

func (a *AgentImpl) Name() string { return "single-agent" }

// Execute runs the bounded multi-step loop for the given task. It does NOT
// perform status transitions — the caller (TaskExecutor) handles those.
func (a *AgentImpl) Execute(ctx context.Context, t *TaskRef) (*TaskRef, error) {
	// Use per-task MaxSteps if set; otherwise fall back to config default.
	maxSteps := a.cfg.MaxSteps
	if t.MaxSteps > 0 {
		maxSteps = t.MaxSteps
	}
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	// Build initial messages from task input.
	messages := buildInitialMessages(t)
	ctx2 := &AgentContext{
		TaskID:    t.ID,
		Messages:  messages,
		ToolState: make(map[string]any),
		Step:      0,
	}

	// Try to restore a checkpoint if the task was previously paused/interrupted.
	if t.Status == StatusPaused || t.Status == StatusRunning {
		if restored, err := Restore(a.store, t.ID); err == nil && restored != nil {
			ctx2 = restored
			a.logger.Info("resumed agent from checkpoint",
				zap.String("task_id", t.ID),
				zap.Int("restored_step", ctx2.Step),
			)
		}
	}

	promptTokens := 0
	completionTokens := 0
	totalTokens := 0

	for ctx2.Step < maxSteps {
		select {
		case <-ctx.Done():
			_ = Save(ctx, a.store, ctx2)
			return t, fmt.Errorf("agent loop cancelled at step %d", ctx2.Step)
		default:
		}

		ctx2.Step++
		stepStart := time.Now()

		// Emit step started event.
		_ = a.store.CreateTaskEvent(&Event{
			ID:        newUUID(),
			TaskID:    t.ID,
			EventType: "task.step.started",
			EventData: mustMarshal(map[string]any{"step": ctx2.Step, "max_steps": maxSteps}),
		})

		// Resolve provider/model for this step.
		resolved, _, err := a.router.ResolveWithFallbackAndContext(ctx, t.Model, ctx2.Messages)
		if err != nil {
			a.failTask(t.ID, fmt.Errorf("resolve route at step %d: %w", ctx2.Step, err))
			return t, err
		}

		req := &apitypes.ChatCompletionRequest{
			Model:    resolved.ProviderModelID,
			Messages: ctx2.Messages,
			Tools:    buildToolDefinitions(a.registry),
		}

		// Call the provider.
		resp, callErr := resolved.Provider.ChatCompletion(ctx, req)
		latencyMs := time.Since(stepStart).Milliseconds()

		stepRecord := &Step{
			ID:               newUUID(),
			TaskID:           t.ID,
			StepNumber:       ctx2.Step,
			Provider:         resolved.ProviderName,
			Model:            resolved.ProviderModelID,
			Request:          mustMarshal(req),
			LatencyMs:        latencyMs,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
		}

		if callErr != nil {
			errStr := callErr.Error()
			stepRecord.Status = "failed"
			stepRecord.Error = &errStr
			a.persistStep(stepRecord)
			a.recordUsage(t.ID, resolved, nil, latencyMs, true, callErr)
			a.failTask(t.ID, fmt.Errorf("provider call at step %d: %w", ctx2.Step, callErr))
			return t, callErr
		}

		stepRecord.Status = "completed"
		stepRecord.Response = mustMarshal(resp)
		stepRecord.PromptTokens = usageToIntPtr(resp.Usage, func(u *apitypes.Usage) int { return u.PromptTokens })
		stepRecord.CompletionTokens = usageToIntPtr(resp.Usage, func(u *apitypes.Usage) int { return u.CompletionTokens })
		stepRecord.TotalTokens = usageToIntPtr(resp.Usage, func(u *apitypes.Usage) int { return u.TotalTokens })
		promptTokens += stepRecord.PromptTokens
		completionTokens += stepRecord.CompletionTokens
		totalTokens += stepRecord.TotalTokens

		if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
			errStr := "empty response choices"
			stepRecord.Status = "failed"
			stepRecord.Error = &errStr
			a.persistStep(stepRecord)
			a.failTask(t.ID, fmt.Errorf("empty response at step %d", ctx2.Step))
			return t, fmt.Errorf("empty response at step %d", ctx2.Step)
		}

		choice := resp.Choices[0].Message
		a.persistStep(stepRecord)
		a.recordUsage(t.ID, resolved, resp.Usage, latencyMs, false, nil)

		// Emit step completed event.
		_ = a.store.CreateTaskEvent(&Event{
			ID:        newUUID(),
			TaskID:    t.ID,
			EventType: "task.step.completed",
			EventData: mustMarshal(map[string]any{"step": ctx2.Step, "latency_ms": latencyMs}),
		})

		// Check for tool calls.
		if len(choice.ToolCalls) > 0 {
			results, err := a.executeToolCalls(ctx, t.ID, choice.ToolCalls, ctx2)
			if err != nil {
				a.logger.Warn("tool call error at step",
					zap.String("task_id", t.ID),
					zap.Int("step", ctx2.Step),
					zap.Error(err),
				)
				ctx2.Messages = append(ctx2.Messages, apitypes.Message{
					Role: "assistant", ToolCalls: choice.ToolCalls,
				})
				for _, tc := range choice.ToolCalls {
					errStr := err.Error()
					ctx2.Messages = append(ctx2.Messages, apitypes.Message{
						Role: "tool", Content: "tool execution failed: " + errStr,
						ToolCallID: tc.Function.Name,
					})
					_ = a.store.CreateTaskToolCall(&ToolCall{
						ID: newUUID(), TaskID: t.ID, StepID: &stepRecord.ID,
						CallID: tc.ID, ToolName: tc.Function.Name,
						Arguments: []byte(tc.Function.Arguments),
						Error: strPtr(errStr), Status: "failed",
					})
				}
				select {
				case <-ctx.Done():
					if saveErr := Save(ctx, a.store, ctx2); saveErr != nil {
						a.logger.Error("failed to save checkpoint on cancel", zap.Error(saveErr))
					}
					return t, ctx.Err()
				default:
				}
				if saveErr := Save(ctx, a.store, ctx2); saveErr != nil {
					a.logger.Error("failed to save checkpoint", zap.Error(saveErr))
					return t, fmt.Errorf("checkpoint save failed: %w", saveErr)
				}
				continue
			}

			// Append assistant message and tool results.
			ctx2.Messages = append(ctx2.Messages, apitypes.Message{
				Role: "assistant", ToolCalls: choice.ToolCalls,
			})
			for i, tc := range choice.ToolCalls {
				ctx2.Messages = append(ctx2.Messages, apitypes.Message{
					Role: "tool", Content: results[i].Content,
					ToolCallID: tc.ID,
				})
			}
			if saveErr := Save(ctx, a.store, ctx2); saveErr != nil {
				a.logger.Error("failed to save checkpoint", zap.Error(saveErr))
				return t, fmt.Errorf("checkpoint save failed: %w", saveErr)
			}
			continue
		}

		// Final text response.
		output := choice.ContentString()
		now := time.Now().UTC()
		t.Status = StatusCompleted
		t.Output = output
		t.OutputJSON = mustMarshal(resp)
		t.Provider = resolved.ProviderName
		t.Model = resolved.ProviderModelID
		t.StepCount = ctx2.Step
		t.CompletedAt = &now
		if err := a.store.UpdateTask(t); err != nil {
			a.logger.Error("failed to update task after successful execution",
				zap.String("task_id", t.ID), zap.Error(err))
		}
		if err := a.store.UpdateStatus(t.ID, StatusCompleted); err != nil {
			a.logger.Error("failed to transition task to completed",
				zap.String("task_id", t.ID), zap.Error(err))
		}
		_ = a.store.CreateTaskEvent(&Event{
			ID:        newUUID(),
			TaskID:    t.ID,
			EventType: "task.completed",
			EventData: mustMarshal(map[string]any{"step": ctx2.Step, "output_len": len(output)}),
		})
		a.logger.Info("task completed",
			zap.String("task_id", t.ID),
			zap.String("provider", resolved.ProviderName),
			zap.String("model", resolved.ProviderModelID),
			zap.Int("steps", ctx2.Step),
			zap.Int64("latency_ms", latencyMs),
		)
		return t, nil
	}

	// Max steps exceeded — fail the task.
	errStr := fmt.Sprintf("max steps (%d) exceeded", maxSteps)
	a.failTask(t.ID, fmt.Errorf(errStr))
	return t, fmt.Errorf("%s", errStr)
}

func (a *AgentImpl) executeToolCalls(ctx context.Context, taskID string, calls []apitypes.ToolCall, ctx2 *AgentContext) ([]tool.ToolResult, error) {
	results := make([]tool.ToolResult, len(calls))
	for i, tc := range calls {
		name := tc.Function.Name
		args := json.RawMessage(tc.Function.Arguments)

		_ = a.store.CreateTaskEvent(&Event{
			ID:        newUUID(),
			TaskID:    taskID,
			EventType: "task.tool.started",
			EventData: mustMarshal(map[string]any{"tool": name, "call_id": tc.ID}),
		})

		t, ok := a.registry.Get(name)
		if !ok {
			errStr := fmt.Sprintf("unknown tool: %s", name)
			results[i] = tool.Failure(errStr)
			_ = a.store.CreateTaskToolCall(&ToolCall{
				ID: newUUID(), TaskID: taskID, CallID: tc.ID,
				ToolName: name, Arguments: args,
				Result: strPtr(errStr), Status: "failed",
			})
			_ = a.store.CreateTaskEvent(&Event{
				ID:        newUUID(),
				TaskID:    taskID,
				EventType: "task.tool.completed",
				EventData: mustMarshal(map[string]any{"tool": name, "call_id": tc.ID, "status": "failed"}),
			})
			continue
		}

		res, err := t.Execute(ctx, args)
		if err != nil {
			errStr := err.Error()
			results[i] = tool.Failure(errStr)
			_ = a.store.CreateTaskToolCall(&ToolCall{
				ID: newUUID(), TaskID: taskID, CallID: tc.ID,
				ToolName: name, Arguments: args,
				Error: &errStr, Status: "failed",
			})
			_ = a.store.CreateTaskEvent(&Event{
				ID:        newUUID(),
				TaskID:    taskID,
				EventType: "task.tool.completed",
				EventData: mustMarshal(map[string]any{"tool": name, "call_id": tc.ID, "status": "failed"}),
			})
			continue
		}

		resultStr := res.Content
		_ = a.store.CreateTaskToolCall(&ToolCall{
			ID: newUUID(), TaskID: taskID, CallID: tc.ID,
			ToolName: name, Arguments: args,
			Result: &resultStr, Status: "completed",
		})
		_ = a.store.CreateTaskEvent(&Event{
			ID:        newUUID(),
			TaskID:    taskID,
			EventType: "task.tool.completed",
			EventData: mustMarshal(map[string]any{"tool": name, "call_id": tc.ID, "status": "completed"}),
		})
		results[i] = res
	}
	return results, nil
}

func (a *AgentImpl) failTask(taskID string, err error) {
	if updateErr := a.store.UpdateStatus(taskID, StatusFailed); updateErr != nil {
		a.logger.Error("failed to transition task to failed",
			zap.String("task_id", taskID), zap.Error(updateErr))
	}
	if updateErr := a.store.FailTask(taskID, err.Error()); updateErr != nil {
		a.logger.Error("failed to update task error",
			zap.String("task_id", taskID), zap.Error(updateErr))
	}
	_ = a.store.CreateTaskEvent(&Event{
		ID:        newUUID(),
		TaskID:    taskID,
		EventType: "task.failed",
		EventData: mustMarshal(map[string]any{"error": err.Error()}),
	})
}

func (a *AgentImpl) persistStep(step *Step) {
	if err := a.store.CreateTaskStep(step); err != nil {
		a.logger.Error("failed to persist task step", zap.Error(err))
	}
}

func (a *AgentImpl) recordUsage(taskID string, resolved *router.ResolvedRoute, u *apitypes.Usage, latencyMs int64, failed bool, callErr error) {
	if a.usageTracker == nil {
		return
	}
	var errStr *string
	if callErr != nil {
		s := callErr.Error()
		errStr = &s
	}
	a.usageTracker.Record(&usage.Record{
		RequestID:        taskID,
		ModelID:          resolved.ModelID,
		ProviderModelID:  resolved.ProviderModelID,
		Provider:         resolved.ProviderName,
		PromptTokens:     usageToIntPtr(u, func(u *apitypes.Usage) int { return u.PromptTokens }),
		CompletionTokens: usageToIntPtr(u, func(u *apitypes.Usage) int { return u.CompletionTokens }),
		TotalTokens:      usageToIntPtr(u, func(u *apitypes.Usage) int { return u.TotalTokens }),
		Requests:         1,
		DurationMs:       latencyMs,
		LatencyMs:        latencyMs,
		StatusCode:       200,
		IsStream:         false,
		ErrorMessage:     errStr,
		CreatedAt:        time.Now().UTC(),
	})
}

func buildInitialMessages(t *TaskRef) []apitypes.Message {
	messages := []apitypes.Message{{Role: "user", Content: t.Input}}
	if len(t.InputJSON) > 0 {
		var extra struct {
			System string `json:"system"`
		}
		if err := json.Unmarshal(t.InputJSON, &extra); err == nil && extra.System != "" {
			messages = append([]apitypes.Message{{Role: "system", Content: extra.System}}, messages...)
		}
	}
	return messages
}

func buildToolDefinitions(reg *tool.Registry) []apitypes.Tool {
	list := reg.List()
	tools := make([]apitypes.Tool, 0, len(list))
	for _, t := range list {
		params := t.Params()
		tools = append(tools, apitypes.Tool{
			Type: "function",
			Function: apitypes.FunctionDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  params,
			},
		})
	}
	return tools
}

func usageToIntPtr(u *apitypes.Usage, fn func(*apitypes.Usage) int) int {
	if u == nil {
		return 0
	}
	return fn(u)
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func strPtr(s string) *string { return &s }

func newUUID() string { return uuid.New().String() }
