package adapter

import (
	"context"

	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/runtime"
)

// ExecutionToRuntimeAdapter translates execution telemetry into Runtime updates.
type ExecutionToRuntimeAdapter struct {
	store    *runtime.RuntimeStore
	eventBus *eventbus.EventBus
}

// NewExecutionToRuntimeAdapter creates a new adapter.
func NewExecutionToRuntimeAdapter(store *runtime.RuntimeStore, eventBus *eventbus.EventBus) *ExecutionToRuntimeAdapter {
	return &ExecutionToRuntimeAdapter{store: store, eventBus: eventBus}
}

// OnExecutionStarted publishes an ExecutionStarted event and prepares telemetry.
func (a *ExecutionToRuntimeAdapter) OnExecutionStarted(providerName string) {
	if a.eventBus != nil {
		a.eventBus.Publish(context.Background(), eventbus.Event{
			Type:    eventbus.ExecutionStarted,
			Payload: providerName,
		})
	}
}

// OnExecutionFinished records execution outcome on the runtime and publishes.
func (a *ExecutionToRuntimeAdapter) OnExecutionFinished(providerName string, modelID string, success bool, retryCount int) {
	if a.store != nil {
		_ = a.store.Update(providerName, func(r runtime.ProviderRuntime) error {
			r.RecordExecutionOutcomeModel(modelID, success, retryCount)
			return nil
		})
	}
	if a.eventBus != nil {
		a.eventBus.Publish(context.Background(), eventbus.Event{
			Type: eventbus.ExecutionFinished,
			Payload: map[string]any{
				"provider":    providerName,
				"model":       modelID,
				"success":     success,
				"retry_count": retryCount,
			},
		})
	}
}

// OnToolCall records a tool call outcome on the runtime and publishes.
func (a *ExecutionToRuntimeAdapter) OnToolCall(providerName string, modelID string, success bool) {
	if a.store != nil {
		_ = a.store.Update(providerName, func(r runtime.ProviderRuntime) error {
			r.RecordToolCallOutcomeModel(modelID, success)
			return nil
		})
	}
	if a.eventBus != nil {
		a.eventBus.Publish(context.Background(), eventbus.Event{
			Type: eventbus.ToolCallCompleted,
			Payload: map[string]any{
				"provider": providerName,
				"model":    modelID,
				"success":  success,
			},
		})
	}
}
