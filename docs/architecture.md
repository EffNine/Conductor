# Novexa Gateway Architecture

## Overview

Novexa Gateway is a single-operator, self-hosted AI gateway. It exposes an OpenAI-compatible API and routes requests to one or more configured upstream providers using explicit routes.

## System Architecture

```
Client (VS Code, Claude Code, Open WebUI, custom apps)
    │
    ▼
┌─────────────────────────────────────────────────────┐
│                Novexa Gateway (Go/Fiber)              │
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

- One gateway API key via `NOVEXA_API_KEY`
- No user management
- Operator owns all upstream provider keys

### Provider Abstraction

- All providers implement a common `Provider` interface
- Currently only the **OpenAI** adapter is fully implemented
- Anthropic, Gemini, DeepSeek, OpenRouter, Groq, Ollama, LM Studio, and Generic are registered as stubs

### Explicit Routing

- The gateway does not auto-select a provider for an unmatched Model ID
- Resolution order: alias → configured route → provider-prefixed route
- Provider prefixes in `/v1/models` are stripped before route lookup

### Catalog

- `/v1/models` queries each provider's `ListModels`
- Duplicate base Model IDs are qualified with `provider/model-id`
- Providers without dynamic listing use the static `models` list from config
- With `catalog.curated_only: true`, only the Curated Model List (`providers.*.models`) is advertised; dynamic catalogs are ignored (including for reachability probes)
- Aliases are never advertised in the catalog
- When model reachability probing is enabled, unreachable models are omitted from `/v1/models` (full list via `/api/models?include_unreachable=true`)

### Model Reachability

- Provider-level `HealthCheck` only proves the upstream API is up, not that each listed model accepts inference
- Especially important for **NVIDIA NIM**: `/models` lists free and unreachable endpoints with no availability flag
- Optional background prober sends minimal chat completions for all registered providers by default (limit with `health.models.providers`)
- Full probe pass on every startup/redeploy, then every `12h` by default
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
2. API key middleware validates against `NOVEXA_API_KEY`
3. Rate limiter checks global and per-provider limits
4. Request validator checks payload structure
5. Router resolves `model`:
   - Strip registered provider prefix if present
   - Resolve alias if exact match
   - Resolve configured route
6. If a fallback chain exists, prepare ordered providers
7. Provider adapter sends the request
8. Response is returned in OpenAI-compatible format
9. Usage tracker records the request asynchronously

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
- SQLite database file (default: `./data/novexa.db`)
- No external dependencies required
- Docker image available
- Deployable on Railway, Fly.io, Render, or locally
