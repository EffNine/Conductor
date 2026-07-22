# Handoff: Conductor — Unified API Key + Merged Model Picker

## Goal

Build a single-operator, self-hosted AI API gateway that exposes one OpenAI-compatible API key and routes requests across multiple upstream AI provider subscriptions. The VS Code/coding-CLI client sees a merged model picker with provider-qualified duplicates and the operator can check usage/cost across all providers.

## What has been decided

All domain decisions are captured in [CONTEXT.md](../CONTEXT.md):

- Single operator, many provider keys/plans.
- Canonical terms: **Model ID** (user-facing), **Provider Model ID** (upstream slug), **Alias**, **Route**, **Fallback**, **Model Catalog**, **Static Model List**, **Provider Key**, **Usage**, **Cost Rate**.
- `/v1/models` queries provider catalogs; duplicates get provider-prefixed IDs (e.g. `groq/llama3-8b`); prefix is stripped on routing.
- Aliases are config-only shortcuts, not advertised in the model list.
- Routing is explicit (aliases → routes → fallbacks); no auto-provider-selection.
- Usage is token-centric with optional extra counters for non-token providers; costs estimated in USD.
- Cost rates come from public pricing APIs/lists plus per-request cost when available, with manual fallback.
- Dashboard MVP: models, usage, health, logs.

Implementation plan is in [docs/PLAN.md](PLAN.md) with six vertical slices.

## What has been implemented

### Slice 1: Domain cleanup — COMPLETE
- Renamed `internal/model` → `internal/apitypes`.
- Split overloaded `model` field:
  - `RouteConfig.Model` → `RouteConfig.ModelID`
  - `FallbackConfig.Model` → `FallbackConfig.ModelID`
  - `ResolvedRoute.Model` → `ResolvedRoute.ProviderModelID`
  - Added `ResolvedRoute.ModelID`
  - `usage.Record` and `database.UsageRecord` now store both `ModelID` and `ProviderModelID`.
- Updated `config/config.example.yaml` to use `model_id`.
- `go build ./...` passes.

### Slice 2: Provider interface expansion — COMPLETE
- Added to `Provider` interface:
  - `ListModels(ctx) ([]ModelInfo, error)`
  - `GetPricing(ctx) (map[string]PricingInfo, error)`
- Added domain types:
  - `UnitType` (`UnitToken`, `UnitRequest`, `UnitMinute`, `UnitCharacter`)
  - `ModelInfo`, `PricingInfo`
  - `ErrNotImplemented` sentinel.
- Implemented real `ListModels` and `GetPricing` for OpenAI provider.
- Added stub implementations for anthropic, deepseek, gemini, generic, groq, lmstudio, ollama, openrouter.
- Updated `cmd/conductor/main.go` to register all stub providers when enabled.
- `go build ./...` passes.

### Slice 3: Merged `/v1/models` — COMPLETE
- Added `internal/catalog` aggregator: merges provider `ListModels`, qualifies duplicate base IDs with `provider/` prefixes, falls back to static `providers.*.models` when listing fails.
- `/v1/models` uses the catalog; aliases are not advertised.
- Router strips a registered provider prefix before route lookup and rejects prefix/provider mismatches.
- Config: `ProviderConfig.Models` for Static Model List; example YAML updated for ollama/lmstudio.
- Tests cover merge, prefixing, static fallback, alias exclusion, and prefix routing.
- `go test ./...` and `go build ./...` pass.

### Slice 4: Usage/cost enhancements — COMPLETE
- `usage.Estimator` resolves cost by precedence: per-request actual → `Provider.GetPricing` → manual config `cost.rates` → unknown (nil).
- Extended `usage.Record` and `database.UsageRecord` with extra counters: `Requests`, `DurationMs`, `InputChars`, `OutputChars`; token fields remain primary and zero for non-token providers.
- Added `CostSource` column to record which source produced the cost.
- `usage.Tracker.Aggregate` returns totals and per-provider/per-model breakdowns.
- Updated `Provider.PricingInfo` with `UnitSize` to avoid token pricing ambiguity.
- Handler records embedding usage.
- Tests cover pricing estimate, actual override, manual fallback, unknown cost, extra counters, and aggregation.
- `go test ./...` and `go build ./...` pass.

### Slice 5: Dashboard API endpoints — COMPLETE
- `GET /api/models` returns the merged model catalog.
- `GET /api/usage` returns totals plus `by_provider` and `by_model` breakdowns from the usage database.
- `GET /api/health` returns per-provider liveness and latency (live provider checks).
- `GET /api/logs` returns recent request logs from the database.
- All dashboard endpoints are already protected by the same gateway API-key middleware that guards the OpenAI-compatible endpoints.
- Tests cover `/api/usage` aggregation, `/api/logs`, `/api/models`, and API-key rejection.
- `go test ./...` and `go build ./...` pass.

### Model online status / auto-hide — COMPLETE
- Background probes (`health.models`) send minimal chat completions to detect unreachable catalog entries.
- Default: probe `nvidia_nim` only; full pass on startup/redeploy, then every `24h`.
- Hide after `unhealthy_threshold: 2` consecutive definitive failures; unprobed stay visible.
- Dashboard: `GET /api/models`, `GET /api/models/status`, `?include_unreachable=true`.
- Documented in docs + README + AGENTS.

### Curated models only — COMPLETE
- `catalog.curated_only` advertises only the Static Model List under `providers.*.models`.
- Dynamic `ListModels` catalogs are ignored for `/v1/models` and for reachability probes when curated-only is on.
- Providers with an empty `models` list contribute nothing in curated-only mode.
- Documented in CONTEXT (Curated Model List), configuration, providers, architecture, API, README, example config.

### Slice 6: Documentation reconciliation — COMPLETE
- Audited README and docs to match implemented capabilities.
- Provider status table now distinguishes implemented vs stub providers.
- Documented static model lists, manual cost rates, and dashboard endpoints.

### New provider adapters
- Added stub adapters for `opencode`, `nvidia_nim`, and `nous_portal`.
- Fully implemented `opencode`, `nvidia_nim`, and `nous_portal` as OpenAI-compatible adapters:
  - Chat completions (non-streaming and SSE streaming)
  - Embeddings
  - Dynamic model listing
  - Static pricing maps (empty for Nous Portal because it is subscription-based)
  - Live health checks
- Added unit tests for `opencode` and `nvidia_nim`.
- Updated README and `docs/providers.md` status tables.
- `go test ./...` and `go build ./...` pass.

## Remaining work

- Implement `nous_portal` adapter if needed.
- Implement remaining stub providers (Anthropic, Gemini, DeepSeek, OpenRouter, Groq, Ollama, LM Studio) in future slices.
- Consider removing router auto-detect via `SupportsModel` to align with explicit-only routing from CONTEXT.md.

## Important notes for the next agent

- Always use the vocabulary from [CONTEXT.md](../CONTEXT.md); challenge any re-introduction of the ambiguous term `model`.
- `/api/usage/costs`, `/api/config`, and `/api/config/reload` remain stubs and should be documented as planned rather than implemented.
- Provider adapters for OpenCode and NVIDIA NIM follow the same OpenAI-compatible passthrough pattern as the OpenAI adapter; reuse it for future similar providers.

## Suggested skills for the next session

- `tdd` — if any doc-driven behavior gaps surface during Slice 6.
- `review` — before marking Slice 6 complete.

## Artifacts

- Domain glossary: [CONTEXT.md](../CONTEXT.md)
- Implementation plan: [docs/PLAN.md](PLAN.md)
- This handoff: [docs/HANDOFF.md](HANDOFF.md)
