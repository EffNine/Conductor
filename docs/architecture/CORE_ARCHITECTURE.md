# Core Architecture

## Overview

Conductor is a headless OpenAI-compatible AI gateway built as a single Go/Fiber binary.
It routes requests across upstream provider subscriptions, persists usage to SQLite, and
exposes a dashboard API. This document describes the frozen core architecture that all
future features must respect.

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Client Layer                                │
│  (VS Code, Claude Code, Open WebUI, custom apps, curl)             │
└──────────────────────────┬──────────────────────────────────────────┘
                           │ HTTPS
                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      Conductor Gateway                              │
│                                                                     │
│  ┌─────────────┐   ┌─────────────┐   ┌─────────────┐              │
│  │   Middleware │   │   Handler   │   │  Event Bus  │              │
│  │  (auth,     │──▶│   (Fiber)   │──▶│  (pub/sub)  │              │
│  │   rate-limit)│   │             │   │             │              │
│  └─────────────┘   └──────┬──────┘   └─────────────┘              │
│                           │                                        │
│  ┌────────────────────────▼──────────────────────────────────┐    │
│  │                     Routing Pipeline                       │    │
│  │                                                            │    │
│  │  ┌──────────┐  ┌──────────────┐  ┌────────────────────┐   │    │
│  │  │  Intent  │→ │   Capability │→ │   Candidate        │   │    │
│  │  │  Stage   │  │    Stage     │  │   Generation       │   │    │
│  │  └──────────┘  └──────────────┘  │   Stage            │   │    │
│  │                                 └──────────┬───────────┘   │    │
│  │                                    ┌───────▼───────────┐   │    │
│  │                                    │   Selection       │   │    │
│  │                                    │   Stage           │   │    │
│  │                                    └───────┬───────────┘   │    │
│  │                                       Execution            │    │
│  └─────────────────────────┬─────────────────────────────────┘    │
│                             │                                      │
│  ┌──────────────────────────▼─────────────────────────────────┐   │
│  │                     Provider Layer                          │   │
│  │                                                            │   │
│  │  ┌─────────────────────────────────────────────────────┐   │   │
│  │  │              Provider Registry                       │   │   │
│  │  │  (openai, anthropic, gemini, deepseek, groq,         │   │   │
│  │  │   ollama, lmstudio, opencode, nvidia_nim,             │   │   │
│  │  │   nousportal, openrouter, xai, agnesai, generic)      │   │   │
│  │  └─────────────────────────────────────────────────────┘   │   │
│  │                           │                                  │   │
│  │  ┌────────────────────────▼─────────────────────────────┐   │   │
│  │  │            Provider Runtime Manager                    │   │   │
│  │  │  (state tracking, health adapters, circuit breakers)   │   │   │
│  │  └─────────────────────────────────────────────────────┘   │   │
│  └─────────────────────────┬─────────────────────────────────┘   │
│                             │                                      │
│  ┌──────────────────────────▼─────────────────────────────────┐   │
│  │                   Plugin SDK Layer                           │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────────┐  │   │
│  │  │Provider  │  │  Policy  │  │ Learning │  │  Scheduler │  │   │
│  │  │  Plugin  │  │  Plugin  │  │  Plugin  │  │   Plugin   │  │   │
│  │  └──────────┘  └──────────┘  └──────────┘  └────────────┘  │   │
│  │  ┌──────────┐  ┌──────────┐                                  │   │
│  │  │Dashboard │  │   Tool   │                                  │   │
│  │  │  Plugin  │  │  Plugin  │                                  │   │
│  │  └──────────┘  └──────────┘                                  │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                    Supporting Subsystems                     │   │
│  │  Catalog  │  Health  │  Cache  │  RateLimit  │  Metrics     │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                      Persistence Layer                       │   │
│  │                    SQLite (GORM)                             │   │
│  │  (usage, model status, future: traces, learning data)        │   │
│  └─────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
                           │
                           ▼
                  ┌────────────────┐
                  │  Upstream      │
                  │  Providers     │
                  │  (OpenAI,      │
                  │   Anthropic,    │
                  │   NVIDIA NIM,   │
                  │   Ollama, ...)  │
                  └────────────────┘
```

## Request Lifecycle

1. Client sends `POST /v1/chat/completions` with `Authorization: Bearer <key>`
2. Auth middleware validates against gateway API key
3. Rate limiter checks global and per-provider limits
4. Request validator checks payload structure
5. Router resolves `model`:
   - Strip registered provider prefix if present
   - Resolve alias if exact match
   - Resolve configured route
   - Apply fallback chain if configured
6. Provider adapter sends the request
7. Response is returned in OpenAI-compatible format
8. Usage tracker records the request asynchronously

## Streaming Flow

Same as above through route resolution, then:
1. Provider adapter opens a streaming connection
2. Gateway reads SSE chunks from the provider
3. Each chunk is forwarded to the client
4. Final usage is recorded when the stream completes

## Core Subsystems

### Router (`internal/router/`)
The routing engine resolves model IDs to provider routes and executes requests.
- **Pipeline**: `IntentStage` → `CapabilityStage` → `CandidateStage` → `SelectionStage`
- **Interfaces**: `IntentResolver`, `CapabilityResolver`, `RoutingEngine`, `ExecutionEngine`, `RouterOrchestrator`
- **DecisionContext**: mutable context flowing through pipeline stages
- **Contracts**: immutable `DecisionContext`, `DecisionTrace`, `DecisionResult`, `ExecutionResult` (in `internal/contracts/`)

### Provider (`internal/provider/`)
Abstraction layer for upstream AI providers.
- **Interface**: `Provider` with 9 methods: `Name`, `ChatCompletion`, `ChatCompletionStream`, `Embeddings`, `ListModels`, `GetPricing`, `HealthCheck`, `SupportsModel`, `GetMetadata`
- **Registry**: thread-safe map of provider name → Provider
- **Plugin**: extensibility contract (see Plugin SDK)
- **OpenAI Base**: shared implementation for OpenAI-compatible providers

### Runtime (`internal/runtime/`)
Lifecycle and state tracking for provider runtimes.
- **ProviderRuntime**: interface for state, latency, error, and health tracking
- **Manager**: registers, queries, and watches runtimes
- **Snapshot**: immutable point-in-time views of runtime state

### Policy (`internal/policy/`)
Intent resolution and capability matching interfaces.
- **IntentResolver**: classifies request task type
- **CapabilityResolver**: determines what a request needs from providers
- **Policy**: request policy contract (allowed, modifications, reason)

### Contracts (`internal/contracts/`)
Immutable, schema-versioned data structures for routing decisions.
- **DecisionContext**: immutable request context with builder
- **DecisionTrace**: immutable execution timeline
- **DecisionResult**: immutable routing outcome
- **ExecutionResult**: immutable execution outcome
- **ProviderSnapshot**: immutable provider state snapshot
- **Candidate**: immutable scoring candidate

### Event Bus (`internal/eventbus/`)
In-process publish/subscribe for cross-subsystem communication.
- Typed events with context propagation
- Thread-safe subscribe/unsubscribe
- Async and sync publish modes

### Health (`internal/health/`)
Per-model reachability probing with exponential backoff.
- Background prober sends minimal chat completions
- Confirmed failures drop models from `/v1/models`
- Status persisted to SQLite across restarts

### Catalog (`internal/catalog/`)
Model catalog merger for `/v1/models` endpoint.
- Merges provider model lists
- Qualifies duplicates with provider prefixes
- Supports curated-only mode with static allowlists
- Applies reachability filtering

### Usage (`internal/usage/`)
Usage tracking and cost estimation.
- Primary counters: OpenAI-style tokens
- Extra counters: requests, duration_ms, input_chars, output_chars
- Cost precedence: provider actual → GetPricing → config rates → unknown

### Cache (`internal/cache/`)
LRU response cache engine.

### Breaker (`internal/breaker/`)
Per-provider circuit breaker.

### Scheduler (`internal/scheduler/`)
Background job registry for probes, cleanup, and future learning tasks.

### Metrics (`internal/metrics/`)
Prometheus-compatible metrics collector.

## Configuration

Conductor uses Viper for configuration loading from:
1. Current directory (`.`)
2. `./config` directory
3. `/etc/conductor` directory

Environment variables prefixed with `CONDUCTOR_` override config file values.
Legacy `NOVEXA_*` prefix is still accepted as an alias.

Key config sections:
- `server`: HTTP listen address, port, CORS
- `providers`: per-provider API keys, base URLs, timeouts, model allowlists
- `routes`: model ID → provider/model mappings
- `aliases`: short names → model ID mappings
- `fallbacks`: ordered fallback chains per model
- `health.models`: probing configuration
- `routing`: weighted scoring configuration
- `database`: SQLite path and settings
- `usage` / `cost`: tracking and rate configuration

## Extension Points

The architecture defines numbered extension hooks in the routing pipeline.
Plugins can intercept at these points without modifying core code.

| Hook | Stage | Purpose |
|------|-------|---------|
| `BeforePipeline` | 0 | Intercept before any pipeline stage runs |
| `AfterIntent` | 1 | Observe or modify resolved intent |
| `AfterCapability` | 2 | Observe or modify capability requirements |
| `AfterCandidateGeneration` | 3 | Add/alter candidate providers |
| `AfterSelection` | 4 | Observe or override provider selection |
| `BeforeExecution` | 5 | Modify request before provider call |
| `AfterExecution` | 6 | Observe or transform provider response |
| `AfterDecision` | 7 | Post-decision logging, metrics, learning |

## Data Flow

```
Request → Middleware → Router Pipeline → Provider → Response
              │              │               │           │
              │              │               │           └→ Usage Tracker → SQLite
              │              │               │
              │              │               └→ Runtime → State Tracking
              │              │
              │              └→ Event Bus → Subscribers
              │
              └→ Metrics → Prometheus
```

## Immutability Contracts

The following types are immutable once built:
- `contracts.DecisionContext` — built via builder, read-only after
- `contracts.DecisionTrace` — built via builder, read-only after
- `contracts.DecisionResult` — built via builder, read-only after
- `contracts.ExecutionResult` — built via builder, read-only after
- `contracts.ProviderSnapshot` — built via builder, read-only after
- `contracts.Candidate` — built via builder, read-only after

Mutable runtime state is confined to:
- `router.DecisionContext` — pipeline-internal mutable context
- `runtime.ProviderRuntime` — live state tracking (health, latency, errors)

## Startup Sequence

1. Load config (Viper)
2. Initialize logger (Zap)
3. Open SQLite database (GORM auto-migrate)
4. Create auth service
5. Create provider registry
6. Create event bus
7. Create job scheduler
8. Register enabled providers to registry
9. Create router engine
10. Create model catalog
11. Create cost estimator and usage tracker
12. Create health monitor
13. Create model status store and prober
14. (Optional) Create auto-mode selector
15. (Optional) Create intelligent routing engine
16. Initialize Fiber app and register handlers
17. Start graceful shutdown handler

## Future Architecture (Out of Scope for V2.2-D)

The following subsystems are planned but NOT implemented in this sprint:
- **Learning**: adaptive routing based on historical performance
- **Dashboard**: web UI for monitoring and configuration
- **Trace Persistence**: durable storage of decision traces
- **Enterprise**: multi-tenant, RBAC, audit logging
- **Key Vault**: encrypted provider API key storage
