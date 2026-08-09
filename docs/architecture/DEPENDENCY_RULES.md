# Dependency Rules

## Principles

These rules define compile-time and runtime dependency constraints that maintain
architectural stability. Violations must be approved via architecture review.

## Rule Table

| # | Rule | Enforcement | Rationale |
|---|------|-------------|-----------|
| D1 | `runtime` cannot import `router` | Compile-time | Runtime is a lower-level abstraction; routing depends on runtime, not vice versa |
| D2 | `learning` cannot import `runtime` | Compile-time (future) | Learning is a higher-level intelligence layer; it observes via interfaces, never mutates runtime state directly |
| D3 | `policy` cannot import `provider` directly | Compile-time | Policy operates on abstractions; provider access is through the `Provider` interface defined in `internal/provider/` |
| D4 | `explain` is read-only | Compile-time + code review | Explainability observes decisions; it never modifies routing state or provider state |
| D5 | `contracts.DecisionContext` is immutable after build | Compile-time (unexported fields) | Immutability guarantees thread-safety and predictable pipeline behavior |
| D6 | `contracts.DecisionTrace` is immutable after build | Compile-time (unexported fields) | Trace integrity requires append-only semantics |
| D7 | `plugin` package cannot import any `internal/*` package except `contracts` | Compile-time | Plugin SDK is the contract layer; it must be independently usable by external plugins |
| D8 | Core packages depend on interfaces, not implementations | Code review | Enables testing, mocking, and plugin substitution |
| D9 | `handler` cannot import `provider` directly | Compile-time | Handlers interact with routing, not providers; provider access is through `RouterOrchestrator` |
| D10 | `middleware` cannot import `router` | Compile-time | Middleware is pre-routing; it authenticates and rate-limits before routing decisions |
| D11 | `catalog` depends only on `provider.Registry` interface | Compile-time | Catalog aggregates model lists; it must not depend on routing or policy logic |
| D12 | `health` depends only on `provider.Provider` interface | Compile-time | Health probes are provider-agnostic; they call `HealthCheck()` and `ListModels()` |
| D13 | `usage` cannot import `router` | Compile-time | Usage tracking is observational; it receives events, not routing decisions |
| D14 | `eventbus` is a pure in-process utility with no internal dependencies | Compile-time | Event bus is the communication fabric; it must not depend on any business logic |
| D15 | `scheduler` cannot import `learning` | Compile-time (future) | Scheduler is a utility; learning jobs are registered via interfaces, not direct imports |
| D16 | `metrics` cannot import `router` or `provider` | Compile-time | Metrics collects counters/gauges; it receives typed events from the bus |
| D17 | `breaker` depends only on `provider.Name()` | Compile-time | Circuit breakers are keyed by provider name; they are provider-agnostic |
| D18 | `cache` cannot import `provider` | Compile-time | Cache stores responses by request hash; it is provider-agnostic |
| D19 | `ratelimit` cannot import `provider` or `router` | Compile-time | Rate limiting is a pre-routing gate; it operates on the gateway API key |
| D20 | `auth` cannot import any business logic package | Compile-time | Auth is the first middleware; it must be independent of all business logic |

## Dependency Graph

```
                    ┌──────────────┐
                    │   main.go    │
                    │  (cmd/)      │
                    └──────┬───────┘
                           │
           ┌───────────────┼───────────────┐
           ▼               ▼               ▼
    ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
    │   handler   │ │  middleware  │ │   catalog   │
    └──────┬──────┘ └──────┬──────┘ └──────┬──────┘
           │               │               │
           ▼               ▼               ▼
    ┌─────────────────────────────────────────────┐
    │              router package                  │
    │  (imports: policy, provider, runtime,       │
    │   contracts, eventbus, breaker, config)      │
    └──────────────────────────┬──────────────────┘
                               │
           ┌───────────────────┼───────────────────┐
           ▼                   ▼                   ▼
    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
    │   policy    │    │  provider   │    │   runtime   │
    └──────┬──────┘    └──────┬──────┘    └──────┬──────┘
           │                  │                  │
           │             ┌────┴─────┐            │
           │             ▼          ▼            │
           │    ┌──────────┐  ┌──────────┐       │
           │    │  plugin  │  │  health  │       │
           │    └──────────┘  └──────────┘       │
           │                  │                  │
           └──────────────────┼──────────────────┘
                              ▼
                    ┌─────────────────┐
                    │    contracts    │
                    │  (immutable     │
                    │   data types)   │
                    └─────────────────┘
                              ▲
           ┌──────────────────┼──────────────────┐
           │                  │                  │
           ▼                  ▼                  ▼
    ┌─────────────┐   ┌─────────────┐   ┌─────────────┐
    │   eventbus  │   │   metrics   │   │    usage    │
    └─────────────┘   └─────────────┘   └─────────────┘
           │                  │                  │
           ▼                  ▼                  ▼
    ┌───────────────────────────────────────────────────┐
    │              plugin SDK (internal/plugin)          │
    │  (NO internal imports — contract layer only)       │
    └───────────────────────────────────────────────────┘
```

## Package Dependency Matrix

| Package | Imports |
|---------|---------|
| `cmd/conductor` | `internal/config`, `internal/provider`, `internal/router`, `internal/handler`, `internal/health`, `internal/catalog`, `internal/scheduler`, `internal/auth`, `internal/eventbus`, `internal/usage`, `internal/metrics`, `internal/automode` |
| `internal/plugin` | **stdlib only** (no `internal/` imports) |
| `internal/contracts` | `github.com/google/uuid` |
| `internal/eventbus` | stdlib only |
| `internal/auth` | stdlib only, `internal/config` |
| `internal/middleware` | stdlib only, `internal/auth`, `internal/config` |
| `internal/handler` | `internal/router`, `internal/config`, `internal/middleware` |
| `internal/policy` | `internal/apitypes` |
| `internal/provider` | `internal/apitypes`, `internal/plugin` |
| `internal/router` | `internal/policy`, `internal/provider`, `internal/runtime`, `internal/contracts`, `internal/eventbus`, `internal/breaker`, `internal/config` |
| `internal/runtime` | `internal/provider` |
| `internal/health` | `internal/provider` |
| `internal/catalog` | `internal/provider`, `internal/config` |
| `internal/usage` | `internal/provider`, `internal/config` |
| `internal/metrics` | stdlib only |
| `internal/breaker` | stdlib only |
| `internal/cache` | stdlib only |
| `internal/ratelimit` | stdlib only |
| `internal/scheduler` | stdlib only, `internal/eventbus` |
| `internal/database` | `gorm.io/gorm`, `internal/contracts` |
| `internal/explain` | `internal/contracts` |
| `internal/automode` | `internal/provider`, `internal/config` |

## Forbidden Import Paths

The following import patterns are prohibited:

```go
// FORBIDDEN: runtime cannot import router
import "github.com/EffNine/conductor/internal/router"  // inside internal/runtime/

// FORBIDDEN: policy cannot import provider implementations
import "github.com/EffNine/conductor/internal/provider/openai"  // inside internal/policy/

// FORBIDDEN: handler cannot import provider directly
import "github.com/EffNine/conductor/internal/provider"  // inside internal/handler/

// FORBIDDEN: plugin cannot import internal packages
import "github.com/EffNine/conductor/internal/router"  // inside internal/plugin/

// FORBIDDEN: metrics cannot import router
import "github.com/EffNine/conductor/internal/router"  // inside internal/metrics/

// FORBIDDEN: usage cannot import router
import "github.com/EffNine/conductor/internal/router"  // inside internal/usage/
```

## Plugin Dependency Rules

Plugins follow a reverse-dependency pattern:

```
┌─────────────────────────────────────────────────────────┐
│              External Plugin Code                       │
│  (third-party or internal extension)                    │
│                                                         │
│  imports:                                               │
│    - internal/plugin          (SDK interfaces)          │
│    - internal/provider        (Provider interface)      │
│    - internal/router          (extension hooks)         │
│    - internal/contracts       (immutable types)         │
│    - internal/eventbus        (publishing events)       │
└─────────────────────────────────────────────────────────┘
                          ▲
                          │ implements
                          │
┌─────────────────────────────────────────────────────────┐
│              internal/plugin/                           │
│  (contract layer — no internal imports)                 │
│                                                         │
│  defines:                                               │
│    - Plugin interface + lifecycle                       │
│    - PluginMetadata                                     │
│    - PluginManager + PluginRegistry                     │
│    - PluginLoader interface                             │
│    - Category interfaces (Provider, Policy, etc.)       │
│    - Extension point interfaces                         │
└─────────────────────────────────────────────────────────┘
                          ▲
                          │ depends on (interfaces only)
                          │
┌─────────────────────────────────────────────────────────┐
│                 Core Packages                           │
│  (router, runtime, policy, provider, etc.)              │
│                                                         │
│  imports:                                               │
│    - internal/plugin      (for extension points)        │
│    - internal/contracts   (immutable types)             │
│    - internal/eventbus    (cross-subsystem events)       │
└─────────────────────────────────────────────────────────┘
```

## Validation

Run `go build ./...` to verify all dependency rules are satisfied at compile time.
The `internal/plugin` package must compile with zero `internal/` imports.

```bash
# Verify plugin package has no internal imports
grep -r '"github.com/EffNine/conductor/internal/' internal/plugin/
# Expected: no output

# Full build check
go build ./...
```
