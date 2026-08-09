# Provider Scorecard Framework

**Sprint V2.3** — Scoring framework for evaluating upstream provider quality and Conductor compatibility.

> **This document defines the scoring framework. Actual scores are not yet populated.** See `provider-capabilities.schema.yaml` for the machine-readable capability metadata that feed into scoring.

## Purpose

The scorecard provides a consistent, quantifiable way to compare providers across dimensions that matter to Conductor users: reliability, capability coverage, agent workflow support, and integration quality. Scores inform:

- **Auto-mode model selection** — which provider/model to pick for a given task
- **Fallback chain ordering** — which providers to try first
- **Routing engine weights** — how much each dimension influences the scorer
- **Release notes** — transparent provider quality reporting

## Scoring Scale

All categories use a **1–5 scale**:

| Score | Label | Meaning |
|-------|-------|---------|
| 5 | Excellent | Fully supports the dimension with production-grade quality |
| 4 | Good | Strong support with minor gaps or edge cases |
| 3 | Adequate | Basic support; notable limitations or inconsistencies |
| 2 | Limited | Partial support; significant gaps or known issues |
| 1 | Poor | Minimal or broken support; not recommended for this dimension |
| 0 | Not Evaluated | No data available; provider not scored in this category |

## Categories

### 1. Compatibility

**What it measures:** How well the provider's native API matches Conductor's canonical contract (OpenAI-compatible chat completions shape, standardized error format, consistent field naming).

**How it is scored:**
- **5** — Native OpenAI-compatible API; zero transformation needed
- **4** — OpenAI-compatible with minor field differences (e.g. `stop_reason` naming)
- **3** — Requires significant request/response mapping (e.g. Anthropic Messages API)
- **2** — Partial compatibility; some endpoints work, others need custom logic
- **1** — No compatibility; fully custom adapter required
- **0** — Not evaluated

**Data sources:**
- Adapter implementation in `internal/provider/<name>/`
- `docs/provider-compatibility/compatibility-matrix.md`
- Round-trip test results (`internal/provider/*_test.go`)

---

### 2. Reliability

**What it measures:** Uptime, consistency of responses, and resilience under load. How often the provider returns successful responses versus errors, timeouts, or degraded output.

**How it is scored:**
- **5** — 99.9%+ uptime; consistent response quality; no known systemic issues
- **4** — 99.5%+ uptime; rare transient failures; consistent quality
- **3** — 99%+ uptime; occasional failures or inconsistent behavior
- **2** — Frequent timeouts or errors; quality varies significantly
- **1** — Unreliable; frequent outages or severe inconsistency
- **0** — Not evaluated

**Data sources:**
- Health probe results (`internal/health/modelprobe.go`)
- Error rates from `/api/models/status`
- User-reported incident data
- Provider status page history

---

### 3. Tool Calling

**What it measures:** Quality and completeness of function/tool calling support — single and parallel tool calls, schema validation, argument extraction accuracy, and error handling when tools fail.

**How it is scored:**
- **5** — Full tool calling with parallel calls, strict JSON schema validation, accurate argument extraction
- **4** — Tool calling works well; minor issues with schema enforcement or edge cases
- **3** — Basic tool calling; works for simple cases but fails on complex schemas
- **2** — Tool calling is buggy or incomplete; frequent argument loss or schema errors
- **1** — Tool calling is broken or unavailable
- **0** — Not evaluated

**Data sources:**
- `internal/provider/*_test.go` tool calling tests
- `router/capability.go` capability metadata
- Agent workflow integration tests

---

### 4. Streaming

**What it measures:** SSE stream quality — chunk ordering, idle timeout handling, usage reporting at stream end, error propagation, and compatibility with streaming consumers (OpenCode, etc.).

**How it is scored:**
- **5** — Perfect SSE streaming; correct chunk ordering, usage in final chunk, no dropped chunks
- **4** — Streaming works well; minor issues with edge cases (e.g. very long reasoning streams)
- **3** — Streaming generally works; occasional chunk reordering or missing usage
- **2** — Streaming is unreliable; frequent interruptions or malformed chunks
- **1** — Streaming is broken
- **0** — Not evaluated

**Data sources:**
- `pkg/sse/sse.go` stream parser behavior
- `internal/handler/handler_stream_test.go`
- `internal/provider/*_test.go` streaming tests
- Observed stream behavior with long-reasoning models

---

### 5. Structured Output

**What it measures:** Support for JSON mode (`response_format: json_object`) and schema-enforced structured output (JSON Schema, Pydantic models). Accuracy of output matching the requested schema.

**How it is scored:**
- **5** — Full structured output with schema enforcement; outputs always match requested schema
- **4** — JSON mode works; schema enforcement is best-effort
- **3** — Basic JSON mode; no schema enforcement; occasional format drift
- **2** — JSON mode is unreliable; output frequently malformed
- **1** — No structured output support
- **0** — Not evaluated

**Data sources:**
- `apitypes.NormalizeChoices` structured output handling
- Provider-specific `response_format` or native schema support
- Test coverage in provider test files

---

### 6. Agent Readiness

**What it measures:** How well the provider supports agent workflows — tool calling + streaming + reasoning + vision combined, context window size, error recovery, and compatibility with agent frameworks (OpenCode, Conductor automode).

**How it is scored:**
- **5** — Excellent agent support: tools, streaming, reasoning, vision, long context all work together
- **4** — Strong agent support; one or more dimensions has minor gaps
- **3** — Adequate for simple agent workflows; breaks down with complex tool chains
- **2** — Limited agent support; frequent failures in multi-step workflows
- **1** — Not suitable for agent workflows
- **0** — Not evaluated

**Data sources:**
- `internal/automode/` classifier and selector behavior
- `internal/router/capability.go` aggregated capability metadata
- End-to-end agent test suites
- `docs/provider-compatibility/compatibility-matrix.md`

---

### 7. Documentation Quality

**What it measures:** Clarity, completeness, and accuracy of the provider's public documentation — API reference, examples, error codes, rate limit documentation, and model catalog.

**How it is scored:**
- **5** — Excellent docs; complete API reference, clear examples, accurate error documentation
- **4** — Good docs; minor gaps or outdated sections
- **3** — Adequate docs; some information missing or hard to find
- **2** — Poor docs; significant gaps, outdated information
- **1** — No usable documentation
- **0** — Not evaluated

**Data sources:**
- Public provider documentation URLs (stored in `Metadata.DocumentationURL`)
- Conductor contributor experience implementing the adapter
- Community issue volume related to documentation gaps

---

### 8. Testing Status

**What it measures:** Extent of test coverage for the provider adapter — unit tests, integration tests, streaming tests, and edge case coverage.

**How it is scored:**
- **5** — Comprehensive test coverage; all public methods tested; streaming and error paths covered
- **4** — Good test coverage; most paths tested; minor gaps
- **3** — Basic test coverage; core functionality tested; edge cases missing
- **2** — Minimal tests; core path only; streaming or errors untested
- **1** — No tests
- **0** — Not evaluated (provider not in codebase)

**Data sources:**
- `internal/provider/<name>/provider_test.go` test file existence and coverage
- `go test ./internal/provider/<name>/...` pass/fail status
- Code coverage reports

---

### 9. Overall Score

**What it measures:** Weighted aggregate of all category scores, reflecting the provider's overall suitability for Conductor workloads.

**How it is scored:**
- Computed as a weighted average of the 8 category scores
- Default weights (can be customized per deployment):
  - Compatibility: 1.0
  - Reliability: 1.5
  - Tool Calling: 1.5
  - Streaming: 1.0
  - Structured Output: 1.0
  - Agent Readiness: 2.0
  - Documentation: 0.5
  - Testing: 0.5
- Final score rounded to 1 decimal place
- **5.0** = ideal provider for all Conductor workloads
- **0.0** = not evaluated or not suitable

**Data sources:**
- Aggregate of all category scores
- Configurable weights in `routing.weights`

## How to Use the Scorecard

### For Contributors

1. Implement or update a provider adapter in `internal/provider/<name>/`
2. Add tests in `provider_test.go`
3. Update `internal/provider/metadata.go` capabilities
4. Populate this scorecard with observed scores
5. Update `compatibility-matrix.md` if capabilities change

### For Operators

1. Check `compatibility-matrix.md` for feature availability
2. Review scorecard categories relevant to your workload (e.g. Agent Readiness for agent apps)
3. Use `routing.weights` in `config.yaml` to bias auto-selection toward high-scoring providers
4. Monitor `/api/models/status` for live reliability data

### For the Routing Engine

The intelligent routing engine (`internal/router/scorer.go`) can consume scorecard data to weight provider selection. Current routing weights (`health`, `latency`, `cost`, `capability`) map to scorecard categories:

| Routing Weight | Scorecard Category |
|----------------|-------------------|
| `health` | Reliability |
| `latency` | (runtime metric, not scored) |
| `cost` | (runtime metric, not scored) |
| `capability` | Tool Calling + Structured Output + Agent Readiness |

## Future Work

- [ ] Populate actual scores for all 17 providers
- [ ] Link scorecard to `/api/providers/scorecard` dashboard endpoint
- [ ] Auto-compute Reliability score from health probe error rates
- [ ] Add latency and cost as scored dimensions
- [ ] Historical score tracking and trend analysis
