# ADR-0004: Event Bus

## Status
Accepted

## Context
Subsystems currently communicate through direct method calls and shared state. This creates tight coupling and makes testing difficult. We need a decoupled communication mechanism.

## Decision
Introduce lightweight in-process pub/sub event bus with:
- `EventBus` struct with subscribe/unsubscribe/publish
- Typed events using `EventType` constants
- Context propagation for cancellation and values
- Both async (goroutine) and sync (direct) publish modes
- Thread-safe subscriber management

Event types include:
- Provider events (register, deregister, health change)
- Model events (status change, probe)
- Routing events (decision, config change)
- Health events (probe start/completion)
- Usage events (record)
- System events (shutdown, config reload)

## Alternatives Considered
1. Use external message broker (Kafka, Redis) - rejected due to operational complexity
2. Use callback interfaces - rejected due to coupling
3. Use channels directly - rejected due to lack of typing and management

## Trade-offs
- **Pros**: Decoupled subsystems, easy testing, context support, no external dependencies
- **Cons**: In-process only (no distributed events), requires event type management

## Future Implications
- Supports Learning Engine event processing
- Enables cross-subsystem coordination
- Facilitates audit logging
- Allows real-time monitoring
