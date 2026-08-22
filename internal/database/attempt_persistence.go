package database

import (
	"context"
	"sync"
	"time"

	"github.com/EffNine/conductor/internal/eventbus"
	"go.uber.org/zap"
)

// attemptSaveTimeout bounds a single async attempt save. Persistence must
// never block the request path; a timed-out save is logged and the record is
// lost (observability, not correctness).
const attemptSaveTimeout = 5 * time.Second

// maxConcurrentAttemptSaves bounds concurrent SQLite inserts spawned by the
// persistence consumer. When the limit is reached, additional records are
// dropped with a warning (matching the trace persistence semantics).
const maxConcurrentAttemptSaves = 16

// AttemptPersistence is the event-bus consumer that persists chat execution
// attempts. It subscribes to ExecutionAttemptCompleted (whose payload is an
// AttemptRecord published by the handler sink) and saves it to SQLite
// asynchronously, off the request path.
//
// Isolation contract: persistence failure, timeout, or saturation NEVER
// fails or alters the request. Save errors are logged only.
type AttemptPersistence struct {
	bus    *eventbus.EventBus
	store  *AttemptStore
	logger *zap.Logger
	subID  uint64
	sem    chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
}

// NewAttemptPersistence creates a consumer bound to the given bus and store.
func NewAttemptPersistence(bus *eventbus.EventBus, store *AttemptStore, logger *zap.Logger) *AttemptPersistence {
	return &AttemptPersistence{
		bus:    bus,
		store:  store,
		logger: logger,
		sem:    make(chan struct{}, maxConcurrentAttemptSaves),
	}
}

// Start subscribes to ExecutionAttemptCompleted. Safe to call once.
func (ap *AttemptPersistence) Start() {
	if ap.bus == nil || ap.store == nil || ap.subID != 0 {
		return
	}
	ap.subID = ap.bus.Subscribe(eventbus.ExecutionAttemptCompleted, ap.handle)
}

// Stop unsubscribes and waits for in-flight saves to finish.
func (ap *AttemptPersistence) Stop() {
	ap.mu.Lock()
	ap.closed = true
	if ap.subID != 0 && ap.bus != nil {
		ap.bus.Unsubscribe(eventbus.ExecutionAttemptCompleted, ap.subID)
	}
	ap.mu.Unlock()
	ap.wg.Wait()
}

// handle receives an ExecutionAttemptCompleted event and schedules an async
// save. It runs inside the publisher's goroutine and must return quickly.
func (ap *AttemptPersistence) handle(evt eventbus.Event) {
	rec, ok := evt.Payload.(AttemptRecord)
	if !ok {
		return
	}

	ap.mu.Lock()
	if ap.closed {
		ap.mu.Unlock()
		return
	}
	select {
	case ap.sem <- struct{}{}:
		ap.wg.Add(1)
	default:
		ap.mu.Unlock()
		ap.logger.Warn("attempt persistence: concurrency limit reached, dropping record",
			zap.String("request_id", rec.RequestID),
			zap.String("provider", rec.Provider))
		return
	}
	ap.mu.Unlock()

	go func() {
		defer func() { <-ap.sem }()
		defer ap.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), attemptSaveTimeout)
		defer cancel()
		if err := ap.store.Save(ctx, rec); err != nil {
			ap.logger.Warn("attempt persistence failed",
				zap.String("request_id", rec.RequestID),
				zap.String("provider", rec.Provider),
				zap.Error(err))
		}
	}()
}
