// Package eventbus provides a lightweight in-process publish/subscribe event bus.
//
// The bus is used for internal cross-subsystem communication. It supports
// typed events, context propagation, and thread-safe subscribe/unsubscribe.
// No external dependencies (Kafka, Redis, etc.) are required.
package eventbus

import (
	"context"
	"sync"
	"time"
)

// EventType identifies the kind of event being published.
type EventType string

const (
	// Provider lifecycle events
	ProviderInitializing EventType = "provider.initializing"
	ProviderRegistered   EventType = "provider.registered"
	ProviderReady        EventType = "provider.ready"
	ProviderUnavailable  EventType = "provider.unavailable"
	ProviderRecovering   EventType = "provider.recovering"
	ProviderRecovered    EventType = "provider.recovered"
	ProviderDeregistered EventType = "provider.deregistered"
	ProviderStateChanged EventType = "provider.state.changed"
	LatencyUpdated       EventType = "provider.latency.updated"
	HealthChanged        EventType = "provider.health.changed"
	FailureRecorded      EventType = "provider.failure.recorded"
	RecoveryDetected     EventType = "provider.recovery.detected"

	// Snapshot events
	RuntimeSnapshotCreated   EventType = "runtime.snapshot.created"
	RuntimeCheckpointCreated EventType = "runtime.checkpoint.created"

	// Model events
	ModelStatusChanged EventType = "model.status.changed"
	ModelProbed        EventType = "model.probed"

	// Routing events
	RoutingDecision      EventType = "routing.decision"
	RoutingConfigChanged EventType = "routing.config.changed"

	// Decision pipeline events
	DecisionStarted        EventType = "decision.started"
	IntentResolved         EventType = "intent.resolved"
	CapabilityResolved     EventType = "capability.resolved"
	CandidatesGenerated    EventType = "candidates.generated"
	ProviderSelected       EventType = "provider.selected"
	DecisionFinished       EventType = "decision.finished"
	DecisionTraceCreated   EventType = "decision.trace.created"
	DecisionTraceCompleted EventType = "decision.trace.completed"

	// Health events
	HealthProbeStarted   EventType = "health.probe.started"
	HealthProbeCompleted EventType = "health.probe.completed"

	// Usage events
	UsageRecorded EventType = "usage.recorded"

	// System events
	SystemShutdown     EventType = "system.shutdown"
	SystemConfigReload EventType = "system.config.reload"

	// Multi-agent coordination events (V2.6)
	TaskDelegated             EventType = "task.delegated"
	TaskChildStarted          EventType = "task.child.started"
	TaskChildCompleted        EventType = "task.child.completed"
	TaskChildFailed           EventType = "task.child.failed"
	TaskAggregationStarted    EventType = "task.aggregation.started"
	TaskAggregationCompleted  EventType = "task.aggregation.completed"
	TaskCoordinationCompleted EventType = "task.coordination.completed"
)

// Event represents a single message in the bus.
type Event struct {
	Type      EventType
	Payload   any
	Context   context.Context
	Timestamp int64
}

// Subscriber is a callback invoked when a matching event is published.
type Subscriber func(Event)

// EventBus is a lightweight in-process pub/sub bus with bounded subscriber concurrency.
type EventBus struct {
	mu            sync.RWMutex
	subscriptions map[EventType][]subscriberEntry
	sem           chan struct{} // bounded concurrency semaphore
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

type subscriberEntry struct {
	id         uint64
	subscriber Subscriber
	priority   int
}

var nextID uint64

// NewEventBus creates a new event bus with default worker count (4).
func NewEventBus() *EventBus {
	return NewEventBusWithWorkers(4)
}

// NewEventBusWithWorkers creates a new event bus with the given max concurrent subscriber calls.
func NewEventBusWithWorkers(workerCount int) *EventBus {
	if workerCount <= 0 {
		workerCount = 4
	}
	return &EventBus{
		subscriptions: make(map[EventType][]subscriberEntry),
		sem:           make(chan struct{}, workerCount),
		stopCh:        make(chan struct{}),
	}
}

// Subscribe registers a subscriber for the given event type.
// Returns a subscription ID that can be used to unsubscribe.
func (eb *EventBus) Subscribe(eventType EventType, sub Subscriber) uint64 {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	nextID++
	id := nextID
	eb.subscriptions[eventType] = append(eb.subscriptions[eventType], subscriberEntry{
		id:         id,
		subscriber: sub,
		priority:   0,
	})
	return id
}

// Unsubscribe removes a subscriber by its ID.
func (eb *EventBus) Unsubscribe(eventType EventType, id uint64) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	entries, ok := eb.subscriptions[eventType]
	if !ok {
		return
	}

	filtered := make([]subscriberEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.id != id {
			filtered = append(filtered, entry)
		}
	}
	eb.subscriptions[eventType] = filtered
}

// Publish sends an event to all subscribers of the given type with bounded concurrency.
// If the concurrency limit is reached, the event is dropped with a warning.
func (eb *EventBus) Publish(ctx context.Context, event Event) {
	eb.mu.RLock()
	entries, ok := eb.subscriptions[event.Type]
	eb.mu.RUnlock()

	if !ok || len(entries) == 0 {
		return
	}

	for _, entry := range entries {
		select {
		case <-eb.stopCh:
			return
		default:
		}

		select {
		case eb.sem <- struct{}{}:
			// slot acquired
		case <-time.After(5 * time.Second):
			return
		}

		eb.wg.Add(1)
		go func(sub Subscriber, evt Event) {
			defer func() { <-eb.sem }()
			defer eb.wg.Done()
			sub(evt)
		}(entry.subscriber, eventWithContext(event, ctx))
	}
}

// PublishSync sends an event synchronously to all subscribers.
// Use this when ordering matters or for testing.
func (eb *EventBus) PublishSync(ctx context.Context, event Event) {
	eb.mu.RLock()
	entries, ok := eb.subscriptions[event.Type]
	eb.mu.RUnlock()

	if !ok || len(entries) == 0 {
		return
	}

	for _, entry := range entries {
		eventWithContext := event
		eventWithContext.Context = ctx
		entry.subscriber(eventWithContext)
	}
}

// Stop signals the bus to stop accepting new publishes and waits for in-flight
// subscriber calls to complete.
func (eb *EventBus) Stop() {
	select {
	case <-eb.stopCh:
		// already closed
	default:
		close(eb.stopCh)
	}
	eb.wg.Wait()
}

func eventWithContext(event Event, ctx context.Context) Event {
	event.Context = ctx
	return event
}
