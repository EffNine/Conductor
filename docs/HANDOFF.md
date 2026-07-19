# Handoff: Novexa Gateway — Unified API Key + Merged Model Picker

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
- Split overloaded `model` field into Model ID / Provider Model ID across config, router, usage, and DB.
- `go build ./...` passes.

### Slice 2: Provider interface expansion — COMPLETE
- `ListModels` / `GetPricing` on `Provider`; OpenAI implemented; others stubbed with `ErrNotImplemented`.
- `PricingInfo.UnitSize` clarifies per-N-unit pricing (e.g. per 1K tokens).

### Slice 3: Merged `/v1/models` — COMPLETE
- `internal/catalog` merges catalogs, prefixes duplicates, static `providers.*.models` fallback.
- Aliases excluded from `/v1/models`; router strips provider prefixes on resolve.

### Slice 4: Usage/cost enhancements — COMPLETE
- `usage.Estimator` precedence: actual per-request cost → `GetPricing` → manual `cost.rates` → unknown (nil, no invented default).
- Usage schema: `Requests`, `DurationMs`, `InputChars`, `OutputChars`; tokens remain primary (0 for non-token).
- DB `EstimatedCostUSD` is nullable; `CostSource` recorded.
- `Tracker.Aggregate` returns totals + by-provider / by-model (ready for Slice 5 `/api/usage`).
- Config: `cost.rates[]` with provider, provider_model_id, unit_type, unit_size, prices.
- Tests cover pricing, actual override, manual fallback, unknown, extra counters, aggregation.
- `go test ./...` and `go build ./...` pass.

## Remaining work

Slices 5–6 from [docs/PLAN.md](PLAN.md):

5. **Dashboard API endpoints** — wire `/api/models` (catalog), `/api/usage` (Aggregate), `/api/health`, `/api/logs`.
6. **Documentation reconciliation** — rewrite README/docs to match actual capabilities.

## Important notes for the next agent

- Always use the vocabulary from [CONTEXT.md](../CONTEXT.md); challenge any re-introduction of the ambiguous term `model`.
- Router still has legacy auto-detect via `SupportsModel` when no route exists; CONTEXT says explicit-only.
- Slice 5 should call `catalog.List` and `usage.Tracker.Aggregate` rather than reimplementing them.
- Providers do not yet populate `ActualCostUSD` from upstream responses; the estimator path is ready when they do.
- Provider prefixes in the model list are serialization-only; route config uses bare Model IDs.

## Suggested skills for the next session

- `tdd` — for test-first implementation of slice 5.
- `review` — before merging.
- `to-issues` — if remaining slices need separate tickets.

## Artifacts

- Domain glossary: [CONTEXT.md](../CONTEXT.md)
- Implementation plan: [docs/PLAN.md](PLAN.md)
- This handoff: [docs/HANDOFF.md](HANDOFF.md)
