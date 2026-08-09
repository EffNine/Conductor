# Sprint V2.1-A: Architecture Refactor Foundation - Summary

## Executive Summary

This sprint established the architectural foundation for Conductor's long-term scalability without changing any external behavior. All existing tests pass, and no routing, API, or configuration changes were made.

## Architecture Tree

```
conductor/
├── cmd/conductor/main.go              # Application entry point
├── internal/
│   ├── auth/                          # Authentication service
│   ├── automode/                      # Auto model selection
│   ├── breaker/                       # Circuit breaker
│   ├── cache/                         # Response caching
│   ├── catalog/                       # Model catalog
│   ├── config/                        # Configuration management
│   ├── database/                      # SQLite persistence
│   ├── eventbus/                      # ✨ NEW: Event bus (pub/sub)
│   ├── explain/                       # ✨ NEW: Explainability contracts
│   ├── handler/                       # HTTP handlers
│   ├── health/                        # Health monitoring
│   ├── metrics/                       # Metrics collection
│   ├── middleware/                    # HTTP middleware
│   ├── policy/                        # ✨ NEW: Policy interfaces
│   ├── provider/                      # Provider implementations
│   ├── router/                        # Routing engine (interfaces added)
│   ├── runtime/                       # ✨ NEW: Runtime abstraction
│   ├── scheduler/                     # ✨ NEW: Job scheduler
│   └── usage/                         # Usage tracking
├── pkg/                               # Public packages
├── docs/
│   └── adr/                           # ✨ NEW: Architecture Decision Records
└── deployments/                       # Deployment configs
```

## New Package Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                         main.go                                 │
│  (wires all subsystems together)                                │
└─────────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┼───────────────┐
              │               │               │
              ▼               ▼               ▼
    ┌───────────────┐ ┌───────────────┐ ┌───────────────┐
    │   eventbus    │ │   scheduler   │ │    runtime    │
    │   (pub/sub)   │ │  (job mgmt)   │ │ (provider     │
    │               │ │               │ │  lifecycle)   │
    └───────┬───────┘ └───────┬───────┘ └───────┬───────┘
            │                 │                 │
            ▼                 ▼                 ▼
    ┌───────────────┐ ┌───────────────┐ ┌───────────────┐
    │    policy     │ │    explain    │ │   router      │
    │  (intent &    │ │(decision      │ │  (interfaces  │
    │   capability) │ │  rationale)   │ │   added)      │
    └───────────────┘ └───────────────┘ └───────────────┘
```

## Dependency Graph

```
                    ┌──────────────┐
                    │   main.go    │
                    └──────┬───────┘
                           │
         ┌─────────────────┼─────────────────┐
         │                 │                 │
         ▼                 ▼                 ▼
    ┌─────────┐       ┌─────────┐       ┌─────────┐
    │ eventbus│       │scheduler│       │ provider│
    └────┬────┘       └────┬────┘       └────┬────┘
         │                 │                 │
         ▼                 ▼                 ▼
    ┌─────────┐       ┌─────────┐       ┌─────────┐
    │ policy  │       │ explain │       │ router  │
    └─────────┘       └─────────┘       └────┬────┘
                                             │
                                             ▼
                                      ┌─────────────┐
                                      │   handler   │
                                      └─────────────┘
```

## ADR Summary

### ADR-0001: Subsystem Boundaries
- **Decision**: Created 5 new subsystem packages with clean interfaces
- **Packages**: eventbus, runtime, policy, explain, scheduler
- **Principle**: Interfaces only, no implementations, no global state

### ADR-0002: Router Responsibilities Split
- **Decision**: Split router into 4 interfaces
- **Interfaces**: IntentResolver, CapabilityResolver, RoutingEngine, ExecutionEngine
- **Compatibility**: Existing Engine implements RouterOrchestrator

### ADR-0003: Runtime Subsystem
- **Decision**: Abstract provider lifecycle and state
- **Types**: ProviderRuntime, ProviderState, RuntimeSnapshot, StateChange
- **Future**: Supports intelligence and resource management

### ADR-0004: Event Bus
- **Decision**: Lightweight in-process pub/sub
- **Features**: Typed events, context propagation, thread-safe
- **No external deps**: Pure Go implementation

### ADR-0005: Explainability
- **Decision**: Contracts for decision justification
- **Types**: DecisionRationale, CandidateRationale, SignalEntry, PenaltyEntry
- **Reasons**: Health, latency, cost, capability, policy, fallback, random, config

## Public Interfaces

### Event Bus (`internal/eventbus`)
```go
type EventBus struct { ... }
func NewEventBus() *EventBus
func (eb *EventBus) Subscribe(eventType EventType, sub Subscriber) uint64
func (eb *EventBus) Unsubscribe(eventType EventType, id uint64)
func (eb *EventBus) Publish(ctx context.Context, event Event)
func (eb *EventBus) PublishSync(ctx context.Context, event Event)

type EventType string
const (
    ProviderRegistered EventType = "provider.registered"
    ProviderDeregistered EventType = "provider.deregistered"
    ModelStatusChanged EventType = "model.status.changed"
    RoutingDecision EventType = "routing.decision"
    // ... more
)
```

### Runtime (`internal/runtime`)
```go
type ProviderRuntime interface {
    Name() string
    State() ProviderState
    Snapshot(ctx context.Context) ProviderStateSnapshot
    RecordLatency(latencyMs int64)
    RecordError(err error)
    RecordSuccess()
    IsHealthy() bool
    UpdateState(newState ProviderState, reason string, metadata map[string]any)
    GetStateChanges() []StateChange
}

type ProviderState string
const (
    StateUnknown ProviderState = "unknown"
    StateHealthy ProviderState = "healthy"
    StateDegraded ProviderState = "degraded"
    StateUnhealthy ProviderState = "unhealthy"
    StateRecovering ProviderState = "recovering"
    StateScalingUp ProviderState = "scaling_up"
    StateScalingDown ProviderState = "scaling_down"
)
```

### Policy (`internal/policy`)
```go
type IntentResolver interface {
    Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*Intent, error)
}

type CapabilityResolver interface {
    Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*CapabilityRequirement, error)
    CheckProviderCapabilities(ctx context.Context, req *CapabilityRequirement, providerName string) bool
}

type Policy interface {
    Name() string
    Execute(ctx context.Context, req *apitypes.ChatCompletionRequest) (*PolicyResult, error)
}
```

### Explain (`internal/explain`)
```go
type DecisionRationale struct {
    RequestID string
    SelectedProvider string
    DecisionReason Reason
    Confidence float64
    Candidates []CandidateRationale
    Signals []SignalEntry
    Penalties []PenaltyEntry
}

type Reason string
const (
    ReasonHealth Reason = "health"
    ReasonLatency Reason = "latency"
    ReasonCost Reason = "cost"
    ReasonCapability Reason = "capability"
    ReasonPolicy Reason = "policy"
    ReasonFallback Reason = "fallback"
    ReasonRandom Reason = "random"
    ReasonConfig Reason = "config"
)
```

### Scheduler (`internal/scheduler`)
```go
type JobRegistry struct { ... }
func NewJobRegistry() *JobRegistry
func (r *JobRegistry) Register(job *Job) error
func (r *JobRegistry) Unregister(id string) error
func (r *JobRegistry) Get(id string) (*Job, error)
func (r *JobRegistry) List() []*Job
func (r *JobRegistry) Enable(id string) error
func (r *JobRegistry) Disable(id string) error

type JobType string
const (
    JobTypeHealthProbe JobType = "health_probe"
    JobTypeCheckpoint JobType = "checkpoint"
    JobTypeCleanup JobType = "cleanup"
    JobTypeLearning JobType = "learning"
    JobTypeForecast JobType = "forecast"
    JobTypeRotation JobType = "rotation"
)
```

### Router (`internal/router`)
```go
type IntentResolver interface {
    Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*policy.Intent, error)
}

type CapabilityResolver interface {
    Resolve(ctx context.Context, req *apitypes.ChatCompletionRequest) (*policy.CapabilityRequirement, error)
}

type RoutingEngine interface {
    Select(ctx context.Context, modelID string, req *apitypes.ChatCompletionRequest) (*SelectionResult, error)
    SelectWithFallback(ctx context.Context, modelID string, req *apitypes.ChatCompletionRequest) (*SelectionResult, []ResolvedRoute, error)
    GetProviderScores(capHint policy.CapabilityRequirement) []ProviderScoreView
    RecordResult(providerName string, latencyMs int64, success bool)
}

type ExecutionEngine interface {
    Execute(ctx context.Context, resolved ResolvedRoute, req *apitypes.ChatCompletionRequest) (*apitypes.ChatCompletionResponse, error)
    ExecuteStream(ctx context.Context, resolved ResolvedRoute, req *apitypes.ChatCompletionRequest) (<-chan apitypes.StreamChunk, error)
}

type RouterOrchestrator interface {
    Resolve(modelID string) (*ResolvedRoute, error)
    ResolveWithContext(ctx context.Context, modelID string, messages []apitypes.Message) (*ResolvedRoute, error)
    ResolveWithFallback(modelID string) (*ResolvedRoute, []ResolvedRoute, error)
    ResolveWithFallbackAndMessages(modelID string, messages []apitypes.Message) (*ResolvedRoute, []ResolvedRoute, error)
    ResolveWithFallbackAndContext(ctx context.Context, modelID string, messages []apitypes.Message) (*ResolvedRoute, []ResolvedRoute, error)
    SetAutoSelector(s AutoSelector)
    HasAutoSelector() bool
    BreakerPool() *BreakerPool
}
```

## What Changed

### New Files Created
1. `internal/eventbus/eventbus.go` - Event bus implementation
2. `internal/eventbus/eventbus_test.go` - Event bus tests
3. `internal/runtime/types.go` - Runtime interfaces and types
4. `internal/runtime/types_test.go` - Runtime tests
5. `internal/policy/types.go` - Policy interfaces
6. `internal/policy/types_test.go` - Policy tests
7. `internal/explain/types.go` - Explainability types
8. `internal/explain/types_test.go` - Explainability tests
9. `internal/scheduler/scheduler.go` - Scheduler implementation
10. `internal/scheduler/scheduler_test.go` - Scheduler tests
11. `internal/router/interfaces.go` - Router interfaces
12. `internal/router/interfaces_test.go` - Router interface tests
13. `docs/adr/0001-subsystem-boundaries.md` - ADR 1
14. `docs/adr/0002-router-responsibilities.md` - ADR 2
15. `docs/adr/0003-runtime-subsystem.md` - ADR 3
16. `docs/adr/0004-event-bus.md` - ADR 4
17. `docs/adr/0005-explainability.md` - ADR 5

### Modified Files
1. `cmd/conductor/main.go` - Added event bus and scheduler initialization

## What Intentionally Remains Unimplemented

### Learning Engine
- No learning algorithms
- No model selection optimization
- No historical pattern analysis

### Runtime Intelligence
- No adaptive scaling
- No capacity planning
- No predictive routing

### Resource Manager
- No resource allocation
- No load balancing algorithms
- No cost optimization

### Enterprise Features
- No multi-tenancy
- No RBAC
- No audit logging

### Policy Engine Logic
- Only interfaces defined
- No policy evaluation
- No policy enforcement

### Key Vault Logic
- No credential management
- No secret rotation
- No vault integration

### Forecasting
- No demand prediction
- No cost forecasting
- No capacity planning

## Risks

### Low Risk
1. **Interface drift** - Interfaces may need adjustment as implementations are added
2. **Package bloat** - New packages add maintenance overhead

### Medium Risk
1. **Learning curve** - Contributors need to understand new abstractions
2. **Integration complexity** - Wiring implementations to interfaces requires care

### Mitigation
- Comprehensive ADR documentation
- Interface tests ensure compile-time correctness
- All existing tests pass - no behavior changes

## Recommended Next Sprint (V2.2)

### Priority 1: Learning Engine Foundation
- Implement `IntentResolver` for task classification
- Add learning engine package
- Create historical pattern analysis

### Priority 2: Runtime Implementation
- Implement `ProviderRuntime` for key providers
- Add state management
- Create runtime store

### Priority 3: Explainability Generation
- Implement decision rationale generation
- Add signals and penalties tracking
- Create explainable routing

### Priority 4: Scheduler Jobs
- Implement health probe job
- Add checkpoint job
- Create cleanup job

## Validation Results

### Build Status
```
✓ go build ./... - SUCCESS
```

### Test Results
```
✓ internal/eventbus - PASS (3 tests)
✓ internal/runtime - PASS (5 tests)
✓ internal/policy - PASS (5 tests)
✓ internal/explain - PASS (6 tests)
✓ internal/scheduler - PASS (8 tests)
✓ internal/router - PASS (17 tests)
✓ All existing tests - PASS
```

### Behavior Verification
- No routing behavior changes
- No API changes
- No configuration changes
- No performance regression
- Backward compatible

## Conclusion

Sprint V2.1-A successfully established the architectural foundation for Conductor's long-term scalability. All goals were met:

1. ✓ Clean subsystem boundaries introduced
2. ✓ Router responsibilities split into interfaces
3. ✓ Runtime abstraction created
4. ✓ Event bus implemented
5. ✓ Explainability contracts defined
6. ✓ Scheduler abstraction created
7. ✓ Dependency injection cleanup (interfaces only)
8. ✓ ADR documentation complete
9. ✓ All tests pass, no behavior changes

The foundation is ready for future feature implementation in subsequent sprints.
