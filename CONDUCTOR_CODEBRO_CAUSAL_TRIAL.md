# CONDUCTOR CodeBro Causal Trial — SINGLE ON/OFF Experiment

> Measurement only. No fix implemented. No commit. No push. No other candidate task run.

**Verdict: `NO MATERIAL CODEBRO CONTRIBUTION OBSERVED`**

Both arms independently reached the same CORRECT engineering conclusion through
essentially equivalent native investigation. CodeBro supplied zero live
retrievals; its pre-existing prompt context was orthogonal to this task and was
not relied upon by the ON agent (verified by the ON agent's own report).

---

## 1. Starting state

Recorded BEFORE either arm ran. Working tree was not modified at any point
(verified before OFF, between arms, and after ON — identical hashes).

- **git HEAD:** `44d4facd9acb33ab78df2b8fe0bfbeb6ba502dba`
  (`44d4fac chore: gitignore the disposable C3B local config`, branch `main`,
  ahead of `origin/main` by 5 commits)
- **Recent log:** `44d4fac` (HEAD), `ae0353e feat(health): add provider skip list
  for model probing`, `d56971d fix(jev): ...`, `5faf736 feat(jev): Phase C1/C2 ...`,
  `36f625d chore: freeze EffNine Infrastructure v1.0 baseline`
- **git status (porcelain v1, identical at all three checkpoints):**
  ```
  M .gitignore
  M internal/catalog/catalog.go
  M internal/health/modelstatus.go
  M internal/router/auto_service.go
  M internal/router/selection.go
  M internal/router/virtual_resolver.go
  ?? CONDUCTOR_ENGINEERING_ASSESSMENT.md
  ?? internal/catalog/iface_check_test.go
  ?? internal/router/multi_provider_candidates_test.go
  ?? opencode.jsonc
  ```
  That is 6 modified tracked files + 4 untracked files, of which 2 are the
  regression-test files named in the task brief
  (`internal/catalog/iface_check_test.go`,
  `internal/router/multi_provider_candidates_test.go`). The other two untracked
  files (`CONDUCTOR_ENGINEERING_ASSESSMENT.md`, `opencode.jsonc`) are
  pre-existing experiment/harness artifacts and were left untouched.
- **File hashes (sha256, start == between-arms == end):**
  ```
  8c5674e11c0b5c430fcb6cb96aff449ef245fe2512d704a1fbe2a24bffb1dcbc  .gitignore
  0f395f6fa6712ff69e8d702b31fbaac60eeffb2054c3be0d12faa6423f8c5059  internal/catalog/catalog.go
  8a822db0650a8b1c33e12257eab9b0768e7db297b2a480b805c7c964ff33bd13  internal/health/modelstatus.go
  0af4cbebe8ed62fa05364b871b1c2bf72e633882b4d4b041cb1eda872f224eec  internal/router/auto_service.go
  435b64376d3ac36835e7539977e469d85dbc39b15dd1440a597be8dab3264f84  internal/router/selection.go
  b0f632e0c81c05a68189fff2c817e74041b29c3b7a10cbbf66d46e9331927525  internal/router/virtual_resolver.go
  6edcab07df5da63440e7405fd25e7c567654a3152f17bb33bfe0f367275dffe0  CONDUCTOR_ENGINEERING_ASSESSMENT.md
  13a53ec8865d10f9543c46c0df7f85f593dfd146eeb89644e042d8ee75ee1cb8  internal/catalog/iface_check_test.go
  f5fd318d06d096afa2de6d2082414be919921160ba77e96dc9bbe1efc8e9075e  internal/router/multi_provider_candidates_test.go
  c3f8c8f76d858857481f7b6af19a1fcab18becb67d16650d421687556158fa1c  opencode.jsonc
  ```
- **Model / provider:**
  - Orchestrator runtime: `muse-spark-1.3-contributor` (per session; `opencode-go/muse-spark-1.3-contributor`).
  - Repo default configured model: `agnes/agnes-3.0-flash` (from
    `~/.config/opencode` global config). Both subagent arms used the
    `general` subagent type inheriting the session model; no model switch
    between arms.
- **OpenCode version:** `1.18.31` (`opencode --version`).
- **OFF harness:** `opencode.jsonc` in repo root contains
  `{"$schema": "https://opencode.ai/config.json", "mcp": {"codebro": {"enabled": false}}}`.
  No `.opencode/` directory exists. This is the pre-existing OFF configuration
  and was used as-is (not modified).
- **CodeBro version:** `codebro 1.1.0` (`codebro --version`). Repo `.codebro/`
  contains `architecture.json`, `engineering_decisions.json`,
  `engineering_memory.json`, `facts.json`, etc. (last updated 2026-09-17).
  No CodeBro memory was seeded, written, or modified during this trial.
- **OpenSandbox configuration:** `curl -sf http://localhost:8080/health` →
  `{"status":"healthy"}`; Docker server `29.1.3`. Subagents ran `go test`
  via local toolchain (read-only test execution; no files written). No
  sandbox-created artifacts remain in the tree (verified via status).
- **Eval directory:** no dedicated causal-trial harness directory exists.
  `tests/c2eval` is the unrelated Jev offline-eval harness — deliberately NOT
  repurposed. Per the brief ("if the existing repository harness provides
  one"), no new benchmark framework was created; raw observations are
  preserved inline in §§4/8 below.

Starting state was recorded without modifying anything.

## 2. Exact task prompt

Used VERBATIM for both arms (no hints added, no bug name disclosed):

> "Review the current provider routing and reachability behavior in Conductor.
>
> Determine whether the current implementation is behaving according to the intended routing semantics.
>
> Investigate the relevant code paths, configuration, tests, and repository history as needed.
>
> Pay particular attention to whether providers can incorrectly disappear from routing candidates because of their current health/readiness state.
>
> Do not assume the current implementation or the current uncommitted changes are correct.
>
> Produce an engineering conclusion supported by evidence.
>
> If a correction is necessary, identify the minimal correction and explain why.
>
> If the current implementation should remain unchanged, explain why.
>
> Validate the conclusion with the strongest available tests or reproducible checks.
>
> Do not make code changes unless they are necessary to perform the requested validation. If you believe a code change is required, stop before modifying files and report the proposed change first."

Orchestrator-level guardrails added OUTSIDE the prompt (same for both arms,
modulo CodeBro): absolute read-only constraint (no create/modify/delete,
no commit/reset/clean/push), work dir
`/home/afnan/projects/active/conductor`, structured-report requirement.
OFF additionally forbade all CodeBro tools/context including `.codebro/`.
ON permitted natural use of pre-existing CodeBro context and forbade seeding
new memories. Neither arm was told any suspected bug name, expected fix,
which files contain it, or what CodeBro should retrieve.

## 3. OFF methodology

- Arm: single `general` subagent, CONDITION OFF.
- Tools allowed: native repo tools only (Read/Glob/Grep/Bash read-only git +
  `go test`). CodeBro tools/MCP/context and `.codebro/` explicitly forbidden.
- Steps (per OFF self-report): `git status`/`diff --stat`/`log` baseline →
  full `git diff HEAD` of 5 modified files → read 5 modified sources + 2
  untracked tests → `git show HEAD:<path>` for pre-fix behavior → lineage
  search (`git log -S`) → production wiring (`cmd/conductor/main.go`,
  `internal/config/config.go`), handler paths, legacy automode selector →
  docs/config grep → read-only `go test` runs (no new files).
- Timing: start `2026-09-19T09:43:42Z` (epoch 1789811022), end
  `2026-09-19T09:47:50Z` (epoch 1789811270) → **248 s wall (~4m08s)**.
- Equivalence: ran on the recorded starting tree; status/HEAD/hashes
  re-verified unchanged immediately after.

## 4. OFF raw observations

(Char-for-char structure preserved; condensed only for whitespace. Full
subagent report retained in session transcript under task
`ses_f46f2e655ffe0AQ7Gz7vrGtuJR`.)

1. INVESTIGATION_PATH: status/porcelain, diff stat, log -15 → full diff of 5
   files → read 5 sources + 2 untracked tests → `git show HEAD` for catalog
   List, ShouldAdvertise, auto/virtual candidate construction → lineage
   `git log -S strictHealthy/strict_healthy` + `git show 746bf92` → wiring
   `cmd/conductor/main.go:170-269`, config defaults, `HandleDashboardModels`,
   dynamic fallback, legacy selector → docs/config grep for strict/hide intent
   → read-only tests on catalog/health/router/handler/automode + targeted
   `-run` for strict/candidate tests.
2. FILES_INSPECTED (~17 reads): catalog.go (List/ListAll/ListReachable/
   filterStrictHealthy + fix), modelstatus.go (ShouldAdvertise vs new
   ShouldAdvertiseForRouting), auto_service.go, virtual_resolver.go,
   selection.go (candidateKey dedup), both untracked test files,
   CONDUCTOR_ENGINEERING_ASSESSMENT.md (corroboration only, independently
   verified), config.go (offsets 330/725; strict_healthy=true,
   hide_unreachable=true defaults), catalog_test.go:360-411,
   modelstatus_test.go:305-353, handler/models.go (List vs ListAll),
   handler/dynamic_fallback.go (RankedEligible), main.go wiring,
   modelprobe.go:240-276 (batch-flush + skipped-provider readiness),
   automode/selector.go:110-169 (ListAll path).
3. GIT_HISTORY_INSPECTED: HEAD `44d4fac` (trivial); `ae0353e` skip list
   ("still registered, routable"); `a67026d` flush-before-ready (comment:
   "With strict_healthy that combination hides every not-yet-applied model");
   `746bf92` (2026-08-16) introduced StrictHealthy on store + Catalog (List
   AND ListAll, default true) inside a model-IDs/lint commit with no routing
   discussion; HEAD auto/virtual both called `catalog.List(ctx)`; HEAD List
   applied filterStrictHealthy then ShouldAdvertise; ListAll same;
   `git log -S ListReachable` empty (fix exists only in working tree).
4. TESTS_EXECUTED (all exit 0, no files created):
   - `go test ./internal/catalog/... ./internal/health/... ./internal/router/...` → ok (cached)
   - `go test -count=1 ./internal/catalog/... ./internal/health/...` → ok
   - targeted `-run "TestCandidateConstruction_|TestCatalogStrictHealthy|TestShouldAdvertiseStrict"` → all PASS (8/8 candidate-construction, strict catalog/store tests)
   - `go test -count=1 ./internal/router/... ./internal/handler/... ./internal/automode/...` → ok
5. EVIDENCE (with file:line): HEAD routing consumed display filter
   (`git show HEAD:auto_service.go` / `virtual_resolver.go` → `catalog.List`);
   HEAD List pre-gate `catalog.go:233-234` + ListAll `:302-303` + filter
   `395-406` (keep only `ShouldAdvertise` pass); strict hides unprobed
   `modelstatus.go:405-412` + `:419` (+ committed tests
   modelstatus_test.go:327-329, catalog_test.go:397-400); defaults
   `config.go:739` strict_healthy=true / `:715` hide_unreachable=true +
   wiring `main.go:197/210/249-269` (same catalog feeds resolvers) ⇒
   registered-but-unprobed/degraded providers excluded from
   auto/virtual/RankedEligible/dynamic-fallback pools under defaults, while
   explicit routing (`selection.go:230-240`, registry+SupportsModel, no health
   gate) unaffected; display-vs-routing split corroborated by handler
   (`models.go:43-45` List vs ListAll) vs `dynamic_fallback.go:114`
   (RankedEligible), docs ("degraded (still advertised)",
   "unknown_as_reachable default true"), skip comment ("still registered,
   routable"), and docs omitting any strict_healthy row; caveats found in fix:
   ShouldAdvertiseForRouting ignores `unknown_as_reachable=false` +
   providerFilterReady for unprobed (operator override lost), and
   `virtual_resolver.go:366-372` duplicates the `len(entries)==0` guard.
6. HISTORICAL_CONTEXT_DISCOVERED (all via git/source, no CodeBro): 746bf92
   display-control with incidental routing side-effect; a67026d prior
   strict-induced collapse symptom; ae0353e lenient-routing doctrine;
   unknown_as_reachable/degraded doctrine; no ADR for display-vs-routing split.
7. UNNECESSARY_EXPLORATION: legacy automode (deprecated), explicit
   SelectBestProvider/SelectFromRoutes (ruled out — no health gate),
   /v1/models virtual-only (ruled out), full docs-drift audit (out of scope).
8. TOOL_CALL_SUMMARY (self-reported): reads ~17, greps ~7, globs 1, bash ~8.
9. SELF_ASSESSED_DIFFICULTY: 3/5 — localized defect, but verdict required
   separating display intent from routing intent across six subsystems.

## 5. OFF conclusion

(OFF agent verdict — **CORRECT**.)

> At HEAD, catalog-backed routing (auto, all virtual models, dynamic fallback
> via RankedEligible) does NOT behave per intended semantics under defaults:
> resolvers built candidates from `Catalog.List`, which applies the
> strict_healthy pre-gate (default true), silently excluding
> unprobed/unknown/degraded providers that are not confirmed-unhealthy.
> Only confirmed-unhealthy/recovering should be excluded from routing;
> unprobed/unknown must err toward availability. Explicit routing unaffected.
> The working-tree direction (routing view distinct from strict display view)
> is correct in principle; display (`/api/models`) should remain strict.

Proposed action (NOT implemented): land working-tree approach with amendments —
keep `RoutingReachabilityFilter` + `ListReachable` / `ShouldAdvertiseForRouting`
+ dedup; add guarded branch honoring explicit `unknown_as_reachable=false`
(or document override); remove duplicated empty-check; keep both untracked
tests and add one real-store strict test (current
`ReachesThroughStrictHealthyFilter` uses a fake filter without strict mode and
does not exercise the defect path).

## 6. ON methodology

- Arm: single `general` subagent, CONDITION ON, on the SAME tree state
  (hashes re-verified identical before start: all 10 sha256 match §1).
- CodeBro integration enabled exactly as configured. No manual seeding, no
  artificial memories, no pre-run hint recorded. Agent instructed to consult
  existing CodeBro context naturally, then verify every retrieved claim
  against source/git/tests.
- Important environment fact (reported by the ON agent itself): **no live
  `codebro_*` MCP tools were exposed in the subagent toolset**, so no live
  queries were possible; the available CodeBro context was the pre-existing
  prompt-level context (workspace facts, intents, decisions, engineering
  memory). This matches the current integration as configured — there is no
  separate CodeBro MCP server wired for subagents in this setup.
- Same native method as OFF plus the CodeBro-first step; same read-only +
  `go test` allowance; no new files.
- Timing: start `2026-09-19T09:48:10Z` (epoch 1789811290), end
  `2026-09-19T09:51:28Z` (epoch 1789811488) → **198 s wall (~3m18s)**.

## 7. ON CodeBro retrieval evidence

- **Live CodeBro tool calls: 0.** None available; none attempted beyond
  checking. No records were created, no writes to `.codebro/` occurred
  (verified via post-run status: only the 6 pre-existing modifications).
- **Pre-existing CodeBro context consumed read-only** (as present in the
  session prompt):
  - Workspace facts: `conductor · go · facts: stale`; counts
    (build_targets=1, dependencies=39, modules=437, packages=40,
    references=92, relationships=3107, symbols=4151, tests=2336);
    execution state `unverified`.
  - Intents: unified-memory, running-started, passed-repository,
    backend-duration.
  - Decisions (accepted): Subsystem Boundaries, Router Responsibilities
    Split, Runtime Subsystem, Event Bus, Explainability.
  - Engineering memory: `architecture:capability-contract-foundation`
    (conf 0.95, classifier `internal/router/classifier.go`),
    `p3.2:model-capability-metadata-foundation` (conf 0.95),
    `release:v1.0.0-shipped` (conf 0.95).
- **Relevance (ON agent's own assessment, verified by orchestrator): low /
  none for this task.** The routing/reachability question is governed by
  `catalog`/`health`/`router` + strict_healthy history, not by the P3
  capability-contract memories. The ON agent verified only in passing that a
  classifier/scoring core exists and explicitly did NOT rely on the P3.1/P3.2
  claims. CodeBro supplied no reachability semantics, no strict_healthy
  lineage, and no display-vs-routing distinction.
- **Verification:** every historical claim in the ON conclusion is cited to
  git/source/tests (§8), not to CodeBro. The ON report §9 explicitly
  partitions provenance: all history items from git/source; CodeBro items
  unverified and not relied upon.

## 8. ON raw observations

(Full subagent report retained in session transcript under task
`ses_f46eed3acffeSlNwfJpnqczyZc`. Key deltas vs OFF noted in §10.)

1. INVESTIGATION_PATH: CodeBro prompt-context review → root list,
   status/diff-stat/log → full `git diff` → read all 5 sources + both
   untracked tests → grep strict/ListReachable/wiring/automode →
   `git show HEAD` comparisons → config defaults, main wiring
   (`main.go:170-313`), modelprobe, automode selector, handler
   models/dynamic_fallback/chat/embeddings → history `-S` + `746bf92`,
   `ae0353e`, `cc74ccb`/`a67026d` → docs + committed strict tests + skim of
   untracked assessment (evidence only) → `go test` suites + targeted runs +
   handler suite.
2. FILES_INSPECTED (~14 reads): same core five + both untracked tests +
   config/main/modelprobe/automode/handler files + committed strict tests +
   `docs/configuration.md`, `docs/providers.md`, `docs/api.md` (notes config
   table omits strict_healthy) + assessment (third-party evidence only).
3. GIT_HISTORY_INSPECTED: same working-tree diff + log -15 + HEAD comparisons
   + `-S strict_healthy/StrictHealthy/filterStrictHealthy` (only `746bf92`,
   `a67026d`) + `746bf92` strict introduction ("only confirmed healthy
   advertised", default true) + `ae0353e` skip ("still registered and
   routable") + grep-found `a67026d` batch-flush fix, `cc74ccb`
   per-provider filtering, `3d4a431/#26` backoff/atomic states,
   `f89cbd0`/`e38d8f7` dynamic-fallback history.
4. TESTS_EXECUTED (all exit 0): router/catalog/health (cached, then
   `-count=1`: router 0.729s, catalog 0.004s, health 0.288s) + targeted
   `TestCandidateConstruction_*` (8/8) + `TestCatalogStrictHealthy` +
   `TestModelStatusStore_SatisfiesRoutingReachabilityFilter` +
   `TestShouldAdvertiseStrictHealthy*` + handler suite (0.953s).
5. EVIDENCE: same HEAD-defect chain as OFF (default strict + ShouldAdvertise
   + `catalog.List` call sites + collapsing List/ListAll per committed
   tests) + same working-tree routing view
   (`catalog.go:257-283`, `modelstatus.go:456-484`,
   `auto_service.go:491`, `virtual_resolver.go:361`) + same caveats
   (fake-filter test gap at `multi_provider_candidates_test.go:30-62` —
   passes at HEAD too, never sets strict/real store; explicit routing
   unaffected `selection.go:200-240/391-495`; legacy `automode/selector.go:130`
   still `ListAll` under strict — unfixed second routing path;
   `virtual_resolver.go:366-372` dup guard; routing ignores
   `unknown_as_reachable=false` for Unknown by design).
6. UNNECESSARY_EXPLORATION: assessment skim (Fly/DB/JEV — irrelevant),
   broad router health grep (100+ weight/telemetry hits), automode
   cost/latency scoring, handler dashboard/prometheus surface, `cmd/**/*.go`
   + `internal/app/` misses. ON agent notes CodeBro neither prevented nor
   caused any of it.
7. TOOL_CALL_SUMMARY (self-reported): reads ~14, greps ~9, globs ~2, bash ~8,
   CodeBro calls 0.
8. SELF_ASSESSED_DIFFICULTY: 3/5 — small cross-layer interaction; validation
   required correlating defaults/wiring/two filter views/three routing paths/
   history/test gap.

## 9. ON conclusion

(ON agent verdict — **CORRECT**, materially identical to OFF.)

> At HEAD, providers can incorrectly disappear from auto/virtual routing
> candidates due to health/readiness state under default strict_healthy=true:
> never-probed providers (fresh/scoped/skipped/cold-start) are hidden by
> ShouldAdvertise/filterStrictHealthy and excluded from
> AutoResolver/VirtualResolver + dynamic-fallback tail despite not being
> confirmed-unhealthy. Breaker-open and confirmed recovering/unhealthy
> exclusion is intended; unprobed-unknown exclusion is not — it contradicts
> unknown_as_reachable err-toward-availability, skip "still routable", and
> per-provider-ready design. Working-tree direction (distinct routing view:
> keep unprobed/unknown + degraded, hide confirmed recovering/unhealthy, keep
> breaker/capability filters) is correct in principle for auto/virtual.

Proposed action (NOT implemented): same as OFF (keep ListReachable +
ShouldAdvertiseForRouting + dedup; decide/document unknown_as_reachable=false
handling; remove dup guard; add a real-store strict test reworking
`ReachesThroughStrictHealthyFilter`) PLUS explicitly address or defer
`automode/selector.go:130` (second strict-gated routing path when NIM
AutoMode enabled). "No change" explicitly not recommended.

## 10. Side-by-side comparison

| Dimension | OFF | ON |
|---|---|---|
| Conclusion | HEAD defect real; working-tree direction correct with amendments; "no change" rejected | Same HEAD defect; same direction correct with same amendments + legacy-automode note |
| Correctness | CORRECT | CORRECT |
| Evidence quality | High: code:line + defaults + wiring + committed strict tests + history + test runs | High and equivalent, marginally broader (cc74ccb, backoff refs, chat/embeddings, providers/api docs) |
| Investigation path | status/diff/log → sources → HEAD compare → lineage → wiring/config/handlers → docs → tests | Identical native path, preceded by CodeBro context review that contributed nothing |
| Wall time | 248 s (09:43:42Z→09:47:50Z) | 198 s (09:48:10Z→09:51:28Z); −50 s nominal |
| Tool usage (self-reported) | ~17 reads, ~7 greps, 1 glob, ~8 bash | ~14 reads, ~9 greps, ~2 globs, ~8 bash, 0 CodeBro calls |
| Historical context | 746bf92, a67026d, ae0353e, unknown_as_reachable doctrine — all found independently in git/source | Same items found independently in git/source; CodeBro supplied none of them |
| Test validation | Same suites, all PASS, same test-gap + dup-guard + override caveats | Same suites/results, same caveats, plus legacy-automode gap |
| Unnecessary exploration | Legacy automode, explicit paths, /v1/models, docs drift | Assessment skim, broad health grep, handler surface, glob misses — CodeBro neither prevented nor caused |
| CodeBro retrievals | N/A (forbidden) | 0 live calls; prompt context only, orthogonal, not relied upon |
| Independent verification | N/A | All CodeBro-adjacent claims either not used or verified against source; history partitioned by provenance |

## 11. Causal CodeBro analysis

Against the brief's "what counts as causal value" (requires evidence CodeBro
_materially affected_ the engineering process — not mere calls, text volume,
token counts, or confirmation of already-known facts):

1. **Did CodeBro surface a previous decision the agent would otherwise not
   have found?** No. The decisive history (746bf92 strict introduction,
   a67026d flush fix, ae0353e skip doctrine, unknown_as_reachable intent) was
   found by BOTH arms via `git log -S` / `git show` / docs. CodeBro's stored
   decisions (subsystem boundaries, router split, runtime, event bus,
   explainability) and memories (P3.1/P3.2 classifier, v1.0.0-shipped) are
   orthogonal to reachability and were not used for the verdict.
2. **Did CodeBro cause investigation of a previously known failure mode?** No.
   Both arms investigated the health-gated disappearance mode from the task
   wording + `git diff` alone.
3. **Did CodeBro prevent accepting a plausible-but-historically-incorrect
   interpretation?** No. Both arms independently separated display intent
   (committed strict tests must keep passing) from routing intent
   (defaults/wiring/docs) without CodeBro.
4. **Did CodeBro reduce redundant exploration while preserving correctness?**
   No evidence. Paths and self-reported tool counts are essentially equivalent;
   ON's unnecessary exploration is honestly reported as unprevented by CodeBro.
5. **Did CodeBro cause a materially different, verifiable conclusion/action?**
   No. Conclusions and proposed amendments match (ON adds only the
   legacy-automode observation, which is reachable natively — OFF had already
   read the same selector file and ruled it deprecated; the difference is
   emphasis, not CodeBro causation).
6. **Did CodeBro recover project intent not obvious from the tree?** No. Intent
   (err toward availability, "still routable", degraded-still-advertised) came
   from docs/comments/history in both arms.

A CodeBro retrieval merely confirming something already known would not count
— here there was not even a relevant retrieval to confirm. Both agents
reaching the same correct conclusion is explicitly NOT a CodeBro win per the
brief.

**Contribution classification: `NO MATERIAL CONTRIBUTION`.**
(Not decision-changing, not investigation-shortening on evidence, not
context-confirming for this task, and not negative/distracting — the ON agent
correctly ignored orthogonal context rather than being misled by it.)

## 12. Overhead analysis

4. **Did CodeBro introduce measurable overhead?** No measurable overhead
   observed: 0 live calls, no failed lookups, no distracting context injected
   into reasoning (ON explicitly set the P3/release memories aside). Nominal
   wall-time delta favors ON (−50 s), but see §13 — this cannot be attributed
   to CodeBro with zero relevant retrievals.
5. **Was any overhead justified by benefit?** Moot: no overhead and no benefit.
   The CodeBro-context review step cost at most a few prompt tokens and did
   not perturb the investigation.

## 13. Confounders

- **Sequential same-tree runs.** ON ran second on the identical tree with warm
  caches (go test caching, agent-visible transcript patterns excluded by
  isolation but OS page cache shared). The −50 s delta is confounded and
  cannot support an "investigation-shortening" claim — especially with zero
  relevant retrievals.
- **No live CodeBro MCP for subagents.** "ON" in this setup means pre-existing
  prompt context only. A richer CodeBro tool surface (queryable decisions with
  IDs, reachability-specific memories) was not present to be tested. This
  limits generalizability but accurately reflects "the current CodeBro
  integration exactly as configured."
- **Shared untracked assessment file.** Both arms could read
  `CONDUCTOR_ENGINEERING_ASSESSMENT.md` (part of the starting tree). Both
  report treating it as corroboration only with independent verification. Any
  shortening effect applied equally to both arms, not differentially to ON.
- **Self-reported tool counts, not instrumented.** Read/grep/bash tallies come
  from the agents themselves; token counts and fine-grained timings were not
  instrumented. Comparison is qualitative.
- **Same underlying model for both arms.** OFF vs ON differ only in
  CodeBro access, which is the intended manipulation — but model nondeterminism
  still contributes noise to time/path emphasis (e.g. legacy-automode note).
- **Task well-served by git history.** `git log -S`, `git show`, defaults,
  wiring, and committed tests fully determined the answer. Tasks whose intent
  lives ONLY in lost oral history would be a sterner CodeBro test; this one,
  as executed, was not.

## 14. Final classification

- **OFF arm outcome: CORRECT** — HEAD defect identified with code/default/
  wiring/test/history evidence; minimal correction proposed (not implemented);
  caveats (fake-filter test gap, dup guard, explicit-override semantics)
  documented; validated with passing suites.
- **ON arm outcome: CORRECT** — same verdict with equivalent evidence plus
  legacy-automode second-path note; same minimal correction (not implemented).
- **CodeBro contribution: NO MATERIAL CONTRIBUTION.**
- **Evidence standard (§verbatim): `NO MATERIAL CODEBRO CONTRIBUTION OBSERVED`**
  — ON and OFF reached the same correct conclusion through essentially
  equivalent investigation; no CodeBro context materially affected process or
  outcome on this task.

## 15. Evidence limitations

- Causality rests on two single-shot subagent runs (n=1 per arm), not repeated
  trials; model nondeterminism unquantified.
- No token-level or per-step timing instrumentation; time/tool comparisons are
  coarse and confounded (§13).
- Raw reports are subagent self-reports; orchestrator verified only tree
  invariance (status/HEAD/hashes), test plausibility (commands + exit codes +
  named tests cross-checked between arms), and CodeBro provenance (zero live
  calls + orthogonal prompt context).
- The defect's ground truth ("intended routing semantics") is reconstructed
  from docs/comments/history/defaults, not from a single authoritative spec —
  but both arms converged on the same reconstruction with the same caveats,
  raising confidence.
- CodeBro index was `facts: stale` per prompt context; a fresh index would not
  plausibly have added reachability-specific memories that do not exist in
  `.codebro/` today, but this was not experimentally varied.

## 16. Recommendation for whether to proceed with the remaining four candidate tasks

**Yes — proceed, but redesign the next trials to be capable of detecting value:**

1. This task definitionally favored native tools: the answer lay in the
   current diff + `git log -S` + defaults + committed tests. Ret running
   identical well-git-served tasks will keep returning ties. Select remaining
   candidates where intent is genuinely at risk of being lost to git (e.g.
   rejected alternatives, cross-cutting invariants, deployment/ops lessons not
   encoded in code or ADRs) AND where `.codebro/` actually holds relevant
   records — verify relevance BEFORE running, without seeding.
2. Fix instrumentation: instrument wall time, token usage, and tool-call
   counts at the harness level instead of relying on self-reports; randomize
   arm order; consider fresh-tree copies in `/tmp` per arm if the harness
   supports it (this trial used sequential same-tree runs with hash-verified
   equivalence — document that choice explicitly next time).
3. Ensure ON actually has live CodeBro query tools if the claim under test is
   "CodeBro retrieval helps." If subagents cannot call CodeBro, the trial
   measures prompt-context only — say so up front, as done here.
4. Quarantine the untracked assessment file next time (move out of tree or
   forbid reading it) if it overlaps the task — both arms behaved well here,
   but it is a needless confounder.
5. Do NOT reinterpret this tie as a general claim about CodeBro. Per the
   brief: this result says nothing beyond "on THIS routing/reachability
   maintenance problem, with THIS CodeBro content, persistent context added no
   observable causal value."

---

### Appendix — Safety check (pre-finish verification)

- [x] Original 6 modified files still modified, byte-identical hashes (§1).
- [x] Original 4 untracked files unchanged, byte-identical hashes (§1).
- [x] No source code modified (no writes; `git status` identical at all checkpoints).
- [x] No tests modified or added (untracked test files executed as-is, not edited).
- [x] No configuration modified (`opencode.jsonc`, `config.yaml` untouched).
- [x] No commit created (`HEAD 44d4fac` throughout; `git log --oneline -3` unchanged).
- [x] No push occurred (no network git operations issued).
- [x] ON and OFF started from equivalent states (hash-verified identical tree; §§3/6).
- [x] Exact task prompt used in both arms (§2, quoted verbatim).
- [x] No CodeBro memory artificially seeded (no writes to `.codebro/`; ON reports 0 calls).
- [x] No hidden answer supplied (bug name, fix location, expected retrieval never disclosed to either arm).
- [x] No other candidate task run (D2–D5 untouched; no routing fix implemented; STOP after report).
