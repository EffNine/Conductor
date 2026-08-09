# Conductor Architecture Constitution V1

## Preamble

This Constitution establishes the immutable architectural principles of Conductor,
an OpenAI-compatible AI gateway. These principles govern all future development
and cannot be overridden by feature requirements without a formal constitution
amendment process.

**Version**: 1.0
**Effective Date**: 2026-08-08
**Status**: Frozen — Core architecture baseline

---

## Article I: Core Stability

### Section 1.1 — No Routing Changes
The routing engine (`internal/router/`) is frozen. No changes to route resolution,
fallback chains, alias handling, or provider selection algorithms are permitted
outside this sprint. Future routing intelligence is implemented through plugins
via extension points, not by modifying core routing code.

### Section 1.2 — No Policy Logic Implementation
The `internal/policy/` package contains interfaces only. No policy enforcement
logic is implemented in this sprint. Intent resolution, capability matching, and
request policies are placeholder interfaces for future plugin implementations.

### Section 1.3 — No Learning
Learning subsystems (adaptive routing, performance modeling, self-optimization)
are out of scope. The `internal/scheduler/` package reserves job types for future
learning tasks but implements none.

### Section 1.4 — No Dashboard
A web dashboard is out of scope. The gateway exposes a JSON API (`/api/*`) for
programmatic access. Any UI layer is a future consideration.

### Section 1.5 — No Trace Persistence
`DecisionTrace` objects are immutable and serializable but are NOT persisted to
database in this sprint. The `TraceStore` interface exists in `router/` for
future implementation.

### Section 1.6 — No Enterprise Features
Multi-tenancy, RBAC, audit logging, and key vault encryption are out of scope.

---

## Article II: Dependency Hierarchy

### Section 2.1 — Plugin SDK is the Foundation
The `internal/plugin/` package is the lowest-level internal package. It imports
**only** the Go standard library. No other internal package may impose dependencies
on it.

### Section 2.2 — Core Depends on Interfaces
All core packages (`router`, `runtime`, `policy`, `handler`) depend on interfaces
defined in `internal/plugin/` and `internal/contracts/`, never on concrete
implementations from sibling packages.

### Section 2.3 — Plugins Depend on Core
Plugin implementations may import core packages to access interfaces they extend.
Core packages must never import plugin implementations.

### Section 2.4 — Forbidden Dependencies
See `DEPENDENCY_RULES.md` for the complete list of 20 compile-time dependency rules.
Key prohibitions:
- Runtime cannot import Router
- Policy cannot import Provider implementations directly
- Handler cannot import Provider directly
- Metrics cannot import Router or Provider
- Usage cannot import Router

---

## Article III: Immutability

### Section 3.1 — Contract Immutability
All types in `internal/contracts/` are immutable after construction:
- `DecisionContext` — built via `NewDecisionContextBuilder()`, read-only after `Build()`
- `DecisionTrace` — built via `NewDecisionTraceBuilder()`, read-only after `Build()`
- `DecisionResult` — built via `NewDecisionResultBuilder()`, read-only after `Build()`
- `ExecutionResult` — built via `NewExecutionResultBuilder()`, read-only after `Build()`
- `ProviderSnapshot` — built via `NewProviderSnapshotBuilder()`, read-only after `Build()`
- `Candidate` — built via `NewCandidateBuilder()`, read-only after `Build()`

### Section 3.2 — Pipeline Mutability
The `router.DecisionContext` is intentionally mutable within a single request's
pipeline execution. It carries state between pipeline stages. Once the pipeline
completes, the resulting `contracts.DecisionTrace` and `contracts.DecisionResult`
are immutable snapshots.

### Section 3.3 — Clone Semantics
All contract types provide a `Clone()` method that returns a deep copy. Clones
are safe to share across goroutines.

---

## Article IV: Plugin Architecture

### Section 4.1 — Plugin Lifecycle
Every plugin implements the `Plugin` interface with four lifecycle methods:
```go
type Plugin interface {
    Init(ctx context.Context, cfg PluginConfig) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Health() PluginHealth
    Version() string
    Metadata() PluginMetadata
}
```

### Section 4.2 — Plugin Categories
Six plugin categories are defined as interfaces:
1. **ProviderPlugin** — adds new upstream AI providers
2. **PolicyPlugin** — implements intent/capability/policy logic
3. **LearningPlugin** — implements adaptive routing and performance modeling
4. **SchedulerPlugin** — adds background jobs
5. **DashboardPlugin** — adds API endpoints and dashboard features
6. **ToolPlugin** — adds request/response transformation tools

### Section 4.3 — Extension Points
Eight extension hooks allow plugins to intercept the routing pipeline:
```
0. BeforePipeline
1. AfterIntent
2. AfterCapability
3. AfterCandidateGeneration
4. AfterSelection
5. BeforeExecution
6. AfterExecution
7. AfterDecision
```

### Section 4.4 — Plugin Registration
Plugins are registered through `PluginManager` at startup. The core gateway
is unaware of specific plugin types — it only knows about the `Plugin` interface.

---

## Article V: Provider Abstraction

### Section 5.1 — Single Interface
All providers implement the `Provider` interface defined in `internal/provider/`.
No provider-specific types leak into core routing or handler code.

### Section 5.2 — Provider Registry
The `Registry` is the single point of provider lookup. Routing, health, catalog,
and usage all access providers through the registry, not through direct imports.

### Section 5.3 — OpenAI Compatibility
All providers expose an OpenAI-compatible interface. Provider-specific quirks
are handled in provider adapter code, never in the router.

---

## Article VI: Event-Driven Communication

### Section 6.1 — Event Bus
Cross-subsystem communication happens exclusively through `internal/eventbus/`.
Direct method calls between unrelated subsystems are discouraged.

### Section 6.2 — Event Types
All events are typed constants (`EventType`). Subscribers filter by event type.
Payloads are `any` to allow flexible event content.

### Section 6.3 — Event Propagation
Events carry a `context.Context` for cancellation and timeout propagation.
Publish is asynchronous by default; `PublishSync` exists for ordered execution.

---

## Article VII: Data Persistence

### Section 7.1 — SQLite
SQLite is the sole persistence backend. GORM provides the ORM layer with
auto-migration on startup.

### Section 7.2 — Persisted Data
Currently persisted:
- Usage records (tokens, cost, latency)
- Model health status (probe results)

Future persistence (out of scope for V2.2-D):
- Decision traces
- Learning data
- Audit logs

### Section 7.3 — No Schema Changes
No new database tables or columns are added in this sprint.

---

## Article VIII: Configuration

### Section 8.1 — Viper-Based
Configuration uses Viper with three search paths: `.`, `./config`, `/etc/conductor`.

### Section 8.2 — Environment Overrides
`CONDUCTOR_*` environment variables override config file values.
Legacy `NOVEXA_*` prefix is accepted as an alias.

### Section 8.3 — Provider Auto-Enable
Setting a provider's API key environment variable (e.g., `OPENAI_API_KEY`)
auto-enables that provider without requiring config file changes.

### Section 8.4 — Config File is Optional
Conductor starts with sensible defaults. `config.yaml` is only needed for
customization.

---

## Article IX: Amendment Process

### Section 9.1 — Amendment Proposal
Any change to this constitution requires a written proposal documenting:
1. The article and section being amended
2. The rationale for the change
3. The impact on existing functionality
4. Migration plan for existing deployments

### Section 9.2 — Amendment Approval
Constitution amendments require:
1. Review by the architecture maintainers
2. A compatibility period of at least one sprint
3. Documentation in `docs/architecture/CONSTITUTION_V1.md` with version bump

### Section 9.3 — Emergency Exceptions
In case of critical security vulnerabilities, amendments may be applied
immediately with retrospective documentation.

---

## Article X: Sprint Scope — V2.2-D

### Section 10.1 — In Scope
- Architecture documentation (`docs/architecture/`)
- Plugin SDK implementation (`internal/plugin/`)
- Plugin category interfaces
- Extension point definitions
- Dependency rule documentation
- Package boundary documentation
- All tests passing

### Section 10.2 — Out of Scope
- Policy logic implementation
- Learning system implementation
- Dashboard implementation
- Trace persistence implementation
- Enterprise features
- Key Vault implementation
- Any routing engine changes
- Any API endpoint changes
- Any runtime behavior changes

### Section 10.3 — Validation Criteria
1. `go build ./...` succeeds
2. `go test ./...` passes with 100% pass rate
3. `internal/plugin/` has zero imports from `internal/`
4. No new dependencies added to `go.mod`
5. No changes to existing package interfaces
6. No changes to HTTP API endpoints
7. No changes to routing behavior

---

## Appendix A: File Inventory

### Documentation
```
docs/architecture/
  CORE_ARCHITECTURE.md       — System overview and component diagram
  DEPENDENCY_RULES.md        — 20 compile-time dependency rules
  PACKAGE_BOUNDARIES.md      — Per-package responsibility definitions
  CONSTITUTION_V1.md         — This document
```

### Plugin SDK
```
internal/plugin/
  plugin.go            — Plugin interface and lifecycle
  metadata.go          — PluginMetadata definition
  manager.go           — PluginManager implementation
  registry.go          — PluginRegistry implementation
  loader.go            — PluginLoader interface
  categories.go        — Six plugin category interfaces
  extension_points.go  — Eight extension point interfaces
```

---

## Appendix B: Future Sprint Preview

| Sprint | Focus | Key Changes |
|--------|-------|-------------|
| V2.3-A | Policy Engine | Implement `PolicyPlugin` interface, add intent/capability logic |
| V2.3-B | Learning Foundation | Implement `LearningPlugin` interface, add adaptive scoring |
| V2.3-C | Trace Persistence | Implement `TraceStore`, persist `DecisionTrace` to SQLite |
| V2.4-A | Dashboard API | Implement `DashboardPlugin` interface, add web UI endpoints |
| V2.4-B | Enterprise | Multi-tenant auth, RBAC, audit logging |

---

*Conductor Architecture Constitution V1 — Frozen for V2.2-D*
*All architectural decisions must reference this document.*
