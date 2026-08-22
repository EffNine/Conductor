package database

import (
	"context"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/eventbus"
	"go.uber.org/zap"
)

func attemptFixture(reqID string) AttemptRecord {
	return AttemptRecord{
		RequestID: reqID, CorrelationID: "corr-" + reqID,
		VirtualModel: "frontier", Mode: "auto",
		Provider: "mistral", ProviderModelID: "ministral-8b-latest",
		CandidateIndex: 1, AttemptIndex: 0,
		FailureClass: "upstream_error", Outcome: AttemptOutcomeFailed,
		HTTPStatus: 500, LatencyMS: 120, RetryWaitMS: 250,
		RetryAfterHonored: true,
	}
}

// waitForAttempts polls until the store holds at least want rows. Publish
// dispatches asynchronously, so tests must wait for delivery instead of
// racing the bus goroutines.
func waitForAttempts(t *testing.T, store *AttemptStore, want int) []RequestAttempt {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := store.ListAttempts(context.Background(), 10)
		if err == nil && len(rows) >= want {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d attempts; have %d (err=%v)", want, len(rows), err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAttemptPersistencePersistsPublishedEvents covers the full bus →
// consumer → SQLite path.
func TestAttemptPersistencePersistsPublishedEvents(t *testing.T) {
	db := newFileDB(t)
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewAttemptStore(db)
	bus := eventbus.NewEventBus()
	persistence := NewAttemptPersistence(bus, store, zap.NewNop())
	persistence.Start()

	rec := attemptFixture("req-1")
	bus.PublishSync(context.Background(), eventbus.Event{Type: eventbus.ExecutionAttemptCompleted, Payload: rec})

	// Stop waits for in-flight saves to finish.
	persistence.Stop()

	rows, err := store.ListAttempts(context.Background(), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.RequestID != "req-1" || got.Provider != "mistral" ||
		got.Outcome != AttemptOutcomeFailed || got.FailureClass != "upstream_error" ||
		got.HTTPStatus != 500 || !got.RetryAfterHonored || got.RetryWaitMS != 250 ||
		got.CandidateIndex != 1 || got.Mode != "auto" {
		t.Fatalf("row mapping drifted: %+v", got)
	}
}

// TestAttemptPersistenceClosedConsumerDropsSafely proves the isolation
// contract: a stopped consumer swallows events without panicking and never
// corrupts prior data. Request execution is unaffected either way.
func TestAttemptPersistenceClosedConsumerDropsSafely(t *testing.T) {
	db := newFileDB(t)
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewAttemptStore(db)
	bus := eventbus.NewEventBus()
	persistence := NewAttemptPersistence(bus, store, zap.NewNop())
	persistence.Start()
	bus.Publish(context.Background(), eventbus.Event{Type: eventbus.ExecutionAttemptCompleted, Payload: attemptFixture("kept")})
	waitForAttempts(t, store, 1)
	persistence.Stop()

	// After Stop the consumer is closed; late events are dropped safely.
	bus.Publish(context.Background(), eventbus.Event{Type: eventbus.ExecutionAttemptCompleted, Payload: attemptFixture("late")})
	time.Sleep(20 * time.Millisecond)

	rows, err := store.ListAttempts(context.Background(), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "kept" {
		t.Fatalf("unexpected rows after close: %+v", rows)
	}
}

// TestAttemptPersistenceIgnoresForeignPayloads ensures type mismatches are
// skipped silently (other publishers may share the bus).
func TestAttemptPersistenceIgnoresForeignPayloads(t *testing.T) {
	db := newFileDB(t)
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewAttemptStore(db)
	bus := eventbus.NewEventBus()
	persistence := NewAttemptPersistence(bus, store, zap.NewNop())
	persistence.Start()

	bus.Publish(context.Background(), eventbus.Event{Type: eventbus.ExecutionAttemptCompleted, Payload: "not-an-attempt"})
	bus.Publish(context.Background(), eventbus.Event{Type: eventbus.ExecutionAttemptCompleted})
	persistence.Stop()

	rows, _ := store.ListAttempts(context.Background(), 10)
	if len(rows) != 0 {
		t.Fatalf("foreign payloads persisted: %+v", rows)
	}
}
