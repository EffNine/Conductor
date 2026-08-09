package eventbus

import (
	"context"
	"testing"
)

// testContextKey is a dedicated key type so context values don't collide with
// stdlib or other packages using the built-in string type.
type testContextKey string

func TestEventBusSubscribeUnsubscribe(t *testing.T) {
	bus := NewEventBus()
	var received []Event

	id := bus.Subscribe(ProviderRegistered, func(e Event) {
		received = append(received, e)
	})

	bus.PublishSync(context.Background(), Event{
		Type:    ProviderRegistered,
		Payload: "test-provider",
	})

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}

	bus.Unsubscribe(ProviderRegistered, id)

	bus.PublishSync(context.Background(), Event{
		Type:    ProviderRegistered,
		Payload: "test-provider-2",
	})

	if len(received) != 1 {
		t.Fatalf("expected 1 event after unsubscribe, got %d", len(received))
	}
}

func TestEventBusContextPropagation(t *testing.T) {
	bus := NewEventBus()
	var ctxReceived context.Context

	id := bus.Subscribe(ModelStatusChanged, func(e Event) {
		ctxReceived = e.Context
	})
	defer bus.Unsubscribe(ModelStatusChanged, id)

	parentCtx := context.WithValue(context.Background(), testContextKey("key"), "value")
	bus.PublishSync(parentCtx, Event{
		Type:    ModelStatusChanged,
		Payload: "test",
	})

	if ctxReceived != parentCtx {
		t.Fatal("context was not propagated")
	}
}

func TestEventBusMultipleSubscribers(t *testing.T) {
	bus := NewEventBus()
	var count1, count2 int

	bus.Subscribe(UsageRecorded, func(e Event) { count1++ })
	bus.Subscribe(UsageRecorded, func(e Event) { count2++ })

	bus.PublishSync(context.Background(), Event{
		Type: UsageRecorded,
	})

	if count1 != 1 || count2 != 1 {
		t.Fatalf("expected both subscribers to be called, got count1=%d, count2=%d", count1, count2)
	}
}
