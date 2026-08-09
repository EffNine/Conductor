package adapter

import (
	"github.com/EffNine/conductor/internal/runtime"
	"github.com/EffNine/conductor/internal/usage"
)

// UsageToRuntimeAdapter translates usage records into Runtime statistics.
type UsageToRuntimeAdapter struct {
	store *runtime.RuntimeStore
}

// NewUsageToRuntimeAdapter creates a new adapter.
func NewUsageToRuntimeAdapter(store *runtime.RuntimeStore) *UsageToRuntimeAdapter {
	return &UsageToRuntimeAdapter{store: store}
}

// OnUsageRecord processes a usage record and updates Runtime.
func (a *UsageToRuntimeAdapter) OnUsageRecord(record *usage.Record) {
	if a.store == nil || record == nil {
		return
	}

	providerName := record.Provider
	if providerName == "" {
		return
	}

	latencyMs := record.LatencyMs
	if latencyMs == 0 {
		latencyMs = record.DurationMs
	}

	_ = a.store.Update(providerName, func(r runtime.ProviderRuntime) error {
		r.RecordLatency(latencyMs)
		if record.ErrorMessage != nil && *record.ErrorMessage != "" {
			r.RecordError(nil)
		} else {
			r.RecordSuccess()
		}
		return nil
	})
}
