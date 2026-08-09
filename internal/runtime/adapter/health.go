// Package adapter provides adapters that bridge existing subsystems to Runtime.
//
// Adapters translate health probe results, circuit breaker states, and usage
// records into Runtime updates without changing existing package behavior.
package adapter

import (
	"github.com/EffNine/conductor/internal/health"
	"github.com/EffNine/conductor/internal/provider"
	"github.com/EffNine/conductor/internal/runtime"
)

// HealthToRuntimeAdapter translates health probe results into Runtime state updates.
type HealthToRuntimeAdapter struct {
	store *runtime.RuntimeStore
}

// NewHealthToRuntimeAdapter creates a new adapter.
func NewHealthToRuntimeAdapter(store *runtime.RuntimeStore) *HealthToRuntimeAdapter {
	return &HealthToRuntimeAdapter{store: store}
}

// OnProbeResult processes a probe result and updates Runtime state.
func (a *HealthToRuntimeAdapter) OnProbeResult(result health.ProbeResult) {
	if a.store == nil {
		return
	}

	// Extract provider name from catalog ID (format: "provider/model")
	providerName := extractProviderName(result.Provider)
	if providerName == "" {
		return
	}

	runtimeState := a.mapHealthState(result.Success, result.ErrMsg)
	reason := a.mapHealthReason(result.Success, result.StatusCode, result.ErrMsg)

	_ = a.store.Update(providerName, func(r runtime.ProviderRuntime) error {
		r.UpdateState(runtimeState, reason, map[string]any{
			"latency_ms": result.LatencyMs,
			"model_id":   result.ModelID,
		})
		return nil
	})
}

// OnHealthCheck processes a provider health check result.
func (a *HealthToRuntimeAdapter) OnHealthCheck(status *provider.HealthStatus) {
	if a.store == nil || status == nil {
		return
	}

	runtimeState := runtime.StateHealthy
	if !status.IsHealthy {
		runtimeState = runtime.StateUnhealthy
	}

	_ = a.store.Update(status.Provider, func(r runtime.ProviderRuntime) error {
		r.RecordLatency(status.LatencyMs)
		if !status.IsHealthy {
			r.RecordError(nil)
		} else {
			r.RecordSuccess()
		}
		r.UpdateState(runtimeState, "health_check", nil)
		return nil
	})
}

// OnLiveResult processes a live request result for runtime updates.
func (a *HealthToRuntimeAdapter) OnLiveResult(providerName string, err error, latencyMs int64) {
	if a.store == nil {
		return
	}

	_ = a.store.Update(providerName, func(r runtime.ProviderRuntime) error {
		r.RecordLatency(latencyMs)
		if err != nil {
			r.RecordError(err)
		} else {
			r.RecordSuccess()
		}
		return nil
	})
}

// mapHealthState converts health states to runtime states.
func (a *HealthToRuntimeAdapter) mapHealthState(success bool, errMsg string) runtime.ProviderState {
	if success {
		return runtime.StateHealthy
	}
	if isTransientError(errMsg) {
		return runtime.StateRecovering
	}
	return runtime.StateUnhealthy
}

// mapHealthReason converts error info to human-readable reasons.
func (a *HealthToRuntimeAdapter) mapHealthReason(success bool, statusCode int, errMsg string) string {
	if success {
		return "probe_passed"
	}
	if statusCode == 404 || statusCode == 410 {
		return "model_not_found"
	}
	if isTransientError(errMsg) {
		return "transient_failure"
	}
	return "probe_failed"
}

// isTransientError reports whether an error is transient (timeout, connection reset).
func isTransientError(errMsg string) bool {
	if errMsg == "" {
		return false
	}
	lower := errMsg
	switch {
	case containsAny(lower, "timeout", "deadline", "context canceled"):
		return true
	case containsAny(lower, "connection reset", "eof", "network"):
		return true
	}
	return false
}

// containsAny checks if s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if contains(s, sub) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// extractProviderName extracts provider name from catalog ID.
func extractProviderName(catalogID string) string {
	// Handle format "provider/model" or "provider/name/sub"
	idx := indexOf(catalogID, "/")
	if idx < 0 {
		return catalogID
	}
	return catalogID[:idx]
}

// RuntimeStateFromHealth is a helper to convert health state to runtime state.
func RuntimeStateFromHealth(hState health.State) runtime.ProviderState {
	switch hState {
	case health.StateHealthy:
		return runtime.StateHealthy
	case health.StateDegraded:
		return runtime.StateDegraded
	case health.StateUnhealthy:
		return runtime.StateUnhealthy
	case health.StateRecovering:
		return runtime.StateRecovering
	default:
		return runtime.StateUnknown
	}
}
