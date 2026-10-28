# CONDUCTOR CodeBro Git-Poor Trial — FIRST VALID-ON ATTEMPT

> No canonical config change. No CodeBro seeding. No commit. No push.
> No remaining candidate task run.

**Verdict: `CAUSALITY INCONCLUSIVE` — OFF arm: `INFRASTRUCTURE FAILURE`
(non-termination); ON arm: NOT RUN (withheld — a lone ON is uninterpretable).**

The validated isolated-ON plumbing (Phase-2 `INSTRUMENTATION PASS`) was reused
without modification, but the trial harness itself — headless `opencode run`
with the frozen generic prompt — did not terminate within experimental budget:
both OFF attempts entered the same OpenSandbox source-transfer loop
(tarball → chunked base64 → `opensandbox_file_write`, ~61 chunks) and never
reached a conclusion. Attempt-1 was killed at a 25-minute timeout; attempt-2
was killed after ~54.5 minutes when stalled (identical progress text across
consecutive polls, 1 chunk written). Running ON alone after OFF failed would
prove nothing about causality, so ON was deliberately withheld.

## 1. Starting state

- **HEAD:** `44d4facd9acb33ab78df2b8fe0bfbeb6ba502dba` (branch `main`).
- **Working tree (canonical + both copies, verified identical):** 6 modified
  tracked files (`.gitignore`, `internal/catalog/catalog.go`,
  `internal/health/modelstatus.go`, `internal/router/auto_service.go`,
  `internal/router/selection.go`, `internal/router/virtual_resolver.go`;
  diffstat 135+/4-) + 6 untracked files (`CONDUCTOR_CODEBRO_CAUSAL_TRIAL.md`,
  `CONDUCTOR_CODEBRO_INSTRUMENTATION_PROBE.md`,
  `CONDUCTOR_ENGINEERING_ASSESSMENT.md`,
  `internal/catalog/iface_check_test.go`,
  `internal/router/multi_provider_candidates_test.go`, `opencode.jsonc`).
- **CodeBro PRE hashes (canonical, before either arm):** all 10
  `.codebro/*.json` recorded (e.g. `facts.json 9d9ada91…`,
  `engineering_memory.json bc4caef0…`, `workspace.json 26b1132f…`; full table
  in session transcript). No CodeBro CLI/MCP call was made during selection —
  inspection was by direct file read only, so PRE state is pristine by
  construction.
- **Versions:** OpenCode `1.18.31`, CodeBro `1.1.0`, model `agnes-3.0-flash`
  (same as all prior arms — no model confound).

## 2. Candidate historical context

Selected from pre-existing CodeBro state (zero additions), as the
best-available git-poor probe:

- **Primary — `rc1:routing-fallback-fix`** (importance 0.9, conf 0.95):
  RC-1 found+fixed a real bug in `internal/handler/chat.go` — fallback
  candidates filtered by ProviderName ONLY erased same-provider model-level
  dynamic fallbacks (single-provider setups silently lost all failover).
  Fix: candidate identity is `(ProviderName, ProviderModelID)` in
  selection-mapping and fallback-rebuild loops. Regression test
  `dynamic_fallback_same_provider_test.go` (FAIL pre-fix, PASS post-fix).
- **Supporting — `architecture:dynamic-category-fallback`** (0.85/0.9):
  eligibility hard filters (vision/planning-agentic/long-horizon),
  `RankedEligible` shared scoring, `catalog.List` does LIVE ListModels HTTP
  per provider so `rankedAlternates` memoizes per (vision, mode) 60s TTL
  (long_horizon/agentic bypass; legacy nil-cfg keeps fallback disabled).
- **Supporting — `architecture:auto-model-routing-decoupling`,
  `architecture:embeddings-failover-parity`, `docs:deployment-truth-fixes`,
  `health:model-probe-skip-providers`:** resolver-nil legacy behavior,
  embeddings DEFAULT|vision=false cache-entry sharing, CONDUCTOR_ prefix
  requirement, skipped-providers filter-ready semantics.
- **Relevance:** the uncommitted tree state rebuilds candidate pools
  (`ListReachable` + `(provider, modelID)` dedup in auto/virtual resolvers)
  feeding `RankedEligible`/dynamic tail — exactly the subsystem of the RC-1
  incident. Judging whether the diff preserves the identity/failover contract
  is the natural invariant question for a generic review.
- **Independent verifiability:** commit `e38d8f7` (message states the
  identity invariant verbatim) + in-tree regression test + `chat.go`
  pair-comparisons (lines ~238/249/261/271) + `candidateKey{entry.Provider,
  entry.ProviderModelID}` in the diff. Orchestrator verified pre-arm:
  invariant currently PRESERVED (diff key matches; committed loops intact).

## 3. Why it qualifies as git-poor (honestly: only WEAKLY)

Full disclosure — after genuine search, no STRONGLY git-poor live violation
was found; this candidate is at best weakly poor:

- **Git-recoverable core:** `e38d8f7` message documents the identity invariant
  verbatim; `7f7651a` message documents memoization rationale
  ("memoized per (vision, mode) with 60s TTL since catalog listing does live
  upstream HTTP"); `6fb3851` documents bare-model precedence + virtual guard;
  regression/contract tests pin P3.14/P3.15/P3.16/P3.5/auth/JEV-observer
  invariants in-tree; ADRs exist as `docs/adr/*.md`.
- **CodeBro-only residue:** embeddings DEFAULT|vision=false cache-entry
  coupling, long_horizon/agentic cache bypass, nil-cfg legacy contract
  detail, incident color ("upstream errors passed straight to clients" —
  partly also in messages), exact 1e-9 identity-component framing.
- **Applied rule:** per the brief, if OFF recovers the history via git, the
  task is classified "git-not-poor enough" — and OFF attempt-1's partial log
  in fact shows native recovery beginning (see §13). The thin residue is
  recorded here so a future trial does not re-select this candidate expecting
  strong poorness.

## 4. Exact task prompt

Byte-identical for both arms (no hints, no bug names, no file pointers):

> "Review the current Conductor implementation for a maintenance issue that
> could cause an existing engineering invariant or previously established
> behavior to be violated. Determine whether the current implementation is
> consistent with the project's intended behavior. Investigate the relevant
> implementation, tests, configuration, and available engineering history as
> needed. Do not assume that the current implementation is correct merely
> because it is internally consistent. Identify the relevant engineering
> invariant or behavioral expectation. Determine whether the current code
> preserves or violates it. If there is a real issue, explain the minimal
> correction that would restore the intended behavior. If there is no issue,
> explain why the current behavior is intentional and supported by evidence.
> Validate the conclusion using the strongest available checks. Do not modify
> source code. If a correction appears necessary, stop at the proposed
> correction and explain it."

## 5. OFF environment

- **Copy:** `/tmp/gp-off` (`cp -a` of canonical; status/HEAD/hashes verified
  identical pre-arm; `.codebro` hashes match PRE).
- **Config:** project `opencode.jsonc` `enabled: false` (unchanged);
  `opencode mcp list` in scope: `○ codebro disabled`, 9 others connected.
- **Digest suppression:** `CODEBRO_CONTEXT_DISABLED=1` (documented
  `codebro-context.js` knob) so OFF is CodeBro-free at BOTH the MCP and
  digest planes — otherwise the session-start digest would leak CodeBro
  content into the control arm. Recorded here as an explicit design decision.
- **Launch:** `opencode run --dir /tmp/gp-off --format json "<frozen>"`,
  model `agnes-3.0-flash`, JSON event log for precise tool accounting.
- **Single plugin in scope:** only `codebro-context.js` (disabled via env).

## 6. OFF investigation

Two attempts, same frozen prompt, same env. Full logs preserved:
`/tmp/gp-off-run.log` (attempt-1), `/tmp/gp-off-run2.log` (attempt-2).

- **Attempt-1** (25-min timeout kill; 263 events / 80 steps / 81 tool uses /
  193,679 tokens): substantive native investigation — HEAD/skip-providers
  commit, `modelprobe.go`, config, `ModelStatusStore` readiness logic,
  `ProbeAll`/`shouldProbe`/`MarkFilterReady` semantics, uncommitted diff,
  strict_healthy routing analysis (TEXT#16: derived the HEAD
  `catalog.List` strict-gate disappearance + "err toward availability"
  principle + prior-fix references, all natively), Viper CSV env probe —
  then OpenSandbox tarball+socket transfer loop (port-8899 self-conflict,
  120s tool timeout) until killed. No conclusion reached.
- **Attempt-2** (background + polling; killed after 3,273s ≈ 54.5 min at
  73+ steps): docs/history/test-reading path (health-semantic tests,
  router invariants), Go/sandbox version check, then the same transfer
  loop via chunked base64 (`conductor-src.tar.gz` → ~61 chunks →
  `opensandbox_file_write`; 1 chunk written, then stalled — identical
  progress text across consecutive polls). Killed as non-terminating.
- **Files inspected (attempt-1 text trail):** `modelprobe.go`, `config.go`,
  `provider_env.go`, `main.go`, `modelstatus.go`, `catalog*.go`,
  router/selection + resolvers, config tests, git history
  (`#11`/`#21` era fixes, flush-before-ready, skip-providers).
- **Git history inspected:** recent-feature commit, `ProbeAll` lineage,
  unmerged-branch check, uncommitted `git diff` (recognized as the likely
  locus: TEXT#15/16).
- **Tests/checks:** Viper env-behavior probes (host), sandbox Go-version
  check (1.25.5 vs go.mod 1.21 minimum — compatible); full suite never
  executed (transfer never completed).
- **Historical context independently discovered (partial log):** skip →
  filter-ready → `unknown_as_reachable` chain; strict_healthy default-true
  hides unprobed from `catalog.List`-built routing pools; "err toward
  availability" principle — all WITHOUT CodeBro (MCP disabled + digest
  suppressed). Directly supports the §3 git-not-poor reading.

## 7. OFF conclusion

**NONE — no conclusion reached (terminated mid-validation twice).**
Any statement of OFF's verdict would be fabrication. The partial trail
suggests where it was heading (reachability/filter-ready semantics of the
diff), but heading ≠ conclusion. Classification: INFRASTRUCTURE FAILURE
(harness non-termination, not agent error).

## 8. ON environment

- **Copy:** `/tmp/gp-on` (`cp -a`; status/HEAD/hashes identical pre-arm).
- **Override:** single field — `opencode.jsonc` `enabled: false` → `true`
  (Phase-2 validated mechanism; whole-tree `diff -rq` vs canonical shows
  only `opencode.jsonc` + `.git/index` metadata).
- **Resolution proof:** `opencode mcp list` in scope: `● ✓ codebro
  connected` (`codebro serve`), 24 `codebro_*` tools (per Phase-2 headless
  evidence); all other servers unchanged.
- **Launch planned (NOT executed):** identical `opencode run --dir /tmp/gp-on
  --format json "<frozen>"`, normal env (digest + MCP both live, as
  validated). Withheld — see §13.

## 9. ON CodeBro retrievals

**NONE — ON was never launched.** No retrievals, no relevance judgments, no
direction/conclusion effects to report. Anything else would be fabricated.

## 10. ON investigation / 11. ON conclusion

**NOT RUN.** Withheld deliberately: a lone ON result has no comparator, so
executing it would consume budget while proving nothing about causality.
`/tmp/gp-on` remains pristine (status/HEAD/`.codebro` hash-verified
post-decision) and available for a future bounded trial.

## 12. Independent verification

- **Candidate invariant truth (orchestrator, pre-arm):** RC-1 identity
  `(ProviderName, ProviderModelID)` VERIFIED in `e38d8f7` message +
  `dynamic_fallback_same_provider_test.go` (present) + `chat.go`
  pair-comparisons + diff `candidateKey` — currently PRESERVED, no live
  violation on identity. Memoization rationale VERIFIED in `7f7651a`
  message. Skip/filter-ready semantics VERIFIED in `ae0353e` + store code.
- **State integrity (post-arm, all scopes):** canonical tree
  (status/HEAD/diffstat identical), canonical `.codebro` (10/10 hashes =
  PRE), `gp-off` tree + `.codebro` (identical; agent wrote nothing durable
  in-scope), `gp-on` tree + config-override + `.codebro` (identical to
  pre-arm). Host strays from the sandbox loop (`*.tgz`, `*.b64`,
  `host-srv.log`, socket attempts) all removed; run logs + start marker
  preserved as evidence. Canonical `opencode.jsonc` still `enabled: false`.
  No CodeBro writes (no CLI/MCP calls by orchestrator; agents' only
  CodeBro-adjacent traffic was reads).
- **No memory seeding:** confirmed by hash equality + log inspection (no
  remember/record/learn/approve/publish calls anywhere).

## 13. Causal comparison

No ON/OFF comparison is possible — there is only one half of the experiment,
and it did not conclude. Per the brief's rule ("If OFF independently finds
the same historical information through git/repository evidence, classify the
task as git-not-poor enough"), the usable evidence points further:

- OFF attempt-1 natively formulated the skip/filter-ready invariant and
  derived the strict-gate routing analysis with zero CodeBro exposure —
  consistent with §3's WEAK-poorness assessment. Had OFF completed, the
  likely outcome was convergence with trial-1's native verdict, i.e. NO
  MATERIAL CONTRIBUTION territory — but "likely" is not evidence, so no
  classification is claimed on that basis.
- The selected candidate must NOT be re-selected for a future git-poor trial
  expecting strong poorness (§3 residue list is exhaustive and thin).

## 14. Timing/tool overhead

- **OFF attempt-1:** ~1,500s (timeout kill); 263 JSON events, 80 steps,
  81 tool uses (~21 read-ish), 193,679 tokens (mostly cached reads, cost 0).
- **OFF attempt-2:** 3,273s (~54.5 min, killed while stalled); 73+ steps,
  ~85 tool uses; tokens not tallied (run killed mid-stream).
- **Overhead source:** OpenSandbox source-transfer loop (18MB tarball →
  base64 → ~61 chunked `opensandbox_file_write` calls), identical in shape
  across both attempts — a harness property (frozen generic prompt +
  sandbox-first AGENTS.md guidance + "strongest available checks" clause),
  not a CodeBro effect. No CodeBro overhead exists to measure (no calls).
- **Experimenter cost:** two long supervisory windows plus
  stray-process/file cleanup (socket-bind attempts, tarballs, chunk files —
  all removed; no repo impact).

## 15. Confounders

- **Harness non-termination dominates:** the frozen prompt's "strongest
  available checks" + repo AGENTS.md sandbox doctrine reliably steer
  headless agents into the transfer loop; this would affect ON identically,
  so even a completed pair would carry huge time variance.
- **Attempt variance:** attempt-1 (probe-history focus) vs attempt-2
  (docs/tests focus) diverged purely by model nondeterminism under the
  identical prompt — reproducibility of any single-arm path is low.
- **Kill criterion is experimenter judgment:** stall defined as identical
  progress text across consecutive ~5-min polls with chunk count frozen at
  1/61; a longer leash might have completed, so INFRASTRUCTURE FAILURE is
  budget-relative, not absolute (documented here so it can be contested).
- **OFF digest suppression** (`CODEBRO_CONTEXT_DISABLED=1`) makes OFF
  doubly-clean vs ON (digest+MCP) — a two-channel difference, disclosed in
  §5; justified to prevent digest leakage into the control.
- **JSON format + `--dir` + env** are run parameters, not prompt content;
  prompt bytes were identical (single shell arg, no interpolation).

## 16. Final classification

- **OFF arm outcome: INFRASTRUCTURE FAILURE** (non-termination × 2; no
  conclusion; partial native investigation preserved in logs).
- **ON arm outcome: NOT RUN** (withheld; no evidence for or against).
- **CodeBro contribution: unmeasurable in this trial** — no ON evidence exists.
- **Evidence standard: `CAUSALITY INCONCLUSIVE`.**
- **Task-suitability finding (non-causal, evidence-backed): git-not-poor
  leaning** — selection search found no strongly git-poor live violation
  (all precise invariants test-pinned and commit-documented), and OFF's
  partial trail recovered the relevant history natively. Do NOT re-select
  the RC-1/fallback cluster for a git-poor trial.

## 17. Whether the remaining Conductor tasks should proceed

**NO further headless-run causal trials with the frozen generic prompt until
the harness is bounded.** The validated plumbing (Phase-2 PASS) is NOT the
problem and needs no rework. What failed is termination control. Preconditions
for any next trial:

1. A step/time budget the agent can see (or a harness that enforces one)
   without adding task hints — e.g. run-level watchdog that stops validation
   loops while preserving the investigation record.
2. Local-test-first behavior for Go suites (host `go test` completes in
   seconds; the sandbox path cost ~80 minutes for zero test output).
3. A candidate invariant that is BOTH CodeBro-only AND live-violated,
   demonstrated BEFORE arms run — the present search found none; do not
   re-run on hope.
4. Keep the isolated-copy design (`/tmp/gp-on` pristine and ready),
   per-arm `mcp list` + hash verification, and the no-seeding rule exactly
   as applied here.

---

### Appendix — Safety check

- [x] Canonical source/tests/config untouched (status/HEAD/diffstat identical
      throughout; `opencode.jsonc` still `enabled: false`).
- [x] Canonical `.codebro` untouched (10/10 hashes = PRE; no orchestrator
      CodeBro calls; no agent writes).
- [x] Isolated overrides uncommitted and confined to `/tmp` (canonical repo
      has no trace of them; `/tmp/gp-on` override intact for future use).
- [x] No memory/fact/ADR/task seeding (hash + log verified).
- [x] No commit, no push (HEAD `44d4fac` everywhere).
- [x] Exact frozen prompt used for both OFF attempts; ON would have received
      the identical bytes.
- [x] No hidden answer supplied to any arm (candidate kept in orchestrator
      notes until this report).
- [x] No other candidate task run; no fix implemented. STOP after this trial.
