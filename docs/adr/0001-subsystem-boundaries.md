# ADR-0001: Subsystem Boundaries

## Status
Accepted

## Context
Conductor's current architecture has tightly coupled packages with unclear boundaries. The router package mixes concerns like alias resolution, fallback handling, intelligent routing, and capability scoring. This makes the system difficult to extend and test.

## Decision
We will introduce five new subsystem packages with clean interfaces:

1. **internal/eventbus** - In-process pub/sub for cross-subsystem communication
2. **internal/runtime** - Provider runtime abstraction for future intelligence
3. **internal/policy** - Intent resolution and capability requirements
4. **internal/explain** - Decision rationale and explainability contracts
5. **internal/scheduler** - Job registration and execution framework

Each subsystem will:
- Define interfaces only (no implementations yet)
- Communicate via interfaces, not concrete types
- Avoid global mutable state
- Support future feature implementation

## Alternatives Considered
1. Keep monolithic router package - rejected due to tight coupling
2. Create monolith with internal packages - rejected due to lack of abstraction
3. Use external message broker - rejected due to unnecessary complexity

## Trade-offs
- **Pros**: Clean boundaries, testable interfaces, future-proof architecture
- **Cons**: More packages to maintain, slight learning curve for new contributors

## Future Implications
- Enables Learning Engine (Sprint V2.2)
- Supports Runtime Intelligence (Sprint V2.3)
- Facilitates Resource Manager (Sprint V2.4)
- Allows Enterprise features (Sprint V2.5)
