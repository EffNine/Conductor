package health

import "strings"

// State is the reachability lifecycle for a catalog model.
type State string

const (
	// StateHealthy means the latest probe passed and error rate is below threshold.
	StateHealthy State = "healthy"
	// StateUnknown means the model has never been probed (or was reset).
	StateUnknown State = "unknown"
	// StateDegraded means the probe passed but live error rate exceeds the unhealthy threshold.
	StateDegraded State = "degraded"
	// StateUnhealthy means probes failed and the model is not advertised.
	StateUnhealthy State = "unhealthy"
	// StateRecovering means probes failed and exponential backoff is scheduling retries.
	StateRecovering State = "recovering"
)

// IsAdvertisable reports whether a state may appear in /v1/models.
// Degraded stays visible (probe passed; elevated live error rate is advisory).
// Recovering and Unhealthy are hidden when hide_unreachable is enabled.
func (s State) IsAdvertisable(unknownAsReachable bool) bool {
	switch s {
	case StateHealthy, StateDegraded:
		return true
	case StateUnknown, "":
		return unknownAsReachable
	default:
		return false
	}
}

// DeriveState maps legacy Reachable/ConsecutiveFails into a State when
// State was not persisted (older DB rows).
func DeriveState(reachable bool, consecutiveFails int, known bool) State {
	if !known {
		return StateUnknown
	}
	if reachable {
		return StateHealthy
	}
	if consecutiveFails > 0 {
		return StateRecovering
	}
	return StateUnhealthy
}

// NormalizeState returns a known State, defaulting empty to unknown.
func NormalizeState(s State) State {
	switch State(strings.ToLower(string(s))) {
	case StateHealthy:
		return StateHealthy
	case StateDegraded:
		return StateDegraded
	case StateUnhealthy:
		return StateUnhealthy
	case StateRecovering:
		return StateRecovering
	case StateUnknown:
		return StateUnknown
	default:
		return StateUnknown
	}
}
