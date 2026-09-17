# Phase C2 — Offline JEV Routing Evaluation

## Status: implemented — harness complete, real-data result is `INSUFFICIENT_EVIDENCE`

**Constraint honored:** C2 is offline-only. No live JEV API was called, no
inference tokens were spent, no production routing behavior was modified, and
no JEV result was fed back into the router. The harness is a pure reader.

---

## 1. Dataset audit

### Schema (`shadow_observations`, from `internal/jev/shadow/store.go` + `sqlite.go`)

| Column | Type | Nullable | Notes |
|--------|------|----------|-------|
| `id` | INTEGER PK | no | autoincrement |
| `created_at` | DATETIME | no | observation timestamp |
| `decision_id` | TEXT(64) UNIQUE | no | one row per routing decision |
| `requested_model` | TEXT(191) | yes | virtual/model ID before resolution |
| `resolved_mode` | TEXT(64) | yes | classifier mode (coding, fast, …) |
| `selected_provider` | TEXT(64) | yes | deterministic winner |
| `selected_model` | TEXT(191) | yes | winner model |
| `selected_score` | REAL | no | deterministic total score |
| `candidate_count` | INTEGER | no | number of candidates |
| `candidate_providers` | TEXT(512) | yes | CSV of candidate providers |
| `advisory_provider` | TEXT(64) | yes | JEV top recommendation |
| `advisory_confidence` | REAL | **yes (NULL)** | JEV confidence pointer |
| `advisory_probabilities` | TEXT(1024) | yes | JSON map of full distribution |
| `jev_model` | TEXT(64) | yes | JEV model id |
| `jev_duration_ms` / `jev_input_tokens` / `jev_output_tokens` | INT | no | JEV call metadata |
| `advisory_status` | TEXT(32) | yes | `ok\|timeout\|unauthorized\|rate_limited\|malformed\|error\|not_configured` |
| `outcome` | TEXT(16) | yes | initially `unknown\|selected\|rejected\|failed`, backfilled to `success\|failed\|skipped` |
| `provider_latency_ms` | INT | no | backfilled execution latency |

### Real-data findings (verified, not assumed)

- `data/conductor.db` is a 0-byte WAL-locked file: the
  `shadow_observations` table **does not exist**.
- `data/p3_16_e2e.db` has no `shadow_observations` table either (only
  `routing_traces` etc., 14 rows, all `selected`).
- **Total observations: 0. Usable: 0. Outcome coverage: 0.**
- Historical JEV shadow data does not exist yet. The advisor (C1) is
  disabled by default and has not run in this environment.

This is the expected state for a freshly-implemented C1 with no live
inference run. C2 must therefore report `INSUFFICIENT_EVIDENCE` and be
verified against synthetic fixtures only.

---

## 2. Evaluation design

### Files added

| File | Purpose |
|------|---------|
| `internal/jev/shadow/eval/eval.go` | Core harness: classification, agreement, calibration, counterfactual, breakdowns, data quality, JSON/text writers |
| `internal/jev/shadow/eval/snapshot.go` | Reproducible snapshot export/import (read-only) |
| `internal/jev/shadow/eval/eval_test.go` | Focused unit tests (synthetic fixtures in-test only) |
| `internal/jev/shadow/eval/eval_int_test.go` | No-mutation + adapter tests |
| `internal/jev/shadow/eval_e2e_test.go` | End-to-end SQLite store → eval pipeline test |
| `cmd/conductor/eval.go` | `conductor eval` subcommand (read-only loader) |
| `tests/c2eval/main.go` | Fixture generator (build-tag `c2fixture`, not part of normal build) |
| `cmd/conductor/main.go` | One new case: `eval` subcommand dispatch |

**No production routing, advisor, bridge, store, handler, or decision-pipeline
code was modified.** The only existing-file change is the `eval` subcommand
dispatch in `main.go` and a gosec shadow-variable rename in one unrelated
startup line.

### Dataset selection policy (deterministic, per-observation classification)

Every row is assigned to exactly one class. B/C/D are **not** treated as JEV
failures.

- **A. COMPLETE** — valid deterministic decision (selected provider +
  non-empty candidate set), valid JEV advisory (`advisory_status=ok` with
  a provider and well-formed probabilities when present), and a known
  execution outcome (`success`/`failed`/`skipped`).
- **B. NO_OUTCOME** — routing/advisory usable but the execution outcome
  was not recorded. **Not a JEV failure.**
- **C. JEV_ERROR** — `advisory_status` is one of
  `timeout/unauthorized/rate_limited/malformed/error/not_configured`.
  Reported, not treated as advisory-signal failure.
- **D. INVALID** — a usable row with `advisory_status=ok` but internally
  inconsistent fields (missing provider, or unparseable/non-finite
  probabilities). These are excluded from all metrics and counted.

### Metric definitions

- **Agreement** — over rows with a valid JEV top recommendation
  (status `ok`, provider present, row not invalid): exact match of
  `advisory_provider` vs `selected_provider`. Descriptive only;
  agreement ≠ correctness. Disagreement count is reported explicitly.
- **Calibration** — confidence buckets (default deciles, configurable via
  `--bucket-edges`). Per bucket: sample count, empirical success rate,
  mean probability assigned to the successful-observed provider, Brier
  score `(p−y)²`, log-loss (guarded to `0<p<1`). Small buckets are
  flagged `low_sample` (default threshold 20) and never summarized as
  meaningful. Brier/log-loss require a valid probability distribution
  and a known outcome.
- **Mode breakdown** — counts + agreement by `resolved_mode` (only modes
  present in the data; unknown modes flagged).
- **Provider breakdown** — observation/selected/advised counts,
  agreement/disagreement, and outcome counts per provider. **No
  ranking.**
- **Counterfactual — `successful_observed_provider_match`** — for
  complete rows whose outcome is `success` and whose successful provider
  is determinable (the selected provider, since the schema has **no
  fallback-provider field**): does `advisory_provider` equal that
  provider? Named precisely as a provider *match*, not "picked the
  winner". Rows where a winner cannot be established are counted as
  `unresolvable`, never guessed.
- **Outcome stratification** — `success` / `failed` / `skipped` /
  `unknown_or_unavailable` reported separately.

### Calibration methodology

Positive class = "selected provider succeeds" (y=1 for `success`, y=0
otherwise). Probability p = JEV probability assigned to the selected
provider. Brier and log-loss are only computed where p ∈ (0,1). Buckets
below `MinBucketSamples` are reported but flagged.

---

## 3. Results

### Against the real observation store

```
$ conductor eval --db ./data/conductor.db
eval: no shadow observations found at ./data/conductor.db (no usable data)
eval: verdict=INSUFFICIENT_EVIDENCE total=0 complete=0
```

- Total observations: **0**
- Usable (complete): **0**
- Outcome coverage: **0**
- JEV error count: **0** (no rows)
- Invalid count: **0** (no rows)
- Agreement / calibration / counterfactual: **not measurable** (no data)

All metric fields are present in the JSON output but are zero/`n/a`, which
is the correct representation of an empty dataset — not a forced
conclusion.

### Against the synthetic fixture (pipeline verification only)

A 4-row fixture (`tests/c2eval`, build-tagged out of the normal build)
exercised the full DB → loader → evaluator path:

```
Dataset: total=4 complete=3 no_outcome=1 jev_error=0 invalid=0 outcome_coverage=0.750
Agreement (measurable=4): rate=0.500 agreements=2 disagreements=2
Global Brier: 0.0867   Global Log-Loss: 0.3243
Outcomes: success=2 failed=1 skipped=0 unknown=1
Counterfactual: eligible=2 matches=2 rate=1.000
Verdict: INSUFFICIENT_EVIDENCE  (3 complete < 10)
```

These numbers exist **only to prove the harness computes correctly**; they
are synthetic and must not be cited as evidence about JEV quality.

---

## 4. Data quality

Exclusions and reasons are emitted per-run in `data_quality.exclusions`
with exact counts. The quality dimensions tracked are:

- `duplicate_decision_ids`
- `malformed_probabilities` (valid-JSON check + finite/in-range check)
- `non_normalized_probabilities` (valid JSON that does not sum to ~1.0;
  flagged, not excluded)
- `candidate_mismatches` (selected or advisory provider not in the
  recorded candidate set)
- `unknown_providers` (when a provider whitelist is supplied)
- `unknown_modes` (mode not in the router's recognized set)

For the real store: all zero (no rows). For the fixture: all zero
(clean fixture). No records are silently dropped; every exclusion is
counted and reported.

---

## 5. Test results

### Focused C2 tests

```
$ CGO_ENABLED=1 go test -race -cover ./internal/jev/...
ok  	github.com/EffNine/conductor/internal/jev		58.7%
ok  	github.com/EffNine/conductor/internal/jev/shadow	54.8%
ok  	github.com/EffNine/conductor/internal/jev/shadow/eval	82.7%
```

`internal/jev/shadow/eval` — 30 tests, all passing, covering:

1. exact agreement
2. disagreement
3. missing outcome → NO_OUTCOME class
4. JEV error status classification (all six statuses)
5. malformed distribution → INVALID
6. candidate/provider mismatch → flagged, not excluded
7. calibration buckets
8. Brier score calculation
9. log-loss calculation
10. successful observed provider matching
11. multiple providers
12. empty dataset
13. insufficient sample sizes + verdict thresholds
14. deterministic ordering/reproducibility (shuffled input ⇒ identical JSON)
15. no mutation of inputs or store
16. outcome stratification
17. duplicate decision IDs
18. unknown mode reporting
19. snapshot round-trip
20. provider whitelist / unknown-provider reporting
21. non-normalized distribution reporting

End-to-end (SQLite store → eval):
`TestSQLiteStoreToEvalEndToEnd` proves the evaluator reads a real
`SQLiteStore` and never writes to it (row count unchanged after
evaluation).

### Full suite

```
$ CGO_ENABLED=1 go test -race ./...
59 packages ok, 0 FAIL
```

### Lint / format

```
$ golangci-lint run ./internal/jev/... ./cmd/...    # exit 0
$ gofmt -l internal/jev/shadow/eval/ cmd/conductor/eval.go   # clean
```

(Pre-existing lint findings in `internal/middleware` and `internal/auth`
are unrelated to C2 and were not introduced by this phase.)

---

## 6. Reproducibility

The evaluator is a pure function of (input records, configuration).

- **Fixed input:** pass a snapshot file with `--snapshot`. The snapshot is
  a deterministic JSON export of the observation rows. Same snapshot ⇒
  byte-identical JSON result.
- **Deterministic ordering:** input rows are sorted by
  `(decision_id, selected_provider, resolved_mode, advisory_provider)`
  before any computation, so input order never affects output.
- **Explicit configuration:** bucket edges and min-sample threshold are
  config fields; defaults are fixed (`BucketEdges` deciles,
  `MinBucketSamples=20`). `--bucket-edges` overrides deterministically.
- **Machine-readable output:** `--out-json` writes the full `Result`.
  **Human-readable:** `--out-text` writes a compact report.
- **Snapshot capture:** `--dump-snapshot <path>` exports the current
  store contents so a future run can be reproduced exactly.

Reproduce the empty-data result:
```
$ conductor eval --db ./data/conductor.db
```
Reproduce from a snapshot:
```
$ conductor eval --snapshot /path/to/snapshot.json --out-json out.json
```

---

## 7. Interpretation

### Directly measured facts

- There are **zero** persisted `shadow_observations` in this environment.
- The evaluation harness computes all defined metrics correctly over
  synthetic data and is provably read-only (no store mutation, no live
  JEV call, no routing modification).
- The verdict for the current store is `INSUFFICIENT_EVIDENCE` — the
  dataset is too small (indeed empty) to measure any signal.

### Limitations

- **No evidence exists yet.** C1 has not been run against live traffic in
  this repo, so no JEV advisory data has been accumulated.
- **The schema has no fallback-provider field.** A `success` outcome's
  provider is the deterministic selected provider; a success that required
  a cross-provider fallback cannot be attributed to a unique winner. The
  counterfactual is therefore named `successful_observed_provider_match`
  and is only computed where the winner is unambiguous.
- **Calibration quality is bounded by sample size.** With fewer than
  `MinBucketSamples` rows per confidence bucket, bucket statistics are
  flagged low-sample and must not be read as calibration conclusions.
- **Agreement is descriptive, not a correctness measure.** JEV agreeing
  with the deterministic router does not mean either is "right"; both may
  be wrong.

### Hypotheses requiring more data

- Whether JEV's confidence/probability distribution is calibrated to
  actual routing outcomes (needs ≥30 complete observations, ideally more
  per mode).
- Whether JEV disagrees systematically with the deterministic router in
  specific modes/providers (needs a disagreement corpus).
- Whether JEV's top recommendation matches the successful observed
  provider more often than chance (needs the counterfactual sample).

These are explicitly **not** answered here. No conclusion is drawn about
whether Conductor should or should not use JEV for routing.

---

## 8. C3 prerequisites (evidence needed before a controlled routing experiment)

A controlled JEV-assisted routing experiment (C3) is safe to design only
after C2 produces the following from **real** accumulated data:

1. **A minimum viable sample** of COMPLETE observations — at least the
   `minAdequateSamples` (30) threshold, with meaningful counts in each
   routing mode and provider being considered.
2. **Known, attributable outcomes** — a fallback-provider field (or
   equivalent) so a `success` outcome can be tied to the provider that
   actually served it, enabling a true counterfactual.
3. **Calibration evidence** — confidence buckets with ≥`MinBucketSamples`
   rows each, plus Brier/log-loss values, to establish whether JEV
   probabilities are usable as routing inputs at all.
4. **Disagreement analysis** — a corpus of disagreements with their
   outcomes, to understand where the deterministic router and JEV diverge
   and whether either side's outcomes are worse.
5. **A safe A/B or interleaved-experiment design** — only after 1–4
   show a measurable signal, and with the deterministic pipeline retained
   as the authority. JEV remains a shadow input until that gate passes.
6. **No regression guardrail** — a criterion (e.g. success-rate parity
   within confidence bounds) under which the experiment is stopped.

Until those exist, the correct C2 outcome is `INSUFFICIENT_EVIDENCE` —
not a forced conclusion in either direction.

---

## 9. Stop-condition check

None of the stop conditions were triggered by *data*; the dataset is
simply empty. The harness was still fully implemented and verified
against synthetic fixtures, per the instruction: "If there are currently
zero usable observations: report zero usable observations, verify the
evaluator against test fixtures, do not invent results." No live JEV call
was made, no outcomes were fabricated, and production routing was
untouched.

**Final C2 verdict: `INSUFFICIENT_EVIDENCE`** (0 usable observations).
The evaluation harness is complete, deterministic, and ready to run as
soon as real `shadow_observations` accumulate from C1.
