# Phase C1 — Conductor × JEV Shadow Integration

## Status: implemented

**Safety:** JEV is a pure observer. It never feeds back into Conductor's
routing selection, fallback, or retry logic. The deterministic
`DecisionPipeline` remains the sole authority for model selection. JEV
failures, timeouts, unauthorized responses, and rate limits are recorded as
the `advisory_status` of the observation and ignored; the request proceeds
normally on the deterministic route. No new model-selection policy was
introduced, and no live JEV call is made by the test suite.

## 1. Audit findings

| Item | Finding |
|------|---------|
| Production routing seam | `DecisionPipeline.Execute` is called from `HandleChatCompletion` (`internal/handler/chat.go`) for non-virtual chat requests when `routing.enabled: true`. The pipeline publishes `DecisionFinished` (carrying the full `DecisionTrace`) and `DecisionTraceCreated` on the in-process event bus. |
| JEV integration point | A new `shadow` consumer subscribes to `DecisionFinished` and `ExecutionAttemptCompleted` on the event bus (the same bus already used by `TracePersistence` and `AttemptPersistence`). It is a third, isolated consumer — it does not touch the routing path. |
| RouterEngine / DecisionPipeline production-connected? | Yes, when `routing.enabled: true` (default `false`). `NewDecisionPipeline` is constructed in `cmd/conductor/main.go` and wired to the handler via `h.SetDecisionPipeline`. The event bus is instantiated regardless. |
| Existing JEV/TypeSafe references | None before this change. |

The shadow advisor is **independent of `routing.enabled`**: it subscribes to
the same event bus that the pipeline publishes on. When routing is disabled
or a request bypasses the pipeline (virtual models, embeddings), no
`DecisionFinished` event is published and the advisor simply has nothing to
observe — the deterministic behavior is unchanged in every case.

## 2. Implementation

### New packages
- `internal/jev` — a minimal, dependency-free HTTP client for TypeSafe's
  System One evaluation API (`POST /v1/systemone`). Supports Choice, Noul,
  and Score questions. Fails soft: every error class (timeout, unauthorized,
  rate-limited, malformed, status) is captured as a typed `RequestError` so
  callers can record "advisory unavailable" without side effects.
- `internal/jev/shadow` — the shadow advisor:
  - `Advisor` — builds a `ShadowObservation` from a `DecisionTrace`, runs the
    JEV advisory call asynchronously, and persists the row.
  - `Bridge` — the event-bus consumer: subscribes to `DecisionFinished`
    (observation) and `ExecutionAttemptCompleted` (outcome backfill), plus an
    optional hourly retention prune. Mirrors the `TracePersistence` /
    `AttemptPersistence` isolation pattern (bounded concurrency, fresh
    background context, errors logged and never propagated).
  - `SQLiteStore` — GORM-backed persistence for the `shadow_observations`
    table, with idempotent save (unique on `decision_id`), outcome
    backfill, and bounded retention pruning.
  - `QueryHandler` — read-only `GET /api/jev-shadow` dashboard surface.

### Shadow contract (one observation row)
An observation records, for a single routing decision:

- **Routing state**: `requested_model`, `resolved_mode`,
  `candidate_providers`, `candidate_count`.
- **Deterministic decision**: `selected_provider`, `selected_model`,
  `selected_score`.
- **JEV advisory**: `advisory_provider` (JEV's independent choice),
  `advisory_confidence`, `advisory_probabilities` (full distribution),
  `jev_model`, `jev_duration_ms`, `jev_input_tokens`, `jev_output_tokens`.
- **Advisory status**: `ok`, `timeout`, `unauthorized`, `rate_limited`,
  `malformed`, `error`, or `not_configured`.
- **Eventual outcome**: `outcome` (success/failed/skipped, backfilled from
  the execution-attempt event) and `provider_latency_ms`.

The JEV question is a single **Choice** primitive over the candidate
providers: *"Given the routing state, which candidate provider should serve
this request?"* — with the deterministic selection provided as context. This
is one narrow, coherent judgment (per the System One design guidance), run in
a single parallel call, and the answer is consumed only to record evidence.

### Persistence
A new `shadow_observations` table (created by `Migrate()` when the advisor is
enabled). Retention defaults to 7 days (`shadow.retention: 168h`); set `0` to
disable pruning. Existing infrastructure (event bus + the trace/attempt
persistence pattern) was sufficient for the wiring; a new table was needed
because the advisory data has no natural home in `routing_traces` (which is
scoped to the routing decision only, not the JEV judgment or execution
outcome).

### Config
A new `shadow` config section (disabled by default). The API key is read from
`shadow.api_key` in YAML or the `TYPESAFE_API_KEY` env var; it is **never**
persisted into observations. `shadow.endpoint` is validated against the same
SSRF policy as provider base URLs (public https only).

### Files
| File | Change |
|------|--------|
| `internal/jev/client.go` | New: System One HTTP client. |
| `internal/jev/client_test.go` | New: client tests (no live LLM). |
| `internal/jev/shadow/advisor.go` | New: observation + advisory + persistence. |
| `internal/jev/shadow/advisor_test.go` | New: advisor tests. |
| `internal/jev/shadow/bridge.go` | New: event-bus consumer + retention. |
| `internal/jev/shadow/bridge_test.go` | New: bridge tests. |
| `internal/jev/shadow/store.go` | New: observation schema. |
| `internal/jev/shadow/sqlite.go` | New: GORM store. |
| `internal/jev/shadow/query.go` | New: read-only query API. |
| `internal/config/config.go` | `ShadowConfig` + defaults. |
| `internal/config/provider_env.go` | Endpoint SSRF validation. |
| `internal/config/config_test.go` | Shadow config tests. |
| `cmd/conductor/main.go` | Advisor construction + bridge start + endpoint registration (only when `shadow.enabled`). |
| `config/config.example.yaml` | `shadow:` example section. |
| `docs/configuration.md` | `shadow.*` env-var reference. |

## 3. Safety

- **Why production routing cannot change:** the advisor is a downstream
  event-bus subscriber. It reads the `DecisionTrace` *after* the pipeline has
  completed and returned its result to the handler. The pipeline's return
  value and the handler's candidate execution never read any field produced
  by the advisor. The only mutable state the advisor touches is its own
  `shadow_observations` rows.
- **JEV failure behavior:** a failed/timeout/unauthorized/rate-limited JEV
  call sets `advisory_status` on the observation and is logged at debug; the
  observation is still persisted with the deterministic decision and outcome.
  No retry is performed for JEV. No fallback from the production route to
  JEV exists.
- **Data/secrets handling:** observations contain only routing identifiers,
  scores, and the JEV advisory judgment. They never contain prompts, API
  keys, authorization headers, or raw request bodies. The JEV API key is
  held in memory on the client only and is not written to the database. The
  `shadow.endpoint` is validated against the SSRF policy (public https only).

## 4. Tests (no live LLM)

Run:
```
CGO_ENABLED=1 go test -race ./internal/jev/...
```
All tests use `httptest` fake servers and in-memory stores. They prove:
1. Normal routing behavior is unchanged (deterministic selection is recorded
   verbatim even when JEV advises a different provider).
2. The observation is invoked at the intended event seam.
3. The JEV advisory output does not modify the selected route.
4. A failing JEV call does not break the observation.
5. A JEV timeout is captured without breaking routing.
6. Missing/invalid JEV output is recorded as `malformed`/`not_configured`.
7. The observation associates routing state, the deterministic decision, the
   advisory result, and the outcome.
8. The existing routing tests still pass (no change to the pipeline or
   handler).
9. No secrets or credentials are persisted (explicit field scan).

## 5. Verification

- Existing routing/decision-pipeline tests pass unchanged.
- No benchmark or live inference was run.
- No fabricated calibration or success-rate numbers are recorded; the
  observation stores the raw JEV output (probabilities, confidence, status)
  for a future offline evaluation.

## 6. Remaining work (Phase C2 prerequisite)

Before JEV can be *evaluated* as a routing advisor (not before it can be
*used* one), the following are needed:
1. A corpus of routing decisions with known outcomes (the `shadow_observations`
   rows are the seed; a stable join between `decision_id` and the execution
   outcome is already present via backfill).
2. An offline evaluation harness that loads `shadow_observations` and
   computes advisory-vs-deterministic agreement, calibration, and
   "would-JEV-have-picked-the-winner" rates per mode/provider.
3. Domain-specific calibration thresholds.
4. A decision on whether advisory signals become routing inputs — out of
   scope for C1 and explicitly blocked until C2.
