# Package Boundaries

## Overview

This document defines the responsibility boundary of each package in the Conductor
codebase. Each package owns its interfaces, implementations, and tests. Cross-package
communication happens through interfaces, not direct type coupling.

## Package Inventory

### `cmd/conductor` — Application Bootstrap

**Responsibility**: Wire all subsystems together and start the HTTP server.

**Owns**:
- Application initialization sequence
- Provider registration from config
- Fiber app setup and route registration
- Graceful shutdown handling

**Imports**: All `internal/*` packages (this is the only package allowed to import everything)

**Imported by**: Nobody (entry point)

---

### `internal/apitypes` — Request/Response Types

**Responsibility**: OpenAI-compatible API request and response type definitions.

**Owns**:
- `ChatCompletionRequest`, `ChatCompletionResponse`
- `StreamChunk`, `Message`, `Tool`, `ToolCall`
- `EmbeddingRequest`, `EmbeddingResponse`
- All API-facing data structures

**Imports**: stdlib only

**Imported by**: `provider`, `router`, `handler`, `policy`, `usage`

---

### `internal/auth` — API Key Authentication

**Responsibility**: Gateway API key generation and validation.

**Owns**:
- `Service` with `Validate(key string) bool`
- Key generation utilities

**Imports**: stdlib, `internal/config`

**Imported by**: `middleware`, `cmd/conductor`

---

### `internal/middleware` — HTTP Middleware

**Responsibility**: Pre-routing HTTP middleware (auth, rate limiting, correlation).

**Owns**:
- API key authentication middleware
- Rate limiting middleware
- Correlation ID middleware
- CORS configuration

**Imports**: stdlib, `internal/auth`, `internal/config`, `internal/ratelimit`

**Imported by**: `handler`, `cmd/conductor`

---

### `internal/ratelimit` — Rate Limiting

**Responsibility**: Token-based and request-based rate limiting.

**Owns**:
- Token bucket implementation
- Per-provider and global rate limiters

**Imports**: stdlib only

**Imported by**: `middleware`, `cmd/conductor`

---

### `internal/provider` — Provider Abstraction

**Responsibility**: Provider interface, registry, and plugin contract.

**Owns**:
- `Provider` interface (9 methods)
- `Registry` (thread-safe provider map)
- `Metadata`, `Capabilities`, `PricingInfo`
- `Plugin` interface (legacy, see `internal/plugin/` for full SDK)
- `PluginConfig`, `PluginFactory`
- Provider-specific implementations (`openai`, `anthropic`, `gemini`, etc.)

**Imports**: `internal/apitypes`, `internal/plugin`

**Imported by**: `router`, `runtime`, `health`, `catalog`, `usage`, `automode`, `cmd/conductor`

---

### `internal/plugin` — Plugin SDK (NEW)

**Responsibility**: Extensibility contract layer. Defines interfaces that both core
and external plugins depend on.

**Owns**:
- `Plugin` interface with full lifecycle (`Init`, `Start`, `Stop`, `Health`, `Version`)
- `PluginMetadata` — plugin identity and capabilities
- `PluginManager` — lifecycle orchestration
- `PluginRegistry` — plugin discovery and lookup
- `PluginLoader` interface — dynamic loading contract
- Category interfaces: `ProviderPlugin`, `PolicyPlugin`, `LearningPlugin`,
  `SchedulerPlugin`, `DashboardPlugin`, `ToolPlugin`
- Extension point interfaces: `BeforePipeline`, `AfterIntent`, `AfterCapability`,
  `AfterCandidateGeneration`, `AfterSelection`, `BeforeExecution`, `AfterExecution`,
  `AfterDecision`

**Imports**: **stdlib only** (zero `internal/` imports)

**Imported by**: `provider`, `router`, `runtime`, all plugin implementations

---

### `internal/router` — Routing Engine

**Responsibility**: Request routing, pipeline execution, and provider selection.

**Owns**:
- `RouterEngine` — main routing orchestrator
- `PipelineStage` interface and concrete stages
- `DecisionContext` — mutable pipeline context
- `IntentResolver`, `CapabilityResolver` interfaces
- `RoutingEngine`, `ExecutionEngine`, `RouterOrchestrator` interfaces
- `SelectionResult`, `ResolvedRoute`
- Trace store interface

**Imports**: `internal/policy`, `internal/provider`, `internal/runtime`,
  `internal/contracts`, `internal/eventbus`, `internal/breaker`, `internal/config`,
  `internal/plugin`

**Imported by**: `handler`, `cmd/conductor`

**Boundary rule**: Router is the ONLY package that directly calls provider methods.
All other packages interact with providers through router interfaces.

---

### `internal/policy` — Policy Interfaces

**Responsibility**: Intent resolution and capability requirement interfaces.

**Owns**:
- `Intent`, `TaskType` — request classification
- `CapabilityRequirement` — what a request needs
- `IntentResolver`, `CapabilityResolver` interfaces
- `Policy` interface — request policy contract
- `PolicyResult`, `RequestModifications`

**Imports**: `internal/apitypes`

**Imported by**: `router`, `cmd/conductor`

**Boundary rule**: Policy defines interfaces but contains no routing logic.
The router's `IntentStage` and `CapabilityStage` are placeholder implementations.

---

### `internal/runtime` — Provider Runtime

**Responsibility**: Provider lifecycle state tracking and adapters.

**Owns**:
- `ProviderRuntime` interface (state, latency, errors, health)
- `RuntimeFactory` interface
- `Manager` interface and implementation
- `ProviderState`, `StateChange`, `ProviderStateSnapshot`
- `GlobalRuntimeState`, `RuntimeSnapshot`
- Adapter subpackage: bridges health/breaker/usage to runtime
- Snapshot subpackage: immutable runtime snapshots

**Imports**: `internal/provider`

**Imported by**: `router`, `health`, `cmd/conductor`

**Boundary rule**: Runtime never imports router. It observes provider state;
it does not make routing decisions.

---

### `internal/contracts` — Immutable Decision Contracts

**Responsibility**: Schema-versioned immutable data types for routing decisions.

**Owns**:
- `DecisionContext` — immutable request context (builder pattern)
- `DecisionTrace` — immutable execution timeline
- `DecisionResult` — immutable routing outcome
- `ExecutionResult` — immutable execution outcome
- `ProviderSnapshot` — immutable provider state
- `Candidate` — immutable scoring candidate
- ID types: `DecisionID`, `TraceID`, `SnapshotID`, `ExecutionID`, `CandidateID`, `ProviderID`
- `SchemaMetadata` — version tracking

**Imports**: `github.com/google/uuid`

**Imported by**: `router`, `runtime`, `explain`, `database`, `eventbus`

**Boundary rule**: Contracts are immutable. They use builder patterns for construction
and return deep clones for safe sharing across goroutines.

---

### `internal/eventbus` — Event Bus

**Responsibility**: In-process publish/subscribe communication.

**Owns**:
- `EventType` constants (provider lifecycle, routing, health, system)
- `Event` struct
- `Subscriber` callback type
- `EventBus` with typed subscribe/unsubscribe/publish

**Imports**: stdlib only

**Imported by**: `router`, `runtime`, `health`, `scheduler`, `cmd/conductor`

**Boundary rule**: Event bus is a pure utility. It has no knowledge of business
logic types — events carry `any` payloads.

---

### `internal/health` — Model Health Probing

**Responsibility**: Per-model reachability probing with exponential backoff.

**Owns**:
- `Monitor` — background probing loop
- `Prober` — individual model probe execution
- `ModelStatusStore` — SQLite persistence of probe results
- `ModelProber` — live probe execution

**Imports**: `internal/provider`, `internal/database`, `internal/eventbus`, `internal/config`

**Imported by**: `cmd/conductor`

**Boundary rule**: Health probes only call `Provider.HealthCheck()` and
`Provider.ChatCompletion()` with minimal payloads. It never touches routing logic.

---

### `internal/catalog` — Model Catalog

**Responsibility**: Merging provider model lists for `/v1/models`.

**Owns**:
- `Catalog` — merged model listing
- Curated-only filtering logic
- Reachability-based model exclusion

**Imports**: `internal/provider`, `internal/config`

**Imported by**: `handler`, `cmd/conductor`

**Boundary rule**: Catalog is read-only from the handler's perspective. It does
not depend on routing, policy, or runtime state.

---

### `internal/usage` — Usage Tracking

**Responsibility**: Token counting and cost estimation.

**Owns**:
- `Tracker` — usage recording
- `CostEstimator` — cost calculation
- Usage record models

**Imports**: `internal/provider`, `internal/config`, `internal/database`

**Imported by**: `cmd/conductor`

**Boundary rule**: Usage receives events from the router or is called after
execution. It never influences routing decisions.

---

### `internal/metrics` — Prometheus Metrics

**Responsibility**: Observability metrics collection.

**Owns**:
- Counter, gauge, and histogram collectors
- Provider-level and gateway-level metrics
- HTTP request metrics

**Imports**: stdlib only

**Imported by**: `cmd/conductor`

**Boundary rule**: Metrics receives typed callbacks; it does not import router
or provider packages. Events flow through the event bus.

---

### `internal/breaker` — Circuit Breaker

**Responsibility**: Per-provider circuit breaking.

**Owns**:
- `BreakerPool` — keyed by provider name
- Circuit state machine (closed, open, half-open)
- Failure counting and recovery

**Imports**: stdlib only

**Imported by**: `router`, `runtime/adapter`

**Boundary rule**: Breaker is provider-name keyed. It has no knowledge of
routing logic or provider implementations.

---

### `internal/cache` — Response Cache

**Responsibility**: LRU response caching.

**Owns**:
- LRU cache engine
- Cache key generation
- Cache eviction policies

**Imports**: stdlib only

**Imported by**: `cmd/conductor` (optional)

**Boundary rule**: Cache is provider-agnostic. It stores serialized responses
by request hash.

---

### `internal/scheduler` — Job Scheduler

**Responsibility**: Background job registry and execution.

**Owns**:
- `JobRegistry` — scheduled job management
- Job types: probe, cleanup, learning (future)

**Imports**: stdlib, `internal/eventbus`

**Imported by**: `cmd/conductor`

**Boundary rule**: Scheduler registers jobs via interfaces. It does not import
learning or policy packages directly.

---

### `internal/database` — Database Layer

**Responsibility**: SQLite persistence via GORM.

**Owns**:
- Database connection and migration
- Model definitions: `UsageRecord`, `ModelStatusRecord`, etc.
- Repository patterns for data access

**Imports**: `gorm.io/gorm`, `internal/contracts`

**Imported by**: `health`, `usage`, `cmd/conductor`

---

### `internal/explain` — Explainability

**Responsibility**: Structured decision rationale types.

**Owns**:
- `DecisionRationale` — why a provider was selected
- `ExplainableDecision` interface

**Imports**: `internal/contracts`

**Imported by**: `handler` (future dashboard API)

**Boundary rule**: Explain is read-only. It formats decision data for display;
it never modifies routing or provider state.

---

### `internal/automode` — Auto Mode (NVIDIA NIM)

**Responsibility**: Runtime automatic model selection for NVIDIA NIM.

**Owns**:
- `AutoSelector` interface
- Scoring by reachability, cost, and latency
- Task profiles and lookback windows

**Imports**: `internal/provider`, `internal/config`

**Imported by**: `router`, `cmd/conductor`

---

### `internal/handler` — HTTP Handlers

**Responsibility**: Fiber HTTP route handlers.

**Owns**:
- OpenAI-compatible API handlers
- Dashboard API handlers
- Health endpoint

**Imports**: `internal/router`, `internal/config`, `internal/middleware`,
  `internal/catalog`, `internal/apitypes`

**Imported by**: `cmd/conductor`

**Boundary rule**: Handler calls router, never provider directly. It is the
single point of entry for all HTTP requests.

---

## Package Boundary Enforcement

### Compile-Time Checks

```bash
# Verify plugin package has no internal imports
! grep -r '"github.com/EffNine/conductor/internal/' internal/plugin/

# Verify runtime does not import router
! grep -r '"github.com/EffNine/conductor/internal/router"' internal/runtime/

# Verify policy does not import provider implementations
! grep -r 'provider/openai\|provider/anthropic' internal/policy/

# Full build
go build ./...
```

### Runtime Checks

The plugin SDK enforces isolation at runtime:
- Plugins register via `PluginManager` — core never calls plugin constructors directly
- Extension points are invoked through the `PluginRegistry` — core code is unaware
  of specific plugin implementations
- Plugin lifecycle (`Init` → `Start` → `Stop`) is managed by `PluginManager`
