# ADR-0002: Router Responsibilities Split

## Status
Accepted

## Context
The current `router.Engine` and `router.RouterEngine` handle:
- Alias and route resolution
- Fallback chain execution
- Intelligent provider selection
- Capability matching
- Circuit breaker integration
- Metrics collection

This violates Single Responsibility Principle and makes testing difficult.

## Decision
Split router responsibilities into four interfaces:

1. **IntentResolver** - Determines what the request wants
2. **CapabilityResolver** - Determines what the request needs from providers
3. **RoutingEngine** - Selects the best provider based on scoring
4. **ExecutionEngine** - Executes the request against selected provider

The existing `Engine` and `RouterEngine` will implement these interfaces to maintain backward compatibility.

## Alternatives Considered
1. Create new router package - rejected due to breaking changes
2. Use strategy pattern - rejected as over-engineered for current needs
3. Keep monolithic router - rejected due to testing and maintenance issues

## Trade-offs
- **Pros**: Clear responsibilities, easier testing, future extensibility
- **Cons**: More interfaces to maintain, requires careful implementation

## Future Implications
- Enables policy-based routing
- Supports adaptive routing algorithms
- Allows A/B testing of routing strategies
- Facilitates explainable routing decisions
