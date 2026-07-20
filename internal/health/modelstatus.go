package health

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// ModelStatus is the cached reachability state for one catalog Model ID.
type ModelStatus struct {
	ModelID          string    `json:"model_id"`
	Provider         string    `json:"provider"`
	ProviderModelID  string    `json:"provider_model_id"`
	Reachable        bool      `json:"reachable"`
	LatencyMs        int64     `json:"latency_ms"`
	LastError        string    `json:"last_error,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
	ConsecutiveFails int       `json:"consecutive_fails"`
}

// ModelStatusStore tracks per-model online status from probes and live traffic.
type ModelStatusStore struct {
	mu                 sync.RWMutex
	statuses           map[string]*ModelStatus
	unhealthyThreshold int
	unknownAsReachable bool
	// filterReady is set after the first full probe pass completes. Until then
	// ShouldAdvertise keeps every model visible so Fly cold-starts / redeploys
	// do not briefly return an empty /v1/models list.
	filterReady bool
}

// NewModelStatusStore creates an empty store.
func NewModelStatusStore(unhealthyThreshold int, unknownAsReachable bool) *ModelStatusStore {
	if unhealthyThreshold <= 0 {
		unhealthyThreshold = 1
	}
	return &ModelStatusStore{
		statuses:           make(map[string]*ModelStatus),
		unhealthyThreshold: unhealthyThreshold,
		unknownAsReachable: unknownAsReachable,
	}
}

// MarkFilterReady enables reachability-based hiding after the first probe pass.
func (s *ModelStatusStore) MarkFilterReady() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filterReady = true
}

// FilterReady reports whether /v1/models may hide unreachable models.
func (s *ModelStatusStore) FilterReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filterReady
}

// RecordSuccess marks a model as reachable.
func (s *ModelStatusStore) RecordSuccess(modelID, provider, providerModelID string, latencyMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.getOrCreateLocked(modelID, provider, providerModelID)
	st.Reachable = true
	st.LatencyMs = latencyMs
	st.LastError = ""
	st.CheckedAt = time.Now().UTC()
	st.ConsecutiveFails = 0
}

// RecordFailure records a failed probe or live request.
// Neutral failures (rate limit, auth) do not change reachability.
func (s *ModelStatusStore) RecordFailure(modelID, provider, providerModelID, errMsg string, statusCode int) {
	if IsNeutralProbeFailure(statusCode, errMsg) {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.getOrCreateLocked(modelID, provider, providerModelID)
	st.LatencyMs = 0
	st.LastError = errMsg
	st.CheckedAt = time.Now().UTC()
	st.ConsecutiveFails++
	if st.ConsecutiveFails >= s.unhealthyThreshold {
		st.Reachable = false
	}
}

// IsReachable reports whether a model should be treated as online.
// known is false when the model has never been probed.
func (s *ModelStatusStore) IsReachable(modelID string) (reachable bool, known bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st, ok := s.statuses[modelID]
	if !ok {
		return s.unknownAsReachable, false
	}
	return st.Reachable, true
}

// ShouldAdvertise returns true if the model should appear in /v1/models when
// hideUnreachable is enabled.
//
// Rules:
//   - Confirmed failures (Reachable=false) are always hidden, including mid-pass
//     so the list shrinks toward available models without waiting for the full pass.
//   - Unprobed models stay visible until the first pass finishes (avoids empty
//     flicker on cold start), then follow unknownAsReachable (default false =
//     available-only).
func (s *ModelStatusStore) ShouldAdvertise(modelID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.statuses[modelID]
	if !ok {
		if !s.filterReady {
			return true
		}
		return s.unknownAsReachable
	}
	return st.Reachable
}

// Get returns a copy of the status for a model, or nil if unknown.
func (s *ModelStatusStore) Get(modelID string) *ModelStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.statuses[modelID]
	if !ok {
		return nil
	}
	cp := *st
	return &cp
}

// GetAll returns copies of all known statuses.
func (s *ModelStatusStore) GetAll() []*ModelStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*ModelStatus, 0, len(s.statuses))
	for _, st := range s.statuses {
		cp := *st
		out = append(out, &cp)
	}
	return out
}

func (s *ModelStatusStore) getOrCreateLocked(modelID, provider, providerModelID string) *ModelStatus {
	st, ok := s.statuses[modelID]
	if !ok {
		st = &ModelStatus{
			ModelID:         modelID,
			Provider:        provider,
			ProviderModelID: providerModelID,
			Reachable:       s.unknownAsReachable,
		}
		s.statuses[modelID] = st
	}
	if provider != "" {
		st.Provider = provider
	}
	if providerModelID != "" {
		st.ProviderModelID = providerModelID
	}
	return st
}

// IsNeutralProbeFailure reports errors that should not mark a model offline
// (rate limits, auth problems, transient capacity signals that are not model-specific).
func IsNeutralProbeFailure(statusCode int, errMsg string) bool {
	switch statusCode {
	case http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusForbidden:
		return true
	}
	msg := strings.ToLower(errMsg)
	if strings.Contains(msg, "rate limit") || strings.Contains(msg, "rate_limit") {
		return true
	}
	if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "authentication") {
		return true
	}
	if strings.Contains(msg, "forbidden") || strings.Contains(msg, "permission") {
		return true
	}
	return false
}

// IsUnreachableProbeFailure reports errors that indicate the model endpoint
// itself is missing, retired, or not callable. Only definitive signals count;
// transient outages, timeouts, and generic 400s must not hide working models.
func IsUnreachableProbeFailure(statusCode int, errMsg string) bool {
	if IsNeutralProbeFailure(statusCode, errMsg) || IsInconclusiveProbeFailure(statusCode, errMsg) {
		return false
	}
	switch statusCode {
	case http.StatusNotFound, http.StatusGone:
		return true
	}
	msg := strings.ToLower(errMsg)
	return strings.Contains(msg, "no such model") ||
		strings.Contains(msg, "model not found") ||
		strings.Contains(msg, "model_not_found") ||
		strings.Contains(msg, "does not exist")
}

// IsInconclusiveProbeFailure reports probe/live errors that should not change
// reachability (slow responses, capacity blips, client timeouts).
func IsInconclusiveProbeFailure(statusCode int, errMsg string) bool {
	switch statusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return true
	}
	if statusCode != 0 {
		return false
	}
	msg := strings.ToLower(errMsg)
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "eof")
}
