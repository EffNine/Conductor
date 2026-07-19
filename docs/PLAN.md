# Novexa Gateway — Implementation Plan

Parent goal: one gateway API key that proxies to multiple upstream AI provider keys, exposes a merged model picker, and tracks usage/cost across providers.

## Slice 1: Domain cleanup

Unblocks all other slices.

### What to build

Rename the OpenAI-compatible request/response package to avoid collision with the domain concept of a model. Split the overloaded `model` field into `Model ID` (user-facing route key) and `Provider Model ID` (upstream slug). Update config structs, router, and handler to use the new vocabulary.

### Acceptance criteria

- [ ] `internal/model` is renamed to `internal/apitypes`.
- [ ] All imports and references are updated.
- [ ] `RouteConfig.Model` / `FallbackConfig.Model` are renamed or documented as `ModelID`.
- [ ] `ResolvedRoute.Model` is renamed to `ProviderModelID`.
- [ ] `usage.Record.Model` stores the resolved Model ID, and `ProviderModelID` is stored separately.
- [ ] Config example uses `model_id` and `provider_model` consistently.
- [ ] `go build ./...` passes.
- [ ] Existing OpenAI provider chat completions still work.

### Blocked by

None - can start immediately.

---

## Slice 2: Provider interface expansion

### What to build

Extend the `Provider` interface so every adapter can list models and report pricing. Add stubs for all configured providers so the registry has a uniform surface.

### Acceptance criteria

- [ ] `Provider` interface gains `ListModels(ctx)` returning provider-native model entries.
- [ ] `Provider` interface gains `GetPricing(ctx)` or equivalent returning cost rates.
- [ ] All provider packages implement the interface (stubs returning "not implemented" are acceptable).
- [ ] Registry can iterate providers and call these methods safely.

### Blocked by

Slice 1.

---

## Slice 3: Merged `/v1/models`

### What to build

Implement the model catalog aggregator. Query each provider's model list, merge results, apply provider prefixes for duplicates, fall back to static lists, and return an OpenAI-compatible `/v1/models` response.

### Acceptance criteria

- [x] `/v1/models` returns entries from all configured providers.
- [x] Duplicate base model IDs from different providers appear as distinct entries with provider prefixes.
- [x] Provider prefix is stripped during subsequent chat request routing.
- [x] Providers without `ListModels` use their configured static model list.
- [x] Aliases are not advertised in `/v1/models`.

### Blocked by

Slice 2.

---

## Slice 4: Usage/cost enhancements

### What to build

Extend usage records with optional extra counters for non-token providers. Fetch public pricing where available, record per-request cost when providers expose it, and keep manual cost-rate fallback.

### Acceptance criteria

- [x] Usage schema supports extra counters (`requests`, `duration_ms`, `input_chars`, `output_chars`, etc.).
- [x] Token fields remain primary and are zero/null for non-token providers.
- [x] Cost estimation uses fetched pricing when available.
- [x] Per-request cost returned by providers can override estimates.
- [x] Manual cost-rate fallback works when no pricing source is available.
- [x] Dashboard can aggregate totals and per-provider/per-model breakdowns.

### Blocked by

Slice 1.

---

## Slice 5: Dashboard API endpoints

### What to build

Implement the management API: merged model list, usage totals/breakdowns, provider health, and recent request logs.

### Acceptance criteria

- [ ] `GET /api/models` returns the merged catalog.
- [ ] `GET /api/usage` returns total and per-provider/per-model usage with estimated cost.
- [ ] `GET /api/health` returns per-provider liveness and latency.
- [ ] `GET /api/logs` returns recent request log entries.
- [ ] Endpoints are protected by the gateway API key.

### Blocked by

Slice 3 and Slice 4.

---

## Slice 6: Documentation reconciliation

### What to build

Rewrite README and docs so the documented feature set matches the implemented code. Remove claims about unimplemented providers or features unless clearly marked as planned.

### Acceptance criteria

- [ ] README accurately describes single-operator, multi-provider routing.
- [ ] Provider support table reflects which adapters are implemented vs stubbed.
- [ ] Configuration docs match the new `model_id` / `provider_model` vocabulary.
- [ ] Architecture doc matches the actual request flow and dashboard scope.

### Blocked by

Slice 5.
