package database

import (
	"context"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/router"
	"go.uber.org/zap"
)

// traceSaveTimeout bounds a single async trace save. Persistence must never
// block the routing path; a timed-out save is logged and the trace is lost
// (observability, not correctness).
const traceSaveTimeout = 5 * time.Second

// maxConcurrentTraceSaves bounds concurrent SQLite inserts spawned by the
// persistence consumer. SQLite serializes writers anyway; this prevents
// unbounded goroutine growth under heavy load. When the limit is reached,
// additional traces are dropped with a warning (matching the event bus's own
// bounded-concurrency drop semantics).
const maxConcurrentTraceSaves = 16

// TracePersistence is the event-bus consumer that persists completed routing
// decisions. It subscribes to DecisionFinished (whose payload is the final
// DecisionTrace, published by DecisionPipeline) and saves the trace to a
// router.TraceStore asynchronously, off the routing critical path.
//
// Isolation contract: persistence failure, timeout, or saturation NEVER
// fails or alters the routing request. Save errors are logged only.
type TracePersistence struct {
	bus    *eventbus.EventBus
	store  router.TraceStore
	logger *zap.Logger
	subID  uint64
	sem    chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
}

// NewTracePersistence creates a consumer over the given bus and store.
// The logger may be nil (a no-op logger is used).
func NewTracePersistence(bus *eventbus.EventBus, store router.TraceStore, logger *zap.Logger) *TracePersistence {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &TracePersistence{
		bus:    bus,
		store:  store,
		logger: logger,
		sem:    make(chan struct{}, maxConcurrentTraceSaves),
	}
}

// Start subscribes to DecisionFinished. Safe to call once.
func (tp *TracePersistence) Start() {
	if tp.bus == nil || tp.store == nil || tp.subID != 0 {
		return
	}
	tp.subID = tp.bus.Subscribe(eventbus.DecisionFinished, tp.handle)
}

// Stop unsubscribes and waits for in-flight saves to finish.
func (tp *TracePersistence) Stop() {
	tp.mu.Lock()
	tp.closed = true
	if tp.subID != 0 && tp.bus != nil {
		tp.bus.Unsubscribe(eventbus.DecisionFinished, tp.subID)
	}
	tp.mu.Unlock()
	tp.wg.Wait()
}

// handle receives a DecisionFinished event and schedules an async save. It
// runs synchronously inside the publisher's goroutine (the bus delivers
// DecisionFinished synchronously), so it must return quickly — it only
// acquires a slot and spawns a goroutine.
func (tp *TracePersistence) handle(evt eventbus.Event) {
	trace, ok := evt.Payload.(*router.DecisionTrace)
	if !ok || trace == nil {
		return
	}

	tp.mu.Lock()
	if tp.closed {
		tp.mu.Unlock()
		return
	}
	select {
	case tp.sem <- struct{}{}:
		tp.wg.Add(1)
	default:
		tp.mu.Unlock()
		tp.logger.Warn("trace persistence: concurrency limit reached, dropping trace",
			zap.String("decision_id", string(trace.DecisionID)))
		return
	}
	tp.mu.Unlock()

	go func() {
		defer func() { <-tp.sem }()
		defer tp.wg.Done()
		// The event's context belongs to the decision lifecycle and is
		// cancelled when the decision finishes; use a fresh background
		// context so the save is independent of the request.
		ctx, cancel := context.WithTimeout(context.Background(), traceSaveTimeout)
		defer cancel()
		if err := tp.store.Save(ctx, trace); err != nil {
			tp.logger.Warn("trace persistence failed",
				zap.String("decision_id", string(trace.DecisionID)),
				zap.Error(err))
		}
	}()
}
