// Package runtime provides the Provider Runtime subsystem.
//
// ProviderRuntime is the single source of truth for all provider operational
// state including health, latency, success/failure counts, and circuit state.
package runtime

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/provider"
)

// ProviderRuntimeImpl is the concrete implementation of ProviderRuntime.
type ProviderRuntimeImpl struct {
	mu              sync.RWMutex
	name            string
	state           ProviderState
	lastHealthCheck time.Time
	latencyMs       atomic.Int64
	capacity        atomic.Int64 // stored as percentage (0-100)
	successCount    atomic.Int64
	failureCount    atomic.Int64
	totalRequests   atomic.Int64
	lastError       atomic.Value // stores error or nil
	isHealthy       atomic.Bool
	stateChanges    []StateChange
	stateMutex      sync.RWMutex
	createdAt       time.Time
	metadata        map[string]any
	tags            map[string]string
	breakerState    atomic.Int32 // maps to breaker.State

	// Execution telemetry counters (P3.7).
	executionCount        atomic.Int64
	executionSuccessCount atomic.Int64
	executionFailureCount atomic.Int64
	toolCallSuccessCount  atomic.Int64
	toolCallFailureCount  atomic.Int64
	retryCount            atomic.Int64

	// Model-level execution telemetry counters (P3.10).
	modelMu         sync.RWMutex
	modelExecutions map[string]*modelExecState
}

// modelExecState holds per-model atomic counters.
type modelExecState struct {
	execCount   atomic.Int64
	execSuccess atomic.Int64
	execFailure atomic.Int64
	toolSuccess atomic.Int64
	toolFailure atomic.Int64
	retryCount  atomic.Int64
}

// Ensure ProviderRuntimeImpl implements ProviderRuntime.
var _ ProviderRuntime = (*ProviderRuntimeImpl)(nil)

// NewProviderRuntime creates a new provider runtime instance.
func NewProviderRuntime(name string, p provider.Provider) *ProviderRuntimeImpl {
	r := &ProviderRuntimeImpl{
		name:            name,
		state:           StateUnknown,
		createdAt:       time.Now().UTC(),
		metadata:        make(map[string]any),
		tags:            make(map[string]string),
		modelExecutions: make(map[string]*modelExecState),
	}
	r.isHealthy.Store(true)
	r.capacity.Store(100)
	return r
}

// Name returns the provider identifier.
func (r *ProviderRuntimeImpl) Name() string {
	return r.name
}

// State returns the current runtime state.
func (r *ProviderRuntimeImpl) State() ProviderState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// Snapshot returns a point-in-time snapshot of the runtime state.
func (r *ProviderRuntimeImpl) Snapshot(ctx context.Context) ProviderStateSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	total := r.totalRequests.Load()
	failure := r.failureCount.Load()

	var errorRate float64
	if total > 0 {
		errorRate = float64(failure) / float64(total)
	}

	capacity := float64(r.capacity.Load()) / 100.0

	return ProviderStateSnapshot{
		State:                 r.state,
		LastHealthCheck:       r.lastHealthCheck,
		LatencyMs:             r.latencyMs.Load(),
		ErrorRate:             errorRate,
		Capacity:              capacity,
		Tags:                  copyTags(r.tags),
		ExecutionCount:        r.executionCount.Load(),
		ExecutionSuccessCount: r.executionSuccessCount.Load(),
		ExecutionFailureCount: r.executionFailureCount.Load(),
		ToolCallSuccessCount:  r.toolCallSuccessCount.Load(),
		ToolCallFailureCount:  r.toolCallFailureCount.Load(),
		RetryCount:            r.retryCount.Load(),
		ModelExecutions:       r.copyModelExecutions(),
	}
}

// RecordLatency records a latency measurement.
func (r *ProviderRuntimeImpl) RecordLatency(latencyMs int64) {
	r.latencyMs.Store(latencyMs)
	r.totalRequests.Add(1)
}

// RecordError records an error event.
func (r *ProviderRuntimeImpl) RecordError(err error) {
	r.failureCount.Add(1)
	r.totalRequests.Add(1)
	if err != nil {
		r.lastError.Store(err)
	}
}

// RecordSuccess records a successful request.
func (r *ProviderRuntimeImpl) RecordSuccess() {
	r.successCount.Add(1)
	r.totalRequests.Add(1)
}

// IsHealthy reports whether the provider is considered healthy.
func (r *ProviderRuntimeImpl) IsHealthy() bool {
	return r.isHealthy.Load()
}

// UpdateState transitions the provider to a new state.
func (r *ProviderRuntimeImpl) UpdateState(newState ProviderState, reason string, metadata map[string]any) {
	r.mu.Lock()
	oldState := r.state
	r.state = newState
	r.lastHealthCheck = time.Now().UTC()
	r.isHealthy.Store(newState == StateHealthy || newState == StateDegraded)
	r.mu.Unlock()

	// Record state change
	change := StateChange{
		ProviderName: r.name,
		From:         oldState,
		To:           newState,
		Reason:       reason,
		Timestamp:    time.Now().UTC(),
		Metadata:     copyMetadata(metadata),
	}

	r.stateMutex.Lock()
	r.stateChanges = append(r.stateChanges, change)
	// Keep only last 100 changes
	if len(r.stateChanges) > 100 {
		r.stateChanges = r.stateChanges[len(r.stateChanges)-100:]
	}
	r.stateMutex.Unlock()

	// Update metadata
	if metadata != nil {
		r.mu.Lock()
		for k, v := range metadata {
			r.metadata[k] = v
		}
		r.mu.Unlock()
	}
}

// GetStateChanges returns recent state changes.
func (r *ProviderRuntimeImpl) GetStateChanges() []StateChange {
	r.stateMutex.RLock()
	defer r.stateMutex.RUnlock()
	result := make([]StateChange, len(r.stateChanges))
	copy(result, r.stateChanges)
	return result
}

// GetMetadata returns provider metadata.
func (r *ProviderRuntimeImpl) GetMetadata() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return copyMetadata(r.metadata)
}

// SetMetadata updates provider metadata.
func (r *ProviderRuntimeImpl) SetMetadata(key string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metadata[key] = value
}

// GetTag returns a provider tag.
func (r *ProviderRuntimeImpl) GetTag(key string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.tags[key]
	return v, ok
}

// SetTag sets a provider tag.
func (r *ProviderRuntimeImpl) SetTag(key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tags[key] = value
}

// RecordExecutionOutcome records a chat completion execution result.
func (r *ProviderRuntimeImpl) RecordExecutionOutcome(success bool, retryCount int) {
	r.executionCount.Add(1)
	if success {
		r.executionSuccessCount.Add(1)
	} else {
		r.executionFailureCount.Add(1)
	}
	if retryCount > 0 {
		r.retryCount.Add(int64(retryCount))
	}
}

// RecordExecutionOutcomeModel records a chat completion execution result
// attributed to a specific model. When modelID is empty, falls back to
// provider-level recording.
func (r *ProviderRuntimeImpl) RecordExecutionOutcomeModel(modelID string, success bool, retryCount int) {
	r.RecordExecutionOutcome(success, retryCount)
	if modelID == "" {
		return
	}
	r.modelMu.Lock()
	st, ok := r.modelExecutions[modelID]
	if !ok {
		st = &modelExecState{}
		r.modelExecutions[modelID] = st
	}
	r.modelMu.Unlock()
	st.execCount.Add(1)
	if success {
		st.execSuccess.Add(1)
	} else {
		st.execFailure.Add(1)
	}
	if retryCount > 0 {
		st.retryCount.Add(int64(retryCount))
	}
}

// RecordToolCallOutcome records the result of a single tool call.
func (r *ProviderRuntimeImpl) RecordToolCallOutcome(success bool) {
	if success {
		r.toolCallSuccessCount.Add(1)
	} else {
		r.toolCallFailureCount.Add(1)
	}
}

// RecordToolCallOutcomeModel records a tool call result attributed to a
// specific model. When modelID is empty, falls back to provider-level.
func (r *ProviderRuntimeImpl) RecordToolCallOutcomeModel(modelID string, success bool) {
	r.RecordToolCallOutcome(success)
	if modelID == "" {
		return
	}
	r.modelMu.Lock()
	st, ok := r.modelExecutions[modelID]
	if !ok {
		st = &modelExecState{}
		r.modelExecutions[modelID] = st
	}
	r.modelMu.Unlock()
	if success {
		st.toolSuccess.Add(1)
	} else {
		st.toolFailure.Add(1)
	}
}

// copyModelExecutions returns a snapshot of per-model execution state.
func (r *ProviderRuntimeImpl) copyModelExecutions() map[string]ModelExecutionState {
	r.modelMu.RLock()
	defer r.modelMu.RUnlock()
	if len(r.modelExecutions) == 0 {
		return nil
	}
	out := make(map[string]ModelExecutionState, len(r.modelExecutions))
	for id, st := range r.modelExecutions {
		out[id] = ModelExecutionState{
			ExecutionCount:        st.execCount.Load(),
			ExecutionSuccessCount: st.execSuccess.Load(),
			ExecutionFailureCount: st.execFailure.Load(),
			ToolCallSuccessCount:  st.toolSuccess.Load(),
			ToolCallFailureCount:  st.toolFailure.Load(),
			RetryCount:            st.retryCount.Load(),
		}
	}
	return out
}

// GetUptime returns how long the runtime has been active.
func (r *ProviderRuntimeImpl) GetUptime() time.Duration {
	return time.Since(r.createdAt)
}

// GetStats returns operational statistics.
func (r *ProviderRuntimeImpl) GetStats() ProviderStats {
	total := r.totalRequests.Load()
	success := r.successCount.Load()
	failure := r.failureCount.Load()

	var lastErr string
	if err, ok := r.lastError.Load().(error); ok && err != nil {
		lastErr = err.Error()
	}

	return ProviderStats{
		TotalRequests: total,
		SuccessCount:  success,
		FailureCount:  failure,
		SuccessRate: func() float64 {
			if total == 0 {
				return 1.0
			}
			return float64(success) / float64(total)
		}(),
		ErrorRate: func() float64 {
			if total == 0 {
				return 0.0
			}
			return float64(failure) / float64(total)
		}(),
		LastError: lastErr,
	}
}

// SetBreakerState updates the circuit breaker state.
func (r *ProviderRuntimeImpl) SetBreakerState(state int32) {
	r.breakerState.Store(state)
}

// GetBreakerState returns the circuit breaker state.
func (r *ProviderRuntimeImpl) GetBreakerState() int32 {
	return r.breakerState.Load()
}

// SnapshotHash returns a hash of the current snapshot for change detection.
func (r *ProviderRuntimeImpl) SnapshotHash() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	h := fnv.New32a()
	h.Write([]byte(r.name))
	h.Write([]byte(r.state))
	h.Write([]byte(fmt.Sprintf("%d", r.latencyMs.Load())))
	h.Write([]byte(fmt.Sprintf("%f", float64(r.failureCount.Load())/float64(max(r.totalRequests.Load(), 1)))))
	return fmt.Sprintf("%08x", h.Sum32())
}

// ProviderStats holds operational statistics for a provider runtime.
type ProviderStats struct {
	TotalRequests int64
	SuccessCount  int64
	FailureCount  int64
	SuccessRate   float64
	ErrorRate     float64
	LastError     string
}

// RuntimeStore manages provider runtime instances.
type RuntimeStore struct {
	mu            sync.RWMutex
	runtimes      map[string]*ProviderRuntimeImpl
	eventBus      *eventbus.EventBus
	watchers      map[uint64]watcherEntry
	nextWatcherID atomic.Uint64
}

type watcherEntry struct {
	id           uint64
	providerName string
	callback     func(ProviderStateSnapshot)
}

// NewRuntimeStore creates a new runtime store.
func NewRuntimeStore(eventBus *eventbus.EventBus) *RuntimeStore {
	return &RuntimeStore{
		runtimes: make(map[string]*ProviderRuntimeImpl),
		eventBus: eventBus,
		watchers: make(map[uint64]watcherEntry),
	}
}

// Register adds a new provider runtime.
func (s *RuntimeStore) Register(runtime ProviderRuntime) error {
	if runtime == nil {
		return fmt.Errorf("runtime cannot be nil")
	}

	name := runtime.Name()
	if name == "" {
		return fmt.Errorf("runtime name cannot be empty")
	}

	impl, ok := runtime.(*ProviderRuntimeImpl)
	if !ok {
		return fmt.Errorf("runtime must be a ProviderRuntimeImpl")
	}

	s.mu.Lock()
	if _, exists := s.runtimes[name]; exists {
		s.mu.Unlock()
		return fmt.Errorf("provider %q already registered", name)
	}
	s.runtimes[name] = impl
	s.mu.Unlock()

	// Publish registration event
	if s.eventBus != nil {
		s.eventBus.Publish(context.Background(), eventbus.Event{
			Type:    eventbus.ProviderRegistered,
			Payload: name,
		})
	}

	return nil
}

// Deregister removes a runtime.
func (s *RuntimeStore) Deregister(name string) error {
	s.mu.Lock()
	runtime, exists := s.runtimes[name]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("provider %q not found", name)
	}
	delete(s.runtimes, name)
	s.mu.Unlock()

	// Notify watchers
	snap := runtime.Snapshot(context.Background())
	s.notifyWatchers(name, snap)

	// Publish deregistration event
	if s.eventBus != nil {
		s.eventBus.Publish(context.Background(), eventbus.Event{
			Type:    eventbus.ProviderDeregistered,
			Payload: name,
		})
	}

	return nil
}

// Get retrieves a runtime by name.
func (s *RuntimeStore) Get(name string) (ProviderRuntime, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	runtime, exists := s.runtimes[name]
	if !exists {
		return nil, fmt.Errorf("provider %q not found", name)
	}
	return runtime, nil
}

// GetAll returns all registered runtimes.
func (s *RuntimeStore) GetAll() ([]ProviderRuntime, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ProviderRuntime, 0, len(s.runtimes))
	for _, runtime := range s.runtimes {
		result = append(result, runtime)
	}
	return result, nil
}

// Snapshot returns a snapshot of all provider runtimes.
func (s *RuntimeStore) Snapshot(ctx context.Context) RuntimeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	providers := make(map[string]ProviderStateSnapshot, len(s.runtimes))
	healthy := 0
	degraded := 0
	unhealthy := 0
	var totalLatency int64

	for name, runtime := range s.runtimes {
		snap := runtime.Snapshot(ctx)
		providers[name] = snap
		totalLatency += snap.LatencyMs

		switch snap.State {
		case StateHealthy:
			healthy++
		case StateDegraded:
			degraded++
		case StateUnhealthy, StateRecovering:
			unhealthy++
		}
	}

	var avgLatency int64
	if len(providers) > 0 {
		avgLatency = totalLatency / int64(len(providers))
	}

	return RuntimeSnapshot{
		Timestamp: time.Now().UTC(),
		Providers: providers,
		GlobalState: GlobalRuntimeState{
			TotalProviders:     len(providers),
			HealthyProviders:   healthy,
			DegradedProviders:  degraded,
			UnhealthyProviders: unhealthy,
			AvgLatencyMs:       avgLatency,
		},
	}
}

// Update updates a provider's state.
func (s *RuntimeStore) Update(name string, updater func(ProviderRuntime) error) error {
	s.mu.RLock()
	runtime, exists := s.runtimes[name]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("provider %q not found", name)
	}

	if err := updater(runtime); err != nil {
		return err
	}

	// Notify watchers
	snap := runtime.Snapshot(context.Background())
	s.notifyWatchers(name, snap)

	// Publish state change event
	if s.eventBus != nil {
		s.eventBus.Publish(context.Background(), eventbus.Event{
			Type:    eventbus.ProviderStateChanged,
			Payload: snap,
		})
	}

	return nil
}

// Watch subscribes to provider state changes.
func (s *RuntimeStore) Watch(providerName string, callback func(ProviderStateSnapshot)) uint64 {
	id := s.nextWatcherID.Add(1)

	s.mu.Lock()
	s.watchers[id] = watcherEntry{
		id:           id,
		providerName: providerName,
		callback:     callback,
	}
	s.mu.Unlock()

	return id
}

// Unwatch unsubscribes from provider state changes.
func (s *RuntimeStore) Unwatch(id uint64) {
	s.mu.Lock()
	delete(s.watchers, id)
	s.mu.Unlock()
}

func (s *RuntimeStore) notifyWatchers(providerName string, snap ProviderStateSnapshot) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, entry := range s.watchers {
		if entry.providerName == "" || entry.providerName == providerName {
			go entry.callback(snap)
		}
	}
}

// Helper functions
func copyTags(tags map[string]string) map[string]string {
	if tags == nil {
		return nil
	}
	copied := make(map[string]string, len(tags))
	for k, v := range tags {
		copied[k] = v
	}
	return copied
}

func copyMetadata(meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	copied := make(map[string]any, len(meta))
	for k, v := range meta {
		copied[k] = v
	}
	return copied
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
