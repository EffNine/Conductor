# CONDUCTOR CodeBro Instrumentation Probe — PURE PLUMBING TEST

> No engineering task. No ON/OFF comparison. No source/test/config modification.
> No memory seeding. No commit. No push.

**Verdict (Phase 1, canonical scope): `INSTRUMENTATION BLOCKED: TOOLS NOT EXPOSED`**

**Verdict (Phase 2, isolated ON scope at `/tmp/conductor-codebro-on-probe`):
`INSTRUMENTATION PASS`**

Phase 1 (below, §§1–10) established the root cause in the canonical scope:
the CodeBro MCP server is defined globally but explicitly **disabled at
project scope**, so no CodeBro tools reach the agent. Phase 2 (§§11–20)
fixes ONLY the experimental plumbing in an isolated copy — overriding the
single project-scope `enabled: false` → `enabled: true` — and demonstrates
an actual CodeBro **MCP tool invocation** (`codebro_workspace_context`) by a
headless OpenCode session started on the isolated configuration, with valid
returned data and zero state mutation. The canonical `opencode.jsonc` was
never modified.

## 1. Starting state

Recorded before any probe action; repository left exactly as found.

- **HEAD:** `44d4facd9acb33ab78df2b8fe0bfbeb6ba502dba`
  (`44d4fac chore: gitignore the disposable C3B local config`, branch `main`)
- **git status (porcelain v1):**
  ```
  M .gitignore
  M internal/catalog/catalog.go
  M internal/health/modelstatus.go
  M internal/router/auto_service.go
  M internal/router/selection.go
  M internal/router/virtual_resolver.go
  ?? CONDUCTOR_CODEBRO_CAUSAL_TRIAL.md
  ?? CONDUCTOR_ENGINEERING_ASSESSMENT.md
  ?? internal/catalog/iface_check_test.go
  ?? internal/router/multi_provider_candidates_test.go
  ?? opencode.jsonc
  ```
  Identical to the causal-trial starting state plus the trial's own report
  file (`CONDUCTOR_CODEBRO_CAUSAL_TRIAL.md`, expected deliverable artifact).
  Nothing was reset or discarded.
- **diffstat:** 6 files, 135 insertions, 4 deletions (same as trial record).
- **OpenCode version:** `1.18.31` (`opencode --version`).
- **CodeBro version:** `codebro 1.1.0` (`codebro --version`).
- **`.codebro/` index present:** 10 JSON files
  (`architecture`, `constraints`, `current_sprint`, `engineering_decisions`,
  `engineering_memory`, `facts`, `metadata`, `project_identity`, `roadmap`,
  `workspace`); sha256 hashes snapshotted before/after (§8).

## 2. MCP configuration evidence

Two configuration layers, pointing in opposite directions:

1. **Global (`~/.config/opencode/opencode.jsonc`, lines 134–141):** a `codebro`
   MCP server IS defined —
   ```json
   "mcp": {
     "codebro": {
       "type": "local",
       "command": ["/home/afnan/.local/bin/codebro", "serve"]
     },
   ```
   Note: unlike sibling servers (`opensandbox`, `ainotebook`, `cloudflare*`,
   `context7`, `github`), the `codebro` entry carries **no explicit
   `"enabled": true` flag**.
2. **Project (`/home/afnan/projects/active/conductor/opencode.jsonc`,
   verbatim 2-line file):**
   ```json
   { "$schema": "https://opencode.ai/config.json", "mcp": { "codebro": { "enabled": false } } }
   ```
   This is the pre-existing OFF harness artifact. It **explicitly disables**
   the `codebro` MCP server for the Conductor project scope.

3. **Adjacent integration (not MCP):** `~/.config/opencode/plugins/codebro-context.js`
   — an OpenCode plugin that injects a bounded CodeBro digest at session start
   by calling the read-only `codebro context` CLI (fail-open, workspace-gated
   on `.codebro/facts.json`, never approves/publishes/mutates). Its header
   documents the host-driven contract. This plugin — not MCP — is the source
   of the "CodeBro context" block present in this session's system prompt.

## 3. MCP initialization evidence

- **Plugin path initialized: YES.** This session's system prompt contains the
  injected CodeBro digest (workspace counts, intents, decisions, engineering
  memory), which proves `codebro-context.js` executed `codebro context`
  successfully at session start.
- **CodeBro MCP server initialized: NO.** `list_mcp_resources` in this runtime
  returns resources from exactly one server — `github` (4 UI resources). No
  `codebro` server, no codebro resources, no codebro resource templates. The
  agent's callable function inventory likewise contains zero `codebro_*`
  tools. Consistent with the project-level `"enabled": false`.

## 4. Tool exposure evidence

- Full callable-tool inventory inspected: `bash`, `read`, `glob`, `grep`,
  `edit`, `write`, `todowrite`, `task`, `skill`, `webfetch`, `websearch`,
  `question`, GitHub MCP tools, Cloudflare MCP tools, OpenSandbox tools,
  `list_mcp_resources`, `list_mcp_resource_templates`,
  `read_mcp_resource`, `ainotebook_context_*`, `context7_*` — **no
  `codebro_*` tool exists**.
- `list_mcp_resources` (no server filter): only `github` server entries.
- The ON subagent's self-report from the prior trial ("no `codebro_*` MCP
  tools exist in the available toolset") is independently corroborated — it
  was an accurate observation of the environment, not an agent failure.

## 5. Exact read-only operation used

- **Operation:** `codebro context` (CLI), run as
  `timeout 20 codebro context` from the repo root.
- **Why this one:** it is the documented host/hook integration surface
  (`codebro --help`: "Print the bounded engineering context packet
  (host/hook integration surface)") and the exact command the
  `codebro-context.js` plugin invokes. Read-only by design: prints a JSON
  packet, writes nothing.
- **Forbidden operations avoided:** no `remember` / `record_memory` / `learn`
  / `approve` / `publish` / `session close` / `init` / `import` / `consult`
  (consult contacts an external provider) — none invoked. No flags passed
  that could mutate state.

## 6. Tool invocation evidence

- Command exited **`0`**.
- Output (first ~3000 bytes captured): valid JSON packet with
  `decisions`/`facts`/`memory`/`records`, `execution_state.state:
  "unverified"` plus the note "no execution evidence is recorded for the
  current working tree…", `guard.verdict: "aligned"`,
  `impact.note: "no task given — impact guidance resumes once a task is
  supplied"`.
- **Invocation channel note (honesty):** this executed via the `bash` tool
  against the `codebro` CLI binary — NOT via an MCP tool call, because no
  CodeBro MCP tool exists in this runtime. It satisfies "harmless read-only
  CodeBro operation invoked by the agent" at the CLI plane, not at the MCP
  plane.

## 7. Returned-data summary

- Data is **valid**: well-formed JSON, schema-consistent with the plugin's
  digest renderer, exit 0.
- Data is **current**: `execution_state.tree_hash` corresponds to the present
  working tree; `guard.known_workspaces: 3`, `identity_matched: true`.
- Data is **consistent** with the session-prompt digest: same intent records
  (`intent.unified-memory` user_confirmed 0.95; `running-started`,
  `passed-repository`, `backend-duration` patterns), same "unverified —
  no execution evidence" execution state. Two independent reads (plugin at
  session start, probe just now) agree.
- **No task-specific information inserted:** the packet for "no task given"
  contains only structural/global context; nothing about routing,
  reachability, or the prior experiment was added by the probe.

## 8. State-before/state-after comparison

`.codebro/*.json` sha256, before vs after the `codebro context` call —
**all 10 hashes byte-identical** (e.g. `facts.json`
`9d9ada91…`, `engineering_memory.json` `bc4caef0…`, `workspace.json`
`26b1132f…`; full table in session transcript).

- `git status --porcelain=v1`: identical before/after (6 modified, 5
  untracked — the 5th untracked being this probe's own predecessors'
  artifacts, untouched).
- `git rev-parse HEAD`: `44d4fac…` before and after.
- `git diff --stat`: identical (135+/4- across 6 files).
- **No state mutation occurred.** Read-only operation confirmed read-only.

## 9. Final instrumentation classification

```
CodeBro configured:            YES  (server defined globally: `codebro serve`;
                                     but explicitly DISABLED at project scope
                                     via opencode.jsonc `"enabled": false`)
CodeBro MCP initialized:       NO   (no codebro server in this runtime;
                                     only the CLI-digest plugin path ran)
CodeBro tools exposed:         NO   (zero codebro_* tools; list_mcp_resources
                                     shows github only)
CodeBro read-only call executed: YES (CLI `codebro context`, exit 0)
CodeBro returned valid data:   YES  (valid, current, consistent JSON packet)
```

Per the brief's success criteria, this is exactly the
**`INSTRUMENTATION BLOCKED: TOOLS NOT EXPOSED`** case ("CodeBro is configured
but tools are not exposed"). It is NOT `INVOCATION FAILED` (nothing invocable
exists to fail) and NOT `UNEXPECTED MUTATION` (hashes prove zero mutation).

## 10. Whether the environment is now valid for a causal ON/OFF CodeBro experiment

**NO — not as an MCP-tool ON/OFF experiment.** As long as the project scope
carries `"mcp": {"codebro": {"enabled": false}}`, an "ON arm with CodeBro
integration enabled" cannot receive CodeBro MCP tools, so ON and OFF are
mechanically identical at the tool plane; any future tie under this
configuration would be vacuous rather than informative. The prior trial's
0/0 call counts are thereby explained and its `NO MATERIAL CODEBRO
CONTRIBUTION OBSERVED` verdict must be read with this caveat: it measured
prompt-digest-only CodeBro, not tool-using CodeBro.

To make the environment valid, ONE of the following is required (each needs
explicit user authorization since each mutates config or scope — NOT done here):

1. **Enable the MCP for the trial scope** (e.g. set project
   `mcp.codebro.enabled: true` or remove the override on a trial copy), then
   re-run this probe to confirm tools-exposed before any causal task; or
2. **Redefine ON/OFF around the digest channel** (OFF: session with
   `CODEBRO_CONTEXT_DISABLED=1`; ON: normal session) and state upfront that
   the trial measures digest-injection, not MCP tools; or
3. **Run ON subagents outside the project scope** where the global MCP
   definition is not overridden — verifying first that tools actually appear.

Recommendation: option 1 on an isolated trial copy (never on the live
project config), followed by a re-probe. Until the re-probe reads
`TOOLS EXPOSED: YES`, do not run another causal task.

---

### Appendix — Safety check

- [x] No source code modified (status/diffstat identical before/after).
- [x] No tests modified or added.
- [x] No configuration modified (project + global `opencode.jsonc` only read;
      `codebro --help` / `codebro context` are non-mutating).
- [x] No CodeBro memory seeded (no write operations; `.codebro/` hashes
      identical; returned packet inspected for insertions — none).
- [x] No commit, no push (`HEAD 44d4fac` throughout).
- [x] No engineering investigation performed; no Conductor conclusion drawn.
- [x] Only new file: this report (`CONDUCTOR_CODEBRO_INSTRUMENTATION_PROBE.md`).

---

# PHASE 2 — Isolated ON-scope probe (MCP-tool plane)

> Canonical configuration untouched. Isolated copy at
> `/tmp/conductor-codebro-on-probe`. No engineering task. No comparison.
> No memory seeding. No commit. No push.

## 11. Isolation mechanism (exact)

- **Mechanism:** full filesystem copy, `rm -rf /tmp/conductor-codebro-on-probe
  && cp -a /home/afnan/projects/active/conductor
  /tmp/conductor-codebro-on-probe` (87M source, 12G avail on /tmp).
  `cp -a` preserves the working tree exactly: HEAD, all 6 modified tracked
  files, all untracked files, `.git/`, `.codebro/`, data, binaries.
- **Copy fidelity proof (before override):** copy `git status --porcelain=v1`
  byte-identical to canonical (6× `M`, 6× `??`); copy HEAD `44d4fac…`
  identical; copy sha256 of all 6 modified files + `opencode.jsonc` identical
  to canonical manifest (e.g. `catalog.go 0f395f6f…`, `modelstatus.go
  8a822db0…`, `opencode.jsonc c3f8c8f7…`).
- **Override:** exactly one field in exactly one file in the copy ONLY —
  `/tmp/conductor-codebro-on-probe/opencode.jsonc`:
  `"mcp": {"codebro": {"enabled": false}}` → `"mcp": {"codebro":
  {"enabled": true}}`. No harness invented; no other file touched.
- **Whole-tree diff proof (canonical vs copy, after override):**
  `diff -rq` reports exactly two differing paths: `opencode.jsonc` (the
  intended override) and `.git/index` (git-internal metadata rewritten by the
  copy itself; `git status` output identical, so no semantic difference).
  No provider/model/MCP/plugin/permission/env change.

## 12. Canonical vs isolated config state + exact diff

- **Canonical** (`.../conductor/opencode.jsonc`, verbatim):
  `{ "$schema": "https://opencode.ai/config.json", "mcp": { "codebro": { "enabled": false } } }`
- **Isolated** (`/tmp/conductor-codebro-on-probe/opencode.jsonc`, verbatim):
  `{ "$schema": "https://opencode.ai/config.json", "mcp": { "codebro": { "enabled": true } } }`
- **Exact configuration diff (the entire semantic change):**
  ```diff
  -  "$schema": "https://opencode.ai/config.json", "mcp": { "codebro": { "enabled": false } } }
  +  "$schema": "https://opencode.ai/config.json", "mcp": { "codebro": { "enabled": true } } }
  ```

## 13. Override resolution proof (not assumed from file content)

`opencode mcp list` executed in each scope (same binary 1.18.31, same global
config, only the project-scope file differs):

- **Canonical scope:** `○ codebro disabled` —
  `/home/afnan/.local/bin/codebro serve` (all other 9 servers `✓ connected`).
- **Isolated scope:** `● ✓ codebro connected` —
  `/home/afnan/.local/bin/codebro serve` (all other 9 servers `✓ connected`,
  unchanged).

Therefore: CodeBro configured YES → project-scope disable NO → CodeBro MCP
enabled YES → MCP initialized (connected) YES — proven by the resolver
itself, before any agent ran.

## 14. Headless ON-probe run (MCP tool invocation)

- **Launch:** `opencode run --dir /tmp/conductor-codebro-on-probe "<probe
  prompt>"` from the canonical checkout (output captured to
  `/tmp/on-probe-run.log`, exit `0`). `--dir` forces OpenCode to resolve
  project configuration from the isolated copy ONLY.
- **Prompt constraints (verbatim intent):** MCP instrumentation probe only —
  no repository investigation, no file reads, no tests, no writes; list MCP
  servers + CodeBro tools; invoke EXACTLY ONE harmless read-only CodeBro MCP
  tool (context/recall/facts/brief-type; NEVER remember/record_memory/learn/
  approve/publish/session-close/reindex/mutating ops; stop with a report if
  none exists); report servers/tools/invocation/success/data; end with
  `PROBE_DONE`. No engineering content of any kind.
- **Session identity:** `build · agnes-3.0-flash` (same model/provider as the
  causal trial arms — no model confound introduced).

## 15. Exposed MCP tool list (as seen by the ON session)

- **MCP servers seen (9):** cloudflare, cloudflare-builds, cloudflare-docs,
  cloudflare-bindings, cloudflare-observability, **codebro**, context7,
  github, opensandbox.
- **CodeBro tools seen (24):** `codebro_workspace_context`, `codebro_context`,
  `codebro_engineering_brief`, `codebro_engineering_facts`,
  `codebro_engineering_memory`, `codebro_memory_stats`,
  `codebro_update_identity`, `codebro_record_memory`, `codebro_delete_memory`,
  `codebro_reindex`, `codebro_forget`, `codebro_learn`, `codebro_recall`,
  `codebro_remember`, `codebro_sandbox_build`, `codebro_sandbox_test`,
  `codebro_sandbox_exec`, `codebro_sandbox_status`,
  `codebro_repository_health`, `codebro_consult`, `codebro_task`,
  `codebro_skill`, `codebro_apply_change`, `codebro_apply_changes`.

## 16. Actual tool invocation evidence

- **Invoked:** `codebro_workspace_context` with no arguments — a pure
  read-only workspace-identity/facts read (not in the forbidden set).
- **Marker in run log:** `⚙ codebro_workspace_context` followed by the
  structured report ending in `PROBE_DONE`; run exit `0`.
- **This is an MCP tool invocation**, not the CLI: it was issued by the
  OpenCode agent through the connected `codebro serve` MCP server inside the
  isolated session. The Phase-1 CLI insufficiency is thereby cured.
- **Call count:** exactly one CodeBro tool call; log grep confirms no other
  `remember`/`record`/`learn`/`edit`/`write` tool activity (26-line log,
  fully inspected).

## 17. Returned-data validity

- **Success:** yes — tool returned successfully.
- **Content (agent-reported, consistent with the known index):** project
  identity `conductor` (Go, GitHub repo); fact counts ~10,206 total
  (437 modules, 4,151 symbols, 2,336 tests — matching the session-prompt
  CodeBro facts block: modules=437, symbols=4151, tests=2336, total=10206);
  architecture summary; execution state unverified/0 evidence records; index
  freshness stale; 5 engineering ADR decisions; supported languages.
- **Validity basis:** counts cross-check against two independent sources
  (Phase-1 CLI packet + session-prompt digest); no task-specific or
  experiment-specific content present or inserted.

## 18. State-before/state-after verification

- **Copy `.codebro/*.json` (10 files):** all sha256 byte-identical before the
  headless run and after (e.g. `facts.json 9d9ada91…`,
  `engineering_memory.json bc4caef0…`, `workspace.json 26b1132f…`).
- **Copy working tree:** `git status --porcelain=v1` identical before/after
  (6× `M`, 6× `??`); HEAD `44d4fac…`; `git diff --stat` identical
  (135+/4-). The probe agent created no files (recursive tree diff vs
  canonical still shows only `opencode.jsonc` + `.git/index`).
- **Canonical `.codebro/*.json`:** all 10 hashes identical to the §1/§8
  manifest (untouched — the isolated MCP server ran with cwd in the copy).
- **Canonical working tree:** status/HEAD/diffstat identical to manifest;
  canonical `opencode.jsonc` still `enabled: false` (verified by cat +
  `mcp list` showing `○ codebro disabled`).
- **Conclusion:** only the isolated configuration differs; CodeBro state
  unchanged; Conductor tree unchanged in BOTH scopes.

## 19. Final classification

```
CodeBro configured:             YES (global definition + isolated enabled:true)
CodeBro MCP initialized:        YES (✓ codebro connected in isolated scope)
CodeBro tools exposed:          YES (24 codebro_* tools listed by ON session)
CodeBro MCP tool actually invoked: YES (codebro_workspace_context, no args)
Invocation succeeded:           YES (exit 0, PROBE_DONE)
Returned data valid:            YES (cross-checked identity/counts/decisions)
CodeBro state mutation:         NONE (10/10 hashes identical, both scopes)
Conductor files changed:        NONE (both scopes; canonical config untouched)
```

**`INSTRUMENTATION PASS`** — all seven success conditions hold.

## 20. Authorization for the next phase

The environment **is now valid for a causal ON/OFF CodeBro experiment**
under the isolated-copy design: OFF arm = canonical scope (project disable
inert, 0 tools — as proven in Phase 1) or an equivalent isolated copy with
`enabled: false`; ON arm = isolated copy with `enabled: true` (24 tools —
as proven here). Constraints for the next phase: preserve this plumbing
exactly, keep the single-field diff minimal, re-verify `mcp list` +
state hashes per arm, forbid mutating CodeBro tools in task prompts, and
STOP after measurement. No causal task is authorized by this report itself.

---

### Appendix — Phase-2 safety check

- [x] Canonical `opencode.jsonc` never modified (`enabled: false` + `○
      codebro disabled` re-verified after the run).
- [x] No canonical source/test/config file modified (status/diffstat/HEAD
      identical).
- [x] No CodeBro memory seeded, recorded, learned, approved, or published
      (single read-only call; `.codebro/` hashes identical in both scopes).
- [x] No commit, no push (HEAD `44d4fac` in both scopes; isolated copy
      uncommitted by design).
- [x] Isolated override NOT committed anywhere; lives only at
      `/tmp/conductor-codebro-on-probe/opencode.jsonc` (outside the repo).
- [x] No engineering task run; no Conductor conclusion drawn; no D-task
      started. STOP after this report.
