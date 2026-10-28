# Conductor Engineering Assessment

**Date:** 2026-09-19 (UTC)
**Scope:** Engineering archaeology + maintenance-value assessment. Investigation only; no code, config, deployment, test, or dependency changes were made (this report file is the sole new artifact).
**Label convention:** Every substantive claim below is prefixed with exactly one of **FACT**, **OBSERVATION**, or **INFERENCE**. Unlabeled prose is structure/transition only.

---

## 1. Executive summary

- **FACT:** Conductor is a single-binary Go/Fiber OpenAI-compatible AI gateway (module `github.com/EffNine/conductor`, Go 1.21) that routes chat/embeddings across ~19 upstream provider adapters, with virtual capability models, health probing, circuit breaking, retry/fallback budgets, usage/cost persistence in SQLite, a task/agent orchestration runtime, Prometheus metrics, and a JEV (TypeSafe System One) shadow observer.
- **FACT:** The working tree is **dirty**: 6 modified files plus 3 untracked files (`git status` at investigation time). The uncommitted delta implements a routing-candidate fix (`catalog.ListReachable` + `ShouldAdvertiseForRouting` + candidate dedup) with two new regression-test files. The most recent commits (2026-09-18) are JEV shadow Phase C1/C2, a JEV outcome-correlation fix, and a health `skip_providers` feature.
- **FACT:** `go test ./...` passes across all packages at investigation time (cached results, `internal/integrationtest` ~7.4s). The repo contains 183 test files vs ~170 non-test source files.
- **OBSERVATION:** The liveliest recent work is **experimental reliability/routing science** (JEV shadow observation, candidate-pool collapse under strict health filtering, local C3B/C3C multi-provider configs), not production feature delivery.
- **OBSERVATION:** Repository evidence shows an **intended** Fly.io deployment (`fly.toml`, `scripts/fly-deploy.sh`, version stamping), but **live usage could not be verified**: `fly apps list` under current credentials returns no apps, `fly status -a conductor` reports app not found, `https://conductor.fly.dev/health` returns Fly-proxy 404 (not Conductor's `{"status":"ok"}`), and `https://conductor.effnine.dev/health` is DNS-unresolvable from here. The local `./data/conductor.db` has zero rows in every user table.
- **INFERENCE:** Conductor today is best described as **(C) experimental infrastructure with reusable engineering value**: technically coherent and test-green, dormantly deployed at best, with real maintenance/debugging substance concentrated in routing correctness, health semantics, and observability — not in serving verified production traffic.
- **FINAL DECISION:** `CONDUCTOR IS A GOOD CODEBRO TESTBED` (rationale in §12–§14).

---

## 2. What Conductor is today

- **FACT:** Single Go/Fiber binary (`cmd/conductor/main.go`), CGO-required SQLite driver (`mattn/go-sqlite3`), default DSN `./data/conductor.db`, single gateway API key (`CONDUCTOR_API_KEY`, Bearer-only, `/health` public, per-scope `conductor:read/execute/admin` on all other routes).
- **FACT:** OpenAI-compatible surface: `POST /v1/chat/completions`, `POST /v1/embeddings` (streaming SSE supported), `GET /v1/models` returns **exactly 10 virtual capability models** (`frontier`, `coding`, `reasoning`, `agentic`, `planning`, `long_horizon`, `fast`, `light`, `vision`, `auto`) — verified in `internal/handler/models.go:HandleListModels`.
- **FACT:** Dashboard/ops surface (all gateway-key authenticated): `GET /api/models` (merged raw catalog, `?include_unreachable`), `GET /api/models/status`, `POST /api/models/force-probe`, `POST /api/models/reset-status`, `GET /api/auto/status`, `GET /api/health`, `GET /api/providers`, `GET /api/usage`, `GET /api/usage/costs` (implemented aggregation, **not** a stub — see `internal/handler/dashboard.go`), `GET /api/logs`, `GET /api/config` (redacted, implemented), `PUT /api/config/reload` (implemented via `reloadFn`, 503 when unwired), `GET /api/metrics` (Prometheus), `GET /api/circuit-breaker`, `GET /api/routing`, `GET /api/routing/traces` + `/api/routing/traces/:id`, `GET /api/failures` + `/api/failures/summary`, `GET /api/cache`, `GET /api/streams`, `GET /api/runtime`, plus task routes `POST/GET /api/tasks…` (`internal/task/handler.go`) and `GET /api/jev-shadow` (read-only shadow query, wired in `main.go` when shadow enabled).
- **FACT:** Provider adapters present under `internal/provider/`: `openai`, `anthropic`, `gemini`, `deepseek`, `openrouter`, `groq`, `opencode`, `nvidianim`, `nousportal`, `ollama`, `lmstudio`, `kilocode`, `mistral`, `zai`, `cerebras`, `requesty`, `xai`, `agnesai`, `cloudflare`, plus `generic` and shared `openaibase`. Most reuse the OpenAI-compatible base; Anthropic/Gemini have dedicated mappers; tool-schema normalization exists per provider (evidence report `GEMINI_TOOL_SCHEMA_NORMALIZATION_EVIDENCE.md`).
- **FACT:** Routing stack: legacy explicit `router.Engine` (alias → route → provider-prefixed → bare-ID auto-resolve when exactly one provider configured) plus opt-in scoring pipeline (`routing.enabled`, **default false**; `NewDecisionPipeline` wired in `main.go`, `SetDecisionPipeline` on handler). Virtual/auto resolvers score catalog candidates by health/latency/cost/capability with per-mode profiles, hard filters (vision, context capacity, reasoning+tools), deterministic tie-breaking, and immutable `DecisionTrace` persistence + query API. Static fallbacks run first; dynamic category-preserving fallback (`routing.dynamic_fallback`, default on, `max_candidates: 3`) draws alternates from the catalog.
- **FACT:** Reliability core (P4.1–P4.5, per `docs/HANDOFF.md` + `docs/reports/P4.*`): canonical 8-class failure taxonomy (`internal/failure`), class-driven retry engine (`internal/resilience`, same-provider, pre-first-chunk for streams, Retry-After honored capped at 30s), classification-aware breaker (rate_limited = throttle-only health-neutral, auth/invalid ignored, streaming credit on completion, open-primary failover), request-scoped budgets (120s deadline, 8 attempts, 200k tokens; streams detach on acquisition), async attempt persistence (`request_attempts`, 16-slot semaphore, drop-not-block) with 168h retention pruning, and read-only failure analytics.
- **FACT:** Task/agent runtime is wired in production `main.go`: `task.MigrateTasks`, `task.NewSQLiteStore`, `agent.New`, `task.NewTaskExecutor`, `taskHandler.Register(app)`, plus `coordinator` and `worker` subsystems. This is the V2.5/V2.6 orchestration lineage (intent → plan → bounded agent loop with fs/shell/git tools → verify, checkpoint/resume/cancel/retry).
- **FACT:** JEV integration is a **pure shadow observer** (Phase C1 + C2, commits `5faf736`, `d56971d`): `internal/jev` minimal HTTP client to `https://api.typesafe.ai/v1/systemone` (model `jev-latest`), `internal/jev/shadow` advisor subscribes to `DecisionFinished` + `ExecutionAttemptCompleted` on the in-process event bus (same isolation pattern as trace/attempt persistence), persists `shadow_observations` (idempotent on `decision_id`), exposes read-only `GET /api/jev-shadow`, plus an offline eval harness under `internal/jev/shadow/eval`. Default `shadow.enabled: false`; without `TYPESAFE_API_KEY` advisories record `not_configured`.
- **FACT:** Health/readiness has two distinct layers: (a) provider-level `HealthCheck` liveness (`GET /api/health`), and (b) per-model reachability probing (`ModelProber`: full pass on startup/redeploy, then every 2h default, concurrency 3, unhealthy_threshold 1, `unknown_as_reachable: true`, `hide_unreachable: true`, exponential backoff 30s→12h ×3.5, 100ms batch window, error-rate degraded marking, loopback providers skipped, state persisted to SQLite, `skip_providers` added 2026-09-18 in `ae0353e`).
- **FACT:** Persistence (GORM AutoMigrate in `internal/database/database.go`): `usage_records`, `request_logs`, `provider_health_records`, `cost_rates`, `model_status_records`, `model_probe_meta`, `routing_traces` (`RoutingTrace`), `request_attempts` (`RequestAttempt`), task tables (`tasks`, `task_steps`, `task_tool_calls`, `task_events`), `shadow_observations`, coordinator tables. Cost precedence: per-request actual → provider `GetPricing` → manual `cost.rates` → unknown (nil, never invented).
- **FACT:** Observability: Prometheus exposition (`conductor_build_info`, uptime, requests/errors, streams, `conductor_fallback_total{kind,provider}`, per-provider stats), structured zap lifecycle logs with `correlation_id`/`request_id`, `fallback_engaged` logs, `/api/streams` and `/api/runtime` snapshots, routing traces, failure analytics. No distributed tracing; no routing-trace retention policy (explicitly documented gap).

---

## 3. Original purpose

- **FACT:** The five oldest commits state the founding slices verbatim: merged model catalog for `/v1/models`; usage/cost estimation with pricing precedence; dashboard endpoints (models/usage/health/logs); documentation reconciliation; stub adapters for opencode/nvidia_nim/nous_portal.
- **FACT:** `CONTEXT.md` (canonical glossary), `docs/PLAN.md` (Slices 1–6), and `docs/HANDOFF.md` jointly define the original problem: a **single-operator, self-hosted gateway** merging many provider subscriptions behind **one gateway key and one endpoint**, with a merged model picker (provider-prefixed duplicates), explicit routes/aliases/fallbacks, token-centric usage with extra counters for non-token providers, USD cost estimation, and a dashboard MVP (models, usage, health, logs).
- **FACT:** Later milestones layered on top without removing the base: P3 capability routing (10 virtual models + decision pipeline, P3.3–P3.17), P4 reliability core (P4.1–P4.5), streaming hardening (Sprint 20), V2.4–V2.7 task/orchestration hardening, provider expansion to 19 adapters, and JEV shadow C1/C2.
- **INFERENCE:** The original "merge my subscriptions behind one key" problem is the stable kernel; everything else (virtual models, scoring, tasks/agents, JEV shadow) is successive experimental scope growth on top of that kernel.

---

## 4. Current purpose

- **OBSERVATION:** Code-complete, test-green gateway **plus** three partially-overlapping decision layers that a newcomer must disentangle: (1) legacy explicit routing (default), (2) opt-in scoring pipeline with virtual models, (3) residual per-provider NIM `auto` config (documented as deprecated in the V2 design doc but still present in `config.example.yaml` and still wired via `automode.NewRouterAdapter` in `main.go`).
- **OBSERVATION:** The freshest behavioral work (uncommitted `ListReachable`/`ShouldAdvertiseForRouting`/dedup + C3B/C3C local configs + `skip_providers`) is about **candidate-pool correctness under health filtering** — i.e., keeping the router honest — rather than about serving new users.
- **INFERENCE:** The current *de facto* purpose is a **routing-reliability laboratory** (health semantics, fallback science, failure analytics, JEV advisory comparison) attached to a still-functional gateway, not an actively consumed production service. Nothing in the repo contradicts this; the empty local DB and unverifiable deployment corroborate it.

---

## 5. OpenCode overlap

Boundary assessed from the Conductor side plus the task-provided context that OpenCode now ships native Go/package model capability. OpenCode internals were **not** inspected; overlap claims are therefore labeled INFERENCE unless they rest on Conductor-internal facts.

| Conductor responsibility | OpenCode-native overlap (INFERENCE) | Conductor-unique remainder (FACT-grounded) |
|---|---|---|
| Single key merging many subscriptions | High overlap if the user works only inside OpenCode with its own provider auth; the "one key for VS Code/Continue/Aider/Open WebUI/Claude Code" story weakens when OpenCode is the only client | Still unique for **non-OpenCode clients** (Open WebUI, Continue, custom apps) and for operators who want one ledger across heterogeneous clients — FACT: Conductor serves standard OpenAI wire protocol to arbitrary clients |
| Bare/virtual model abstraction (`auto`, `coding`, …) | Overlapping intent: OpenCode has its own model selection UX | Conductor's mechanism is distinct and observable: catalog-backed scoring with weights, hard filters, deterministic tie-breaks, persisted `DecisionTrace`s — FACT: `internal/router/*`, trace query API |
| Failover / retry / breaker / budgets | Partial: any competent agent framework retries; OpenCode may do so internally | Conductor's cross-provider category-preserving fallback, classification-aware breaker (throttle vs open), and request-scoped budgets are gateway-level and client-agnostic — FACT: P4 reports + `internal/resilience`, `internal/breaker`, `internal/handler/dynamic_fallback.go` |
| Usage/cost ledger | OpenCode tracks its own spend; overlap for single-client accounting | Conductor aggregates **across providers and across clients** in one SQLite store with cost-source provenance — FACT: `usage_records` + `cost_rates` + `/api/usage*` |
| Health probing / reachability (esp. NIM) | No equivalent claimed for OpenCode as a client; probing is inherently a gateway concern | Unique: background probes, backoff, persisted model status, `unknown_as_reachable` semantics — FACT: `internal/health/*` |
| Task/agent orchestration (`/api/tasks`, coordinator, workers, tools) | Highest overlap risk: OpenCode *is* an agent runtime with fs/shell/git, checkpoints, retries | Conductor's runtime remains independently wired (FACT: `main.go` task mount) but **no in-repo client or doc path demonstrates current use** (OBSERVATION); retain-or-retire is an open maintenance question, not a finding |
| JEV shadow advisor + offline eval | No OpenCode overlap asserted; this is Conductor-specific instrumentation | Unique by construction: pure-observer advisory comparison with persistence — FACT: `internal/jev/*` |
| SSRF protection, scope auth, redaction, SSE hardening | Framework-level concerns any host needs; OpenCode solves them differently in-process | Conductor's are wire-gateway specifics (multi-tenant-ish hardening for an exposed endpoint) — FACT: `internal/security`, `ValidateProviderURL` tests, `ARCHITECTURE_LOCK.md` I12 |

- **INFERENCE:** Overlap does **not** imply delete. It implies the gateway's surviving justification is everything OpenCode cannot do from inside a single agent process: cross-client aggregation, cross-provider failover with health evidence, provider-reachability science, and independent auditability (traces, attempts, costs). The task/agent runtime is the one subsystem where overlap is plausibly fatal; it needs a usage verdict, not an assumption.

---

## 6. Current deployment role

### REPOSITORY EVIDENCE

- **FACT:** Root `fly.toml`: `app = 'conductor'`, region `sin`, Dockerfile `deployments/Dockerfile`, `CONDUCTOR_SERVER_PORT=8080`, probe tuning env (`UNHEALTHY_THRESHOLD=1`, `CONCURRENCY=3`, `CHECK_INTERVAL=1h`), 1GB volume `conductor_data` → `/app/data`, `http_service` with `force_https`, `auto_stop_machines='stop'`, `min_machines_running=0`, `/health` checks, `shared-cpu-1x/256mb`.
- **FACT:** `scripts/fly-deploy.sh` is a careful one-shot deploy (app/volume creation, gateway-secret reuse-vs-generate logic, provider secrets, remote builder, `VERSION` stamping so `/health` and `conductor_build_info` self-identify).
- **FACT:** `deployments/Dockerfile` is CGO-correct (`CGO_ENABLED=1`, gcc, `libgcc` runtime), non-root `conductor` user, entrypoint fixing volume ownership.
- **FACT:** `deployments/fly.toml` is a **stale duplicate** of root `fly.toml` (its own header says the canonical config lives at root; env blocks differ — root has probe tuning, deployments copy does not). Docs reference both paths.
- **FACT:** `ARCHITECTURE_LOCK.md` (2026-08-30, EffNine infra freeze) attests Conductor healthy **via a different topology**: Docker `effnine-conductor:8080` behind Caddy + Cloudflare Tunnel (`conductor.effnine.dev`), with `go test 44 packages PASS` and public smoke checks. That is host/Tunnel evidence, not Fly evidence, and is 20 days old at assessment time.
- **FACT:** Local `./data/` contains `conductor.db` (all user tables zero rows) and a stale `p3_16_e2e.db` — consistent with test runs, not service traffic.

### LIVE DEPLOYMENT UNKNOWN

- **FACT:** Read-only live checks performed, no mutations: `fly apps list` → "No apps found" under current credentials; `fly status -a conductor` → app not found; `curl https://conductor.fly.dev/health` → Fly-proxy `{"status_code":404,"detail":"Not Found"}` (not Conductor's `{"status":"ok"}`); `curl https://conductor.effnine.dev/health` → DNS failure. `fly.toml` header notes the app was generated for `conductor-yknfkg`; that hostname is also unresolvable from here.
- **OBSERVATION:** The deployment configuration is **coherent** (builds, mounts, checks, secrets handling all hang together) and **intentional** (version stamping, probe tuning, volume), but coherence is not usage.
- **INFERENCE:** Do not infer usage from deployment files. Current Fly role is best stated as **deployed-or-deployable but presently unverifiable and, by all reachable evidence, idle**.

---

## 7. Active vs dormant evidence

| Signal | Reading |
|---|---|
| **FACT:** 130 commits; last frozen baseline 2026-08-30; JEV C1/C2 + fixes 2026-09-18; uncommitted routing fix + 2 new test files + disposable `config/c3b-local.yaml` / `c3c-local.yaml` present | **OBSERVATION:** Engineering-active within the last 24–48h, but on **experimental/debugging** tracks (shadow data collection, candidate-pool collapse), not releases (`CHANGELOG.md` `[Unreleased]` section matches this) |
| **FACT:** `git status` dirty (6 modified, 3 untracked including `opencode.jsonc` disabling CodeBro) | **OBSERVATION:** Work in flight, undispositioned; the tree is not in a landable state |
| **FACT:** Local DB tables all zero rows; no telemetry, logs, or usage artifacts in-repo | **OBSERVATION:** No evidence of served traffic from this checkout |
| **FACT:** No in-repo external client, script, cron, or service manifest points at a live Conductor URL except curl examples and the mock-based `deployments/local-integration` stack | **OBSERVATION:** "Something actually uses X" is unproven for every subsystem; "the code supports X" is proven for all of them |
| **FACT:** EffNine `conductor-worker` (Cloudflare proxy → Fly) is referenced in `ARCHITECTURE_LOCK.md` but its source (`domain/conductor-worker`) is **not in this repo** | **INFERENCE:** The one plausible external dependent lives outside this repository's evidence boundary and cannot be confirmed here |

Phase-2 classification: **C. Experimental infrastructure with reusable engineering value.** Rejected alternatives: **A** (no traffic evidence), **B** (implies useful-but-idle production; closer but understates the experimental JEV/routing-science character and overstates deployment verifiability), **D** (overstates: routing/health/failure-analytics/JEV-harness are genuinely reusable), **E** (we have abundant code/history/test evidence; what is missing is specifically *usage* evidence, which §15 bounds).

---

## 8. Architecture summary

- **FACT:** Request path: `Auth (Bearer + scope)` → `RateLimit` → `Validate` → `Route` (explicit or decision pipeline) → `Resilience executor` (budgets → breaker gate → per-candidate retry) → `Provider adapter` → `Normalize` → `Response`, with async `Usage/Cost` + `Attempt` + `Trace` + `Shadow` persistence off the request path via the in-process event bus.
- **FACT:** Catalog: per-provider `ListModels` merged, duplicates qualified `provider/model`, static `models:` fallback, `curated_only` allowlist mode, reachability filtering with the new routing-aware split (`ShouldAdvertise` for display vs `ShouldAdvertiseForRouting` for candidates — currently **uncommitted**).
- **FACT:** Subsystem map (`internal/`): `handler`, `router` (+`automode` legacy), `catalog`, `health`, `breaker`, `failure`, `resilience`, `provider/*`, `usage`, `database`, `metrics`, `middleware`, `auth`, `config`, `security`, `cache`, `eventbus`, `runtime`, `policy`, `task`, `agent`, `orchestration`, `coordinator`, `worker`, `tool/*`, `jev/*`, `apitypes`, `buildinfo`, `integrationtest`, plus `pkg/sse`, `cmd/conductor`, `tests/{c2eval,provider-certification}`, `benchmarks`, `loadtest`.
- **FACT:** ADRs 0001–0005 (`docs/adr/`) establish subsystem boundaries, router responsibility splits, runtime abstraction, event bus, and explainability contracts; `docs/architecture/` adds constitution/boundaries/dependency rules.
- **OBSERVATION:** The architecture is **layered but triple-pathed** in routing (explicit default vs scoring opt-in vs residual NIM auto) and **dual-homed** in operations (Fly files vs EffNine Tunnel history). Both multiplicities are the top sources of newcomer confusion and regression risk.

---

## 9. Historical engineering decisions

- **FACT (Slices 1–6):** Explicit-routing doctrine — aliases → routes → fallbacks, no auto-provider-selection; `Model ID` vs `Provider Model ID` vocabulary split; provider prefixes as serialization convention stripped before lookup. Source: `CONTEXT.md` + `docs/HANDOFF.md`.
- **FACT (P3.3–P3.17):** Deliberate reversal of the no-auto doctrine for **virtual models only**: classifier → capability filter → scoring → selection, with mode profiles, telemetry-weighted scoring, immutable `DecisionTrace` contract, SQLite trace persistence, and read-only trace query API. Legacy static routing preserved as default (`routing.enabled: false`). Sources: `docs/HANDOFF.md` slice table + `docs/reports/P3.*`.
- **FACT (P4.1–P4.5):** Reliability without self-learning routing: failure taxonomy → retry engine → classification-aware breaker → budgeted executor → attempt persistence + retention → failure analytics. Explicit non-goal recorded: no learning routing; `DecisionTrace` immutable; OpenAI API unchanged.
- **FACT (health lineage):** `git log -S ShouldAdvertise` shows the probe/hide lineage (`#5`, `#10`, `#11`, `#21`, `#25`, `#26`, plus `a67026d` batch-flush-before-ready). The through-line is **availability-erring iteration**: probe-all → flicker complaints → revert aggressive defaults → persist across cold starts → atomic batched snapshots → backoff → per-provider filtering. The uncommitted `ShouldAdvertiseForRouting` split is the next step in that lineage (display-strict vs routing-lenient).
- **FACT (JEV lineage):** `5faf736` introduces shadow advisor + offline eval as pure observer; `d56971d` repairs shadow outcome correlation and persistence ordering. The `config/c3b-local.yaml` → `c3c-local.yaml` progression records a concrete root cause: C3B's `agnesai.base_url=https://api.agnes-ai.com` returned 404/empty catalog; C3C corrects to `https://apihub.agnes-ai.com/v1` (matching the adapter default) and raises `dynamic_fallback.max_candidates` to 8 for multi-provider non-degeneracy.
- **FACT (deployment lineage):** `cf5ce63` stamps Fly builds with real versions; `36f625d` freezes EffNine infra v1.0 with Conductor as one of three verified containers; `ae0353e` adds `skip_providers` so unreachable-from-target endpoints don't stall probe passes.
- **OBSERVATION:** History lives in three places with different half-lives: **commits** (durable, greppable), **`docs/reports/`** (durable per-slice evidence, explicitly archival), and **the dirty tree + disposable configs** (fragile — the C3B/C3C root-cause notes exist only in uncommitted YAML comments and would vanish on `git clean`/`checkout`).

---

## 10. Remaining engineering risks/problems

1. **Undispositioned routing fix (highest priority).** **FACT:** `ListReachable`/`ShouldAdvertiseForRouting`/dedup exists only in the dirty tree with two untracked tests. **OBSERVATION:** If the tree is reset, the candidate-collapse knowledge is lost; if committed as-is, it ships without the full suite signal it deserves. Either way the current state is not landable.
2. **Agnes endpoint drift.** **FACT:** C3B vs C3C configs disagree on the Agnes base URL; C3C's comment claims `apihub` is correct per adapter default. **OBSERVATION:** If any committed doc/config still points at `api.agnes-ai.com`, shadow/probe data for that provider is silently empty.
3. **No routing-trace retention.** **FACT:** Documented as a known gap carried past P3; attempt retention exists (168h) but trace pruning does not. **OBSERVATION:** Long-running deploys accumulate `routing_traces` unboundedly.
4. **Docs-vs-implementation drift.** **FACT:** README still describes `/v1/models` as the merged prefixed catalog (code serves 10 virtuals there; merged raw catalog is `/api/models`); `docs/api.md` still calls `/api/usage/costs`, `/api/config`, `/api/config/reload` stubs (all implemented — `HANDOFF.md` is correct, `api.md` is stale); `config.example.yaml` still documents legacy `nvidia_nim.auto` (V2 doc says deprecated); `docs/PLAN.md` still says "P4 intentionally undefined" while P4.1–P4.5 shipped; `deployments/fly.toml` duplicates root `fly.toml` with divergent env. **OBSERVATION:** A newcomer trusting any single doc will misconfigure or misroute.
5. **Triple-path routing confusion.** **FACT:** Explicit default + scoring opt-in + residual NIM auto coexist; `routing.enabled: false` is the default while C3 configs set it true. **OBSERVATION:** Bug reports must always establish which path served the request (traces help, but only when the pipeline ran).
6. **Task/agent runtime without a usage verdict.** **FACT:** Fully wired; zero in-repo consumers demonstrated. **OBSERVATION:** Security/maintenance surface (tools execute fs/shell/git) carried without a retain-or-retire decision.
7. **Deployment verifiability gap.** **FACT:** Two deployment stories (Fly files vs EffNine Tunnel lock) with no currently reachable endpoint from this environment. **OBSERVATION:** Probe tuning, volume durability, and cold-start behavior cannot be validated here.
8. **Local secrets hygiene.** **FACT:** A real-credential `.env` exists in the checkout (provider keys redacted in this report, never quoted). **OBSERVATION:** `.env` is gitignored (good), but its presence alongside disposable configs means any pasted log/config excerpt risks key leakage; the report deliberately omits all key material.

---

## 11. Candidate real engineering tasks

All five arise from observed state, have objectively verifiable outcomes, and require historical context beyond any single file. None is executed here.

### TASK-1 — Land or revert the strict_healthy candidate-pool fix
- **TITLE:** Disposition the uncommitted `ListReachable` / `ShouldAdvertiseForRouting` / candidate-dedup delta
- **CURRENT PROBLEM:** **FACT:** `internal/catalog/catalog.go`, `internal/health/modelstatus.go`, `internal/router/{auto_service,selection,virtual_resolver}.go` are modified; `iface_check_test.go` and `multi_provider_candidates_test.go` are untracked. Under a strict display filter, never-probed providers collapse out of the auto/virtual candidate pool (single-provider degeneracy), and duplicate catalog entries can skew deterministic tie-breaks.
- **WHY IT IS REAL:** **OBSERVATION:** The tree cannot be landed or safely discarded as-is; `git stash`/`checkout` loses the fix, blind commit ships it without full verification. This is the exact "provider pool collapsing because of strict_healthy filtering" case.
- **EXPECTED ENGINEERING WORK:** Reproduce collapse with the untracked multi-provider test, verify display (`ShouldAdvertise`) vs routing (`ShouldAdvertiseForRouting`) semantics per state (healthy/degraded/unknown visible; recovering/unhealthy hidden), run `go test ./... -race`, `go vet`, `golangci-lint`, then either commit the delta with a report or revert with a recorded rationale.
- **VERIFICATION METHOD:** **FACT-grounded:** untracked regression tests pass; `go test ./... -race` green; `/api/models` display behavior unchanged while auto/virtual candidate sets span eligible providers (assertable via the new tests + `GET /api/models/status`).
- **RELEVANT HISTORICAL CONTEXT:** Health-filter lineage (`#5→#26`, `a67026d`), `unknown_as_reachable` doctrine, C3C non-degeneracy requirement in `config/c3c-local.yaml` comments.
- **WHY CODEBRO MIGHT HELP:** Facts locate every `ShouldAdvertise` consumer; recall/learn surface the flicker→revert→persist→batch lineage so the fixer doesn't re-tighten display filtering to "fix" routing; task continuity preserves the C3B→C3C root-cause across sessions.
- **WHY CODEBRO MIGHT NOT HELP:** The fix is small and fully characterized in the diff itself; plain `git diff` + `go test` may suffice for a careful engineer already holding the lineage in head.

### TASK-2 — Resolve the Agnes AI endpoint disagreement
- **TITLE:** Single-source the Agnes AI base URL and prove catalog health
- **CURRENT PROBLEM:** **FACT:** `config/c3b-local.yaml` uses `https://api.agnes-ai.com` (404/empty catalog per C3C comment); `config/c3c-local.yaml` uses `https://apihub.agnes-ai.com/v1`; adapter default agrees with C3C. Any lingering `api.agnes-ai.com` reference silently yields empty-provider behavior.
- **WHY IT IS REAL:** **OBSERVATION:** A one-character-class config drift that produces "provider registered but zero candidates" — the hardest failure mode to notice, already bitten once (C3B).
- **EXPECTED ENGINEERING WORK:** Grep all committed + example + doc references to Agnes hosts, correct to the adapter-verified host, add/extend a provider test asserting `ListModels` path against the canonical host, run mock-upstream probe pass.
- **VERIFICATION METHOD:** `grep -rn "agnes-ai.com"` clean except the intentional C3B historical note; provider + catalog + health tests green; `/api/models` shows Agnes entries with the corrected host.
- **RELEVANT HISTORICAL CONTEXT:** C3B→C3C comment trail (exists only in disposable configs — fragile), `8b6925e` Cloudflare account-ID precedent for host-extraction bugs.
- **WHY CODEBRO MIGHT HELP:** Memory persisting "C3B host wrong, C3C host right + why" prevents the next disposable config from reintroducing the bad host; facts find every reference including docs.
- **WHY CODEBRO MIGHT NOT HELP:** Single-grep fix; value is purely in not-forgetting, which a committed comment or doc line achieves equally well.

### TASK-3 — Close the routing-trace retention gap
- **TITLE:** Bound `routing_traces` growth (retention/pruning policy)
- **CURRENT PROBLEM:** **FACT:** Attempt persistence has 168h batched pruning (`P4.4.4`); routing traces have persistence (`P3.15`) and query API (`P3.16`) but **no retention policy** — documented gap in `HANDOFF.md` and `PLAN.md`.
- **WHY IT IS REAL:** **OBSERVATION:** Any long-lived deploy (Fly volume or EffNine host) accumulates traces unboundedly; the failure mode is slow disk growth, not a crash — easy to defer until it matters.
- **EXPECTED ENGINEERING WORK:** Mirror the attempt-retention pattern (startup sweep + hourly batched delete, config `routing.traces.retention`, failures logged never fatal), add tests for pruning + query pagination stability under pruning.
- **VERIFICATION METHOD:** New retention tests; `go test ./... -race`; config-docs updated; `/api/routing/traces` behavior unchanged except bounded age.
- **RELEVANT HISTORICAL CONTEXT:** P4.4.4 retention design (1000-row batches, never-fatal), P3.14 immutable-trace contract (pruning must delete whole decisions, never mutate them).
- **WHY CODEBRO MIGHT HELP:** Brief surfaces the P4.4.4 precedent + P3.14 immutability constraint together, preventing a naive "UPDATE traces SET …" or unbounded `DELETE`; prior failure (P4.5 bucket double-scale bug) warns about time handling.
- **WHY CODEBRO MIGHT NOT HELP:** Well-trodden pattern copy; a competent engineer finds `attempt_persistence.go` by filename alone.

### TASK-4 — Reconcile the triple-path routing contract and its docs
- **TITLE:** One true routing-path matrix + doc corrections (`/v1/models`, NIM auto, P4 status)
- **CURRENT PROBLEM:** **FACT:** Three coexisting paths (explicit default, scoring opt-in, residual NIM auto) plus four concrete doc errors (§10.4). A newcomer cannot determine which path served a request or which doc to trust.
- **WHY IT IS REAL:** **OBSERVATION:** Every future routing bug report pays this confusion tax first; the evidence reports themselves warn that milestone reports are archival while `PLAN.md`/`api.md`/README drifted.
- **EXPECTED ENGINEERING WORK:** Build a path-matrix (request shape × `routing.enabled` × model kind → serving path), add contract tests pinning it, correct README (`/v1/models`), `api.md` (costs/config/reload), `config.example.yaml` (NIM auto deprecation note), `PLAN.md` (P4 done), and dedup `fly.toml` references.
- **VERIFICATION METHOD:** New contract tests green; `go test ./...`; docs diff reviewed; no behavior change (docs + tests only, plus deprecation log if NIM auto fires).
- **RELEVANT HISTORICAL CONTEXT:** ADR-0002 router-responsibility split, P3.4 contract cleanup, V2 migration phases (coexistence → opt-in → default → removal — currently stalled at coexistence/opt-in).
- **WHY CODEBRO MIGHT HELP:** This is the highest-context task: facts map path consumers, recall recovers why each path survives (compat guarantees), memory records the "which doc is canonical" verdict so the next agent doesn't re-derive it.
- **WHY CODEBRO MIGHT NOT HELP:** Doc work's value is in the writing, not the retrieval; CodeBro can't decide the retain-or-retire call for NIM auto — a human must.

### TASK-5 — Verify JEV shadow outcome correlation under failure
- **TITLE:** Harden shadow backfill against the repaired ordering bug
- **CURRENT PROBLEM:** **FACT:** `d56971d` fixed shadow outcome correlation and persistence ordering; the advisor remains async/bounded/detached with `not_configured` degradation. **OBSERVATION:** The exact interleavings that motivated the fix (concurrent backfill vs advisory save, outcome arriving before/after advisory) are the kind that regress silently.
- **WHY IT IS REAL:** **OBSERVATION:** Shadow correctness is invisible in production (observer never alters routing); only dedicated ordering/chaos tests catch regressions.
- **EXPECTED ENGINEERING WORK:** Add ordering-fault tests (backfill-before-advisory, advisory-before-backfill, concurrent both, JEV timeout/401/429/malformed each), assert `UpdateAdvisory` never clobbers outcome columns and vice versa, verify `/api/jev-shadow` reflects both orders identically.
- **VERIFICATION METHOD:** New `internal/jev/shadow` ordering tests (deterministic, no live JEV calls — fake transports per existing pattern); `go test -race ./internal/jev/...` green.
- **RELEVANT HISTORICAL CONTEXT:** `d56971d` diff, C1 isolation contract (bounded saves, 8-way concurrency, 5s save timeout), C2 offline eval harness expectations.
- **WHY CODEBRO MIGHT HELP:** Recall of the `d56971d` failure mode + the store's split-write contract (`Save` vs `UpdateAdvisory` vs `BackfillOutcome`) is precisely the context an agent needs to write the right fault tests instead of happy-path ones.
- **WHY CODEBRO MIGHT NOT HELP:** The package is small and well-commented; reading `advisor.go` + `store.go` directly may be faster than any retrieval layer.

---

## 12. CodeBro suitability

The suitability question is whether Conductor holds **session-transcending engineering knowledge** an agent would otherwise re-derive or lose. For each candidate, the five requiredBoxs:

1. **Strict_healthy pool collapse (uncommitted fix).**
   - Code/problem: `Catalog.List` applied the display filter to candidate construction; never-probed providers vanished under strict_healthy; duplicates skewed ties. Fix: `ListReachable` + `ShouldAdvertiseForRouting` + dedup (dirty tree).
   - History: health-filter lineage commits + `unknown_as_reachable` doctrine + C3C non-degeneracy note.
   - Where it lives: **uncommitted diff + two untracked test files + disposable YAML comments** — i.e., nowhere durable.
   - Discoverable without CodeBro? Partially (`git diff`/`git status` show it today, but `git clean -fd` or a fresh clone destroys the C3 notes forever).
   - CodeBro surfacing: facts (all `ShouldAdvertise` call sites), record_memory/remember (C3B-host-wrong verdict), task continuity (land-or-revert disposition).
   - Outcome change: **Plausibly yes** — prevents shipping the display/ routing conflation again or losing the fix entirely.

2. **Unprobed vs unhealthy distinction.**
   - Code/problem: `ShouldAdvertise` (display) vs `ShouldAdvertiseForRouting` (candidates); states healthy/degraded/unknown vs recovering/unhealthy; sub-threshold blips.
   - History: `#5→#26` flicker saga, `a67026d` batch-flush ordering, `unknown_as_reachable: true` default reasoning.
   - Where it lives: commits + `AGENTS.md` probe section + code comments; no single canonical page.
   - Discoverable without CodeBro? Yes but slowly (requires reading 6+ commits and the prober).
   - CodeBro surfacing: brief assembles the lineage + current default matrix in one call.
   - Outcome change: **Plausibly yes** — stops the recurring "tighten the filter to fix noise" regression.

3. **JEV shadow ordering.**
   - Code/problem: async advisory save vs outcome backfill racing; `d56971d` repair.
   - History: C1 report (isolation contract) + fix commit + eval harness.
   - Where it lives: commit diff + `advisor.go`/`store.go` comments + C1/C2 reports.
   - Discoverable without CodeBro? Yes for the careful reader; no for the hurried one (observer code looks trivially correct).
   - CodeBro surfacing: recall ("why does UpdateAdvisory avoid outcome columns?") returns the failure instead of requiring rediscovery.
   - Outcome change: **Maybe** — matters most when writing new fault tests (TASK-5).

4. **Legacy vs intelligent router behavior.**
   - Code/problem: which requests hit `Engine` vs `DecisionPipeline` vs NIM auto; `routing.enabled` default false vs C3 true.
   - History: ADR-0002, P3.4 cleanup, V2 migration phases, `HANDOFF.md` compat guarantees.
   - Where it lives: ADRs + reports + `main.go` wiring + code; mutually inconsistent docs (§10.4).
   - Discoverable without CodeBro? Painfully (must triangulate code vs four docs).
   - CodeBro surfacing: engineering_brief with target `internal/router` + memory recording "api.md stale, HANDOFF.md canonical for X".
   - Outcome change: **Plausibly yes** — picks the right file to fix on the first try.

5. **Provider readiness assumptions (NIM catalog, Agnes host, skip_providers).**
   - Code/problem: catalogs listing unreachable endpoints; wrong-host empty catalogs; deploy-target-unreachable endpoints stalling probes.
   - History: `ae0353e` skip list, C3B→C3C host correction, NIM `chat_template_kwargs` injection, `8b6925e` Cloudflare host bug.
   - Where it lives: adapter docstrings, C3 comments, reports, AGENTS.md.
   - Discoverable without CodeBro? Only by hitting the failure (as C3B did).
   - CodeBro surfacing: memory ("agnes canonical host is apihub; C3B host 404s") + facts (all base_url defaults).
   - Outcome change: **Yes for the next provider onboarding** — the exact mistake is already documented once, fragily.

---

## 13. Overengineering assessment

WITHOUT CODEBRO (normal OpenCode + git history + repo search + tests + OpenSandbox):

- Adequate for: single-file provider fixes, running `go test ./... -race`, verifying Fly config coherence, reading the (short) shadow package, landing the uncommitted diff *if the engineer already holds the health lineage in head*.
- Inadequate for: retaining the C3B/C3C root-cause notes past a `git clean`; keeping "which doc is canonical" straight across README/api.md/HANDOFF/PLAN/V2-doc; re-deriving why display and routing filters must differ three months from now; connecting a future candidate-collapse symptom to the `a67026d`/strict_healthy lineage instead of re-tightening the wrong filter.

WITH CODEBRO (persistent project context + intent + session history + engineering facts + learning + task continuity):

- Pays for: cross-session retention of the five §12 items; one-call briefs binding code + lineage + risks before touching `internal/router`/`internal/health`/`internal/jev/shadow`; task-disposition tracking for the dirty tree; learning that converts "hit twice" (Agnes host, filter tightening) into durable memory.
- Costs: a running MCP layer, indexed state, and discipline (record/confirm/reject) for a repo the user otherwise touches rarely; risk of persisting judgments (e.g., "task runtime is dead") that later become stale.

Verdict: **the additional context solves real, demonstrated problems** — fragile uncommitted knowledge, multi-source lineage no single file holds, and repeat-offense config/filter mistakes. It is not machinery for its own sake. The caveat is scope discipline: CodeBro should be pointed at routing/health/JEV/retention/docs-verdicts, not at re-indexing the entire 19-provider surface or at manufacturing tasks where §11 already supplies real ones. Under that scoping, use is proportionate, not overengineered.

---

## 14. Recommended next step

1. **Disposition the dirty tree first** (TASK-1): run the untracked regression tests + full `go test ./... -race`, then commit the `ListReachable` split with a short report or revert with rationale — before any other work, because every other task's evidence depends on which filter semantics are canonical.
2. Then, in order: TASK-2 (Agnes host single-sourcing, same verification run), TASK-5 (shadow ordering fault tests, cheap and high-signal), TASK-3 (trace retention, mirrors a proven pattern), TASK-4 (routing-path matrix + doc corrections, human verdict on NIM auto and the task runtime).
3. Keep Fly read-only until a reachable credential/app is established; do not redeploy to "check usage" — usage must be demonstrated by traffic evidence, not by a fresh deploy.
4. If CodeBro is evaluated here, seed it with the five §12 memories **after** TASK-1 lands, so persisted knowledge records the landed semantics rather than the dirty-tree draft.

---

## 15. Evidence limitations

- **FACT:** No live Fly telemetry, logs, metrics, or traffic artifacts were accessible: current Fly credentials show zero apps; both candidate hostnames are unreachable; the EffNine Tunnel endpoint from the 2026-08-30 lock is DNS-unresolvable from here.
- **FACT:** OpenCode's Go/package internals were not inspected in this phase; all OpenCode-overlap statements are INFERENCE from Conductor-side evidence plus the task-provided context, not from OpenCode source.
- **FACT:** Provider "stub vs implemented" status beyond HANDOFF.md's list was spot-checked (opencode/nvidia_nim/nous_portal fully implemented; `grep ErrNotImplemented` empty) but not exhaustively audited per adapter; per-provider chat/embeddings/streaming/pricing claims rest on README + adapter tests, not live calls (no upstream keys in this environment — consistent with HANDOFF.md's own "no live-provider E2E" caveat).
- **FACT:** Test results reported are `go test ./...` as-run here (all packages ok, integrationtest 7.4s); `-race` full-suite and `golangci-lint` were not re-run in this read-only phase.
- **FACT:** Secret material (`.env` provider keys, gateway key) was observed only to establish auto-enable behavior and was deliberately excluded from this report; key values are never quoted.
- **OBSERVATION:** The dirty tree means any finding referencing `ListReachable`/`ShouldAdvertiseForRouting`/dedup describes the **working tree**, not any commit; a fresh clone at `44d4fac` will not contain that fix.
- **INFERENCE:** Absence of usage evidence is not proof of non-use (an operator could hold separate Fly credentials or query from another network). The classification in §7 is therefore stated as the best-supported reading of *accessible* evidence, revisable on new traffic/auth evidence.

---

## FINAL DECISION

`CONDUCTOR IS A GOOD CODEBRO TESTBED`
