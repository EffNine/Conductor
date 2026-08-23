# Conductor Architecture

## Overview

Conductor is a single-operator, self-hosted AI gateway. It exposes an OpenAI-compatible API and routes requests to one or more configured upstream providers using explicit routes, virtual categories, and automatic failover, with optional score-based model selection.

## System Architecture

```
Client (VS Code, Claude Code, Open WebUI, custom apps)
    │
    ▼
┌─────────────────────────────────────────────────────┐
│                Conductor (Go/Fiber)              │
│                                                     │
│  API Key Check → Rate Limit → Validate → Route      │
│       → Provider Adapter → Normalize → Response     │
│                                                     │
│  Catalog: merges provider model lists, qualifies    │
│           duplicates with provider prefixes,        │
│           optionally filters by model reachability  │
│                                                     │
│  Model Prober: minimal chat probes (esp. NIM) to    │
│                hide unreachable catalog entries     │
│                                                     │
│  Usage Tracker: records tokens, counters, latency,  │
│                 cost source to SQLite               │
│                                                     │
│  SQLite (usage, logs, health records)               │
└─────────────────────────────────────────────────────┘
```

## Key Design Decisions

### Single-Operator Model

- One gateway API key via `CONDUCTOR_API_KEY`
- No user management
- Operator owns all upstream provider keys

### Provider Abstraction

- All providers implement a common `Provider` interface
- Most adapters share an OpenAI-compatible base adapter (`internal/provider/openaibase`); each adds provider-specific pricing, base URLs, and request quirks
- OpenAI-compatible providers (DeepSeek, Groq, Mistral, Cerebras, NVIDIA NIM, …) reuse this base directly
- **Anthropic** and **Gemini** have dedicated request/response mappers because their native APIs differ from OpenAI's wire format
- A `generic` adapter serves any operator-configured OpenAI-compatible endpoint

### Explicit Routing

- Explicit configuration takes precedence: resolution order is alias → configured route → provider-prefixed route
- Provider prefixes in `/v1/models` are stripped before route lookup
- Bare model IDs auto-resolve when exactly one provider is configured (`routing.auto_resolve_bare_models`, default on)
- Virtual categories (`auto`, `fast`, `coding`, `frontier`, …) resolve to concrete models at request time
- On failure, requests retry and fail over: static fallback chains first, then dynamic category-preserving alternates drawn from the catalog (`routing.dynamic_fallback`, default on), gated by per-provider circuit breakers

### Catalog

- `/v1/models` queries each provider's `ListModels`
- Duplicate base Model IDs are qualified with `provider/model-id`
- Providers without dynamic listing use the static `models` list from config
- With `catalog.curated_only: true`, providers with a Curated Model List (`providers.*.models`) advertise only that allowlist; providers without one still use dynamic catalogs
- Aliases are never advertised in the catalog
- When model reachability probing is enabled, unreachable models are omitted from `/v1/models` (full list via `/api/models?include_unreachable=true`)

### Model Reachability

- Provider-level `HealthCheck` only proves the upstream API is up, not that each listed model accepts inference
- Especially important for **NVIDIA NIM**: `/models` lists free and unreachable endpoints with no availability flag
- Optional background prober sends minimal chat completions for all registered providers by default (limit with `health.models.providers`)
- Full probe pass on every startup/redeploy, then every `2h` by default; failed models retry sooner on exponential backoff
- Results are cached and also updated from live chat traffic; rate limits / auth errors are ignored
- Dashboard: `/api/models`, `/api/models/status`

### Usage and Cost

- Primary counters are OpenAI-style tokens
- Extra counters (`requests`, `duration_ms`, `input_chars`, `output_chars`) support non-token providers
- Cost estimation precedence:
  1. Per-request actual cost from provider
  2. Provider `GetPricing`
  3. Manual `cost.rates` from config
  4. Unknown (omitted/null, no invented default)
- All cost values are USD

### Storage

- SQLite is the default database
- Usage records, request logs, and provider health records are persisted
- WAL mode is enabled for SQLite

## Request Lifecycle

1. Client sends `POST /v1/chat/completions` with `Authorization: Bearer <key>`
2. API key middleware validates against `CONDUCTOR_API_KEY`
3. Global inbound rate limiter checks the request (`rate_limit.*`)
4. Request validator checks payload structure
5. Router resolves `model`:
   - Strip registered provider prefix if present
   - Resolve alias if exact match
   - Resolve configured route
6. The resilience executor applies retry policy and circuit-breaker gating, then tries the primary provider, static fallbacks, and dynamic alternates in order
7. Provider adapter sends the request
8. Response is returned in OpenAI-compatible format
9. Usage tracker records the request asynchronously; each attempt is persisted for failure analytics (`/api/failures`)

## Intelligent Routing (opt-in)

- `routing.enabled: true` activates the scoring router: candidates are scored by health, latency, cost, and capability weights (`routing.weights.*`)
- Selection decisions produce decision traces (why a model was chosen, which alternatives were rejected) persisted to SQLite and exposed at `/api/routing/traces`
- Default remains explicit/static routing; the scoring engine is additive, not required

## Task Orchestration

- `POST /api/tasks` creates persistent tasks: intent classification → capability match → plan generation → bounded agent loop with tool calls (fs, shell, git) → verification
- Tasks, steps, tool calls, and events persist to SQLite; execution runs on an internal worker pool with checkpoint/resume and cancellation

## Streaming Flow

Same as above through route resolution, then:

1. Provider adapter opens a streaming connection
2. Gateway reads SSE chunks from the provider
3. Each chunk is forwarded to the client
4. Final usage is recorded when the stream completes

## Security Model

- Provider API keys are stored in environment variables or config, never logged
- Request/response bodies are not logged by default
- CORS is configurable
- Payload size limits are enforced
- Dashboard endpoints require the same gateway API key as OpenAI-compatible endpoints

## Deployment Model

- Single binary, single process
- SQLite database file (default: `./data/conductor.db`)
- No external dependencies required
- Docker image available
- Deployable on Railway, Fly.io, Render, or locally
