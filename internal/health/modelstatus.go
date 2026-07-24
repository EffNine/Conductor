package health

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/config"
)

// ModelStatus is the cached reachability state for one catalog Model ID.
type ModelStatus struct {
	ModelID          string    `json:"model_id"`
	Provider         string    `json:"provider"`
	ProviderModelID  string    `json:"provider_model_id"`
	Reachable        bool      `json:"reachable"`
	State            State     `json:"state"`
	LatencyMs        int64     `json:"latency_ms"`
	LastError        string    `json:"last_error,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
	NextProbeTime    time.Time `json:"next_probe,omitempty"`
	ConsecutiveFails int       `json:"consecutive_fails"`
	ErrorRate        float64   `json:"error_rate"`
}

// StatusDetail is the enriched status payload for GET /api/models/status.
type StatusDetail struct {
	ModelID             string  `json:"id"`
	Provider            string  `json:"provider"`
	ProviderModelID     string  `json:"provider_model_id,omitempty"`
	State               State   `json:"state"`
	Reachable           bool    `json:"reachable"`
	LastProbe           *string `json:"last_probe,omitempty"`
	NextProbe           *string `json:"next_probe,omitempty"`
	ProbeError          *string `json:"probe_error"`
	ErrorRate           float64 `json:"error_rate"`
	ErrorRateWindow     string  `json:"error_rate_window,omitempty"`
	LatencyMs           int64   `json:"latency_ms"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	BackoffMultiplier   float64 `json:"backoff_multiplier,omitempty"`
	RetryCountdownMs    *int64  `json:"retry_countdown_ms,omitempty"`
}

// StatusPersistence stores reachability across process restarts.
type StatusPersistence interface {
	UpsertStatus(st ModelStatus) error
	SaveFilterState(allReady bool, readyProviders []string) error
}

// ModelStatusStore tracks per-model online status from probes and live traffic.
type ModelStatusStore struct {
	mu                 sync.RWMutex
	statuses           map[string]*ModelStatus
	unhealthyThreshold int
	unknownAsReachable bool
	// allProvidersReady is set after a full probe pass that covered every provider.
	// readyProviders tracks providers whose first scoped probe pass has completed.
	// Until a provider is ready, ShouldAdvertise keeps its unprobed models visible
	// so scoped probes (e.g. only nvidia_nim) do not hide models from other providers.
	allProvidersReady bool
	readyProviders    map[string]struct{}
	persist           StatusPersistence
	backoff           config.ProbeBackoffConfig
	errorWindow       time.Duration
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
		readyProviders:     make(map[string]struct{}),
		backoff:            NormalizeBackoff(config.ProbeBackoffConfig{Enabled: true}),
		errorWindow:        5 * time.Minute,
	}
}

// Configure applies extended health options (backoff, error-rate window).
func (s *ModelStatusStore) Configure(cfg config.ModelHealthConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg.UnhealthyThreshold > 0 {
		s.unhealthyThreshold = cfg.UnhealthyThreshold
	}
	s.unknownAsReachable = cfg.UnknownAsReachable
	s.backoff = NormalizeBackoff(cfg.Backoff)
	s.backoff.Enabled = cfg.Backoff.Enabled
	if cfg.ErrorTracking.Window > 0 {
		s.errorWindow = cfg.ErrorTracking.Window
	}
}

// SetBackoff replaces the backoff schedule used when recording failures.
func (s *ModelStatusStore) SetBackoff(cfg config.ProbeBackoffConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backoff = NormalizeBackoff(cfg)
	s.backoff.Enabled = cfg.Enabled
}

// SetPersistence attaches durable storage for status updates.
func (s *ModelStatusStore) SetPersistence(p StatusPersistence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persist = p
}

// Restore hydrates in-memory status from durable storage. Call once at startup
// before the first /v1/models request so available-only filtering survives
// cold starts (Fly auto-stop).
func (s *ModelStatusStore) Restore(statuses []ModelStatus, allReady bool, readyProviders []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range statuses {
		st := statuses[i]
		if st.State == "" {
			st.State = DeriveState(st.Reachable, st.ConsecutiveFails, true)
		} else {
			st.State = NormalizeState(st.State)
		}
		st.Reachable = st.State == StateHealthy || st.State == StateDegraded
		cp := st
		s.statuses[st.ModelID] = &cp
	}
	s.allProvidersReady = allReady
	for _, name := range readyProviders {
		if name != "" {
			s.readyProviders[name] = struct{}{}
		}
	}
}

// MarkFilterReady enables reachability-based hiding for all providers after a
// full probe pass.
func (s *ModelStatusStore) MarkFilterReady() {
	s.mu.Lock()
	s.allProvidersReady = true
	ready := s.readyProviderNamesLocked()
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		_ = persist.SaveFilterState(true, ready)
	}
}

// MarkProviderFilterReady enables reachability-based hiding for one provider
// after its first scoped probe pass completes.
func (s *ModelStatusStore) MarkProviderFilterReady(provider string) {
	s.mu.Lock()
	if provider != "" {
		s.readyProviders[provider] = struct{}{}
	}
	allReady := s.allProvidersReady
	ready := s.readyProviderNamesLocked()
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		_ = persist.SaveFilterState(allReady, ready)
	}
}

func (s *ModelStatusStore) readyProviderNamesLocked() []string {
	out := make([]string, 0, len(s.readyProviders))
	for name := range s.readyProviders {
		out = append(out, name)
	}
	return out
}

// FilterReady reports whether /v1/models may hide unreachable models globally.
func (s *ModelStatusStore) FilterReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allProvidersReady
}

// ProviderFilterReady reports whether a specific provider has finished its
// first probe pass.
func (s *ModelStatusStore) ProviderFilterReady(provider string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.allProvidersReady {
		return true
	}
	_, ok := s.readyProviders[provider]
	return ok
}

// ApplyBatch applies a batch of probe results under a single write lock so
// catalog readers see an atomic swap of health state.
func (s *ModelStatusStore) ApplyBatch(results []ProbeResult) {
	if len(results) == 0 {
		return
	}
	s.mu.Lock()
	toPersist := make([]ModelStatus, 0, len(results))
	for _, r := range results {
		if r.Skip || r.ModelID == "" {
			continue
		}
		if r.Success {
			st := s.applySuccessLocked(r.ModelID, r.Provider, r.ProviderModelID, r.LatencyMs)
			toPersist = append(toPersist, *st)
			continue
		}
		if IsNeutralProbeFailure(r.StatusCode, r.ErrMsg) {
			continue
		}
		// Inconclusive failures do not hide healthy models, but if already
		// recovering they still advance backoff so we keep retrying.
		if IsInconclusiveProbeFailure(r.StatusCode, r.ErrMsg) {
			st, ok := s.statuses[r.ModelID]
			if !ok || (st.State != StateRecovering && st.State != StateUnhealthy) {
				continue
			}
			st = s.applyFailureLocked(r.ModelID, r.Provider, r.ProviderModelID, r.ErrMsg, true)
			toPersist = append(toPersist, *st)
			continue
		}
		st := s.applyFailureLocked(r.ModelID, r.Provider, r.ProviderModelID, r.ErrMsg, true)
		toPersist = append(toPersist, *st)
	}
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		for _, st := range toPersist {
			_ = persist.UpsertStatus(st)
		}
	}
}

// RecordSuccess marks a model as reachable.
func (s *ModelStatusStore) RecordSuccess(modelID, provider, providerModelID string, latencyMs int64) {
	s.mu.Lock()
	st := s.applySuccessLocked(modelID, provider, providerModelID, latencyMs)
	cp := *st
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		_ = persist.UpsertStatus(cp)
	}
}

func (s *ModelStatusStore) applySuccessLocked(modelID, provider, providerModelID string, latencyMs int64) *ModelStatus {
	st := s.getOrCreateLocked(modelID, provider, providerModelID)
	st.Reachable = true
	st.State = StateHealthy
	st.LatencyMs = latencyMs
	st.LastError = ""
	st.CheckedAt = time.Now().UTC()
	st.ConsecutiveFails = 0
	st.NextProbeTime = time.Time{}
	return st
}

// RecordFailure records a failed probe or live request.
// Neutral failures (rate limit, auth) do not change reachability.
func (s *ModelStatusStore) RecordFailure(modelID, provider, providerModelID, errMsg string, statusCode int) {
	if IsNeutralProbeFailure(statusCode, errMsg) {
		return
	}
	// Definitive unreachable OR generic non-inconclusive failure counts.
	if IsInconclusiveProbeFailure(statusCode, errMsg) {
		return
	}

	s.mu.Lock()
	st := s.applyFailureLocked(modelID, provider, providerModelID, errMsg, true)
	cp := *st
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		_ = persist.UpsertStatus(cp)
	}
}

func (s *ModelStatusStore) applyFailureLocked(modelID, provider, providerModelID, errMsg string, countTowardThreshold bool) *ModelStatus {
	st := s.getOrCreateLocked(modelID, provider, providerModelID)
	st.LatencyMs = 0
	st.LastError = errMsg
	st.CheckedAt = time.Now().UTC()
	if countTowardThreshold {
		st.ConsecutiveFails++
	} else if st.ConsecutiveFails == 0 {
		st.ConsecutiveFails = 1
	} else {
		st.ConsecutiveFails++
	}
	if st.ConsecutiveFails >= s.unhealthyThreshold {
		st.Reachable = false
		if s.backoff.Enabled {
			st.State = StateRecovering
			st.NextProbeTime = NextProbeAfter(s.backoff, st.ConsecutiveFails, st.CheckedAt)
		} else {
			st.State = StateUnhealthy
			st.NextProbeTime = time.Time{}
		}
	}
	return st
}

// UpdateErrorRate applies a live error-rate sample. Marks degraded when above
// unhealthy threshold; restores healthy when below recovery threshold (and
// currently degraded with a prior successful probe).
func (s *ModelStatusStore) UpdateErrorRate(modelID, provider, providerModelID string, rate, unhealthyThreshold, recoveryThreshold float64) {
	s.mu.Lock()
	st := s.getOrCreateLocked(modelID, provider, providerModelID)
	st.ErrorRate = rate
	changed := false
	switch {
	case rate > unhealthyThreshold && (st.State == StateHealthy || st.State == StateDegraded || st.State == StateUnknown):
		// Degraded: still advertised (probe had passed / unknown) but elevated errors.
		if st.State != StateRecovering && st.State != StateUnhealthy {
			st.State = StateDegraded
			st.Reachable = true
			changed = true
		}
	case rate < recoveryThreshold && st.State == StateDegraded:
		st.State = StateHealthy
		st.Reachable = true
		changed = true
	}
	cp := *st
	persist := s.persist
	s.mu.Unlock()
	if changed && persist != nil {
		_ = persist.UpsertStatus(cp)
	}
}

// ModelsNeedingRetry returns models in recovering/unhealthy whose NextProbeTime has passed.
func (s *ModelStatusStore) ModelsNeedingRetry(now time.Time) []ModelStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := make([]ModelStatus, 0)
	for _, st := range s.statuses {
		if st.State != StateRecovering && st.State != StateUnhealthy {
			continue
		}
		if !st.NextProbeTime.IsZero() && now.Before(st.NextProbeTime) {
			continue
		}
		cp := *st
		out = append(out, cp)
	}
	return out
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
	switch st.State {
	case StateHealthy, StateDegraded:
		return true, true
	case StateUnknown, "":
		return s.unknownAsReachable, true
	default:
		return st.Reachable, true
	}
}

// ShouldAdvertise returns true if the model should appear in /v1/models when
// hideUnreachable is enabled.
//
// Rules:
//   - Healthy and Degraded models are advertised.
//   - Recovering / Unhealthy (confirmed failures after unhealthyThreshold) are
//     always hidden, including mid-pass, so the list shrinks toward available
//     models without waiting for the full pass.
//   - Sub-threshold failures keep the model visible until unhealthyThreshold is
//     reached, matching the documented hide-after-N behavior.
//   - Unprobed models stay visible until their provider's first probe pass
//     finishes (avoids empty flicker on cold start and on scoped probes), then
//     follow unknownAsReachable (default true = err toward availability).
func (s *ModelStatusStore) ShouldAdvertise(modelID, provider string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.statuses[modelID]
	if !ok {
		if !s.providerFilterReadyLocked(provider) {
			return true
		}
		return s.unknownAsReachable
	}
	// A model is only confirmed unreachable once it hits the unhealthy threshold.
	// Before that, keep it visible so sub-threshold blips do not hide it.
	if st.ConsecutiveFails > 0 && st.ConsecutiveFails < s.unhealthyThreshold {
		return true
	}
	state := st.State
	if state == "" {
		state = DeriveState(st.Reachable, st.ConsecutiveFails, true)
	}
	switch state {
	case StateHealthy, StateDegraded:
		return true
	case StateRecovering, StateUnhealthy:
		return false
	case StateUnknown:
		if !s.providerFilterReadyLocked(provider) {
			return true
		}
		return s.unknownAsReachable
	default:
		return st.Reachable
	}
}

func (s *ModelStatusStore) providerFilterReadyLocked(provider string) bool {
	if s.allProvidersReady {
		return true
	}
	if provider == "" {
		return false
	}
	_, ok := s.readyProviders[provider]
	return ok
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

// GetStatusDetails returns enriched status rows for the dashboard API.
func (s *ModelStatusStore) GetStatusDetails() []StatusDetail {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	out := make([]StatusDetail, 0, len(s.statuses))
	for _, st := range s.statuses {
		d := StatusDetail{
			ModelID:             st.ModelID,
			Provider:            st.Provider,
			ProviderModelID:     st.ProviderModelID,
			State:               st.State,
			Reachable:           st.Reachable,
			ErrorRate:           st.ErrorRate,
			LatencyMs:           st.LatencyMs,
			ConsecutiveFailures: st.ConsecutiveFails,
		}
		if s.errorWindow > 0 {
			d.ErrorRateWindow = s.errorWindow.String()
		}
		if !st.CheckedAt.IsZero() {
			ts := st.CheckedAt.UTC().Format(time.RFC3339)
			d.LastProbe = &ts
		}
		if !st.NextProbeTime.IsZero() {
			ts := st.NextProbeTime.UTC().Format(time.RFC3339)
			d.NextProbe = &ts
			remain := st.NextProbeTime.Sub(now).Milliseconds()
			if remain < 0 {
				remain = 0
			}
			d.RetryCountdownMs = &remain
		}
		if st.LastError != "" {
			errMsg := st.LastError
			d.ProbeError = &errMsg
		} else {
			d.ProbeError = nil
		}
		if st.State == StateRecovering && s.backoff.Enabled {
			d.BackoffMultiplier = s.backoff.Multiplier
		}
		out = append(out, d)
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
			State:           StateUnknown,
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
