package health

import (
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/config"
)

// ErrorTracker tracks per-model success/failure rates over a sliding window
// and feeds degraded/healthy transitions into the status store.
type ErrorTracker struct {
	mu        sync.Mutex
	window    time.Duration
	unhealthy float64
	recovery  float64
	enabled   bool
	windows   map[string]*rollingWindow
	store     *ModelStatusStore
}

type rollingWindow struct {
	totalRequests int64
	totalErrors   int64
	windowStart   time.Time
}

// NewErrorTracker creates a tracker. store may be nil and set later via SetStore.
func NewErrorTracker(cfg config.ErrorTrackingConfig, store *ModelStatusStore) *ErrorTracker {
	cfg = NormalizeErrorTracking(cfg)
	return &ErrorTracker{
		window:    cfg.Window,
		unhealthy: cfg.UnhealthyThreshold,
		recovery:  cfg.RecoveryThreshold,
		enabled:   cfg.Enabled,
		windows:   make(map[string]*rollingWindow),
		store:     store,
	}
}

// NormalizeErrorTracking fills zero fields with defaults.
func NormalizeErrorTracking(cfg config.ErrorTrackingConfig) config.ErrorTrackingConfig {
	out := cfg
	if out.Window <= 0 {
		out.Window = 5 * time.Minute
	}
	if out.UnhealthyThreshold <= 0 {
		out.UnhealthyThreshold = 0.15
	}
	if out.RecoveryThreshold <= 0 {
		out.RecoveryThreshold = 0.05
	}
	return out
}

// SetStore attaches the status store used for degraded/healthy transitions.
func (et *ErrorTracker) SetStore(store *ModelStatusStore) {
	if et == nil {
		return
	}
	et.mu.Lock()
	et.store = store
	et.mu.Unlock()
}

// Enabled reports whether live error tracking is on.
func (et *ErrorTracker) Enabled() bool {
	return et != nil && et.enabled
}

// RecordRequest records one live request outcome and may mark the model degraded.
func (et *ErrorTracker) RecordRequest(modelID, provider, providerModelID string, success bool) {
	if et == nil || !et.enabled || modelID == "" {
		return
	}

	et.mu.Lock()
	w, ok := et.windows[modelID]
	if !ok {
		w = &rollingWindow{windowStart: time.Now().UTC()}
		et.windows[modelID] = w
	}
	if time.Since(w.windowStart) > et.window {
		w.totalRequests = 0
		w.totalErrors = 0
		w.windowStart = time.Now().UTC()
	}
	w.totalRequests++
	if !success {
		w.totalErrors++
	}
	rate := float64(0)
	if w.totalRequests > 0 {
		rate = float64(w.totalErrors) / float64(w.totalRequests)
	}
	store := et.store
	unhealthy := et.unhealthy
	recovery := et.recovery
	et.mu.Unlock()

	if store == nil {
		return
	}
	store.UpdateErrorRate(modelID, provider, providerModelID, rate, unhealthy, recovery)
}

// GetErrorRate returns the current window error rate for a model (0 if unknown).
func (et *ErrorTracker) GetErrorRate(modelID string) float64 {
	if et == nil || modelID == "" {
		return 0
	}
	et.mu.Lock()
	defer et.mu.Unlock()
	w, ok := et.windows[modelID]
	if !ok || w.totalRequests == 0 {
		return 0
	}
	if time.Since(w.windowStart) > et.window {
		return 0
	}
	return float64(w.totalErrors) / float64(w.totalRequests)
}
