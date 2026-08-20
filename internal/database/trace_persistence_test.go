package database_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/eventbus"
	"github.com/EffNine/conductor/internal/router"
)

var errDiskFull = errors.New("disk full")

// TestTracePersistenceConsumer: the production consumer (TracePersistence)
// persists the DecisionFinished trace asynchronously into the SQLite store,
// ignores non-trace payloads, and stops accepting work after Stop.
func TestTracePersistenceConsumer(t *testing.T) {
	store, _ := newTestTraceStore(t)
	bus := eventbus.NewEventBus()
	tp := database.NewTracePersistence(bus, store, nil)
	tp.Start()
	defer tp.Stop()

	tr := sampleTrace("dec-consumer-1", time.Now().UTC())
	bus.PublishSync(context.Background(), eventbus.Event{
		Type:    eventbus.DecisionFinished,
		Payload: tr,
	})

	// Save is async: poll until visible.
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := store.Get(context.Background(), "dec-consumer-1")
		if err == nil && got != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace never persisted: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Non-trace payloads must be ignored, not crash the consumer.
	bus.PublishSync(context.Background(), eventbus.Event{
		Type:    eventbus.DecisionFinished,
		Payload: "not a trace",
	})
	tp.Stop()

	// After Stop, events are dropped.
	bus.PublishSync(context.Background(), eventbus.Event{
		Type:    eventbus.DecisionFinished,
		Payload: sampleTrace("dec-consumer-late", time.Now().UTC()),
	})
	time.Sleep(50 * time.Millisecond)
	if _, err := store.Get(context.Background(), "dec-consumer-late"); err != router.ErrTraceNotFound {
		t.Fatalf("expected no persistence after Stop, got err=%v", err)
	}
}

// TestTracePersistenceFailureIsolated: a failing store behind the real
// consumer must not panic or block the publisher.
func TestTracePersistenceFailureIsolated(t *testing.T) {
	bus := eventbus.NewEventBus()
	failing := &failingStore{}
	tp := database.NewTracePersistence(bus, failing, nil)
	tp.Start()
	defer tp.Stop()

	tr := sampleTrace("dec-consumer-fail", time.Now().UTC())
	// Must return promptly and not panic.
	bus.PublishSync(context.Background(), eventbus.Event{
		Type:    eventbus.DecisionFinished,
		Payload: tr,
	})
	time.Sleep(20 * time.Millisecond)
	if !failing.wasCalled() {
		t.Fatal("store never called")
	}
}

// failingStore simulates a broken persistence backend.
type failingStore struct {
	mu      sync.Mutex
	didCall bool
}

func (f *failingStore) Save(context.Context, *router.DecisionTrace) error {
	f.mu.Lock()
	f.didCall = true
	f.mu.Unlock()
	return errDiskFull
}

func (f *failingStore) Get(context.Context, router.DecisionID) (*router.DecisionTrace, error) {
	return nil, router.ErrTraceNotFound
}

func (f *failingStore) List(context.Context, router.TraceFilter) ([]router.DecisionTraceSummary, error) {
	return nil, nil
}

func (f *failingStore) wasCalled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.didCall
}
