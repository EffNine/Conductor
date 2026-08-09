package adapter

import (
	"github.com/EffNine/conductor/internal/breaker"
	"github.com/EffNine/conductor/internal/runtime"
)

// BreakerToRuntimeAdapter exposes circuit breaker state through Runtime.
type BreakerToRuntimeAdapter struct {
	store *runtime.RuntimeStore
}

// NewBreakerToRuntimeAdapter creates a new adapter.
func NewBreakerToRuntimeAdapter(store *runtime.RuntimeStore) *BreakerToRuntimeAdapter {
	return &BreakerToRuntimeAdapter{store: store}
}

// OnBreakerStateChange updates Runtime when breaker state changes.
func (a *BreakerToRuntimeAdapter) OnBreakerStateChange(providerName string, stats breaker.BreakerStats) {
	if a.store == nil {
		return
	}

	// Map breaker state to runtime state
	runtimeState := a.mapBreakerState(stats.State)

	_ = a.store.Update(providerName, func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtimeState, "circuit_breaker", map[string]any{
			"consecutive_fails": stats.ConsecutiveFails,
			"total_failures":    stats.TotalFailures,
			"total_successes":   stats.TotalSuccesses,
		})
		return nil
	})
}

// mapBreakerState converts breaker states to runtime states.
func (a *BreakerToRuntimeAdapter) mapBreakerState(state breaker.State) runtime.ProviderState {
	switch state {
	case breaker.StateClosed:
		return runtime.StateHealthy
	case breaker.StateOpen:
		return runtime.StateUnhealthy
	case breaker.StateHalfOpen:
		return runtime.StateRecovering
	default:
		return runtime.StateUnknown
	}
}
