# Gemini Tool Schema Normalization — Evidence Report

**Date:** 2026-08-21  
**Repository:** /home/afnan/projects/active/conductor  
**Branch:** main  
**Remote:** origin https://github.com/EffNine/Conductor.git

---

## 1. Repository Identity

```
pwd:                    /home/afnan/projects/active/conductor
git rev-parse:          /home/afnan/projects/active/conductor
git remote -v:          origin https://github.com/EffNine/Conductor.git (fetch/push)
git branch:             main
```

All expected directories present: `cmd/conductor/`, `internal/provider/gemini/`, `internal/router/`, `internal/handler/`.

---

## 2. Git State

### Modified tracked files:
```
 M internal/provider/gemini/tool_mapper.go
```

### New untracked files (intended change):
```
?? internal/provider/gemini/tool_mapper_test.go
```

### Pre-existing untracked files (unrelated — not part of this change):
```
?? internal/provider/tool_params.go
?? internal/provider/tool_params_test.go
?? .codebro/
?? CONDUCTOR_CAPABILITY_ROUTING_E2E_REPORT.md
?? P3.3_MODE_PROFILE_ACTIVATION_REPORT.md
?? P3.4_ROUTING_CONTRACT_CLEANUP_REPORT.md
?? P3.5_PUBLIC_MODE_API_REPORT.md
?? P3.6_LONG_HORIZON_CAPABILITY_ENFORCEMENT_REPORT.md
?? P3.7_EXECUTION_CAPABILITY_TELEMETRY_ATTRIBUTION_REPORT.md
?? P3.8_PLANNING_MODE_ACTIVATION_REPORT.md
?? P3.9_AGENTIC_MODE_ACTIVATION_REPORT.md
?? P3.10_EXECUTION_TELEMETRY_ATTRIBUTION_REPORT.md
?? P3.11_CROSS_MODE_ROUTING_CALIBRATION_REPORT.md
?? P3.12_ROUTING_EDGE_CASE_CONTRACT_REPORT.md
?? P3.13_CAPABILITY_SIGNAL_QUALITY_REPORT.md
?? P3.14_ROUTING_DECISION_TRACE_CONTRACT_REPORT.md
?? P3.15_ROUTING_TRACE_PERSISTENCE_REPORT.md
?? P3.16_ROUTING_TRACE_QUERY_API_REPORT.md
?? P3.17_API_AUTHENTICATION_REPORT.md
```

### Diff stat:
```
 internal/provider/gemini/tool_mapper.go | 178 +++++++++++++++++++++++++++++++-
 1 file changed, 177 insertions(+), 1 deletion(-)
```

---

## 3. Intended Diff

`internal/provider/gemini/tool_mapper.go`:
- Added import `"github.com/EffNine/conductor/internal/provider"`
- Changed `mapTool()` to call `provider.StripSchemaMetaFields()` then `normalizeGeminiSchema()`
- Added `normalizeGeminiSchema()`, `normalizeGeminiUnion()`, `normalizeGeminiAllOf()`, `normalizeGeminiType()`, `normalizeGeminiSchemaValue()`, `toMap()`, `toSlice()` — all unexported, scoped to the `gemini` package

`internal/provider/gemini/tool_mapper_test.go` (new):
- 647 lines
- 28 unit test cases covering all schema constructs
- 1 OpenCode regression fixture
- 1 allOf-specific test
- Deep-equality helpers

---

## 4. Pre-existing / Unrelated Files

| File | Status | Notes |
|---|---|---|
| `internal/provider/tool_params.go` | Untracked | Pre-existing; contains `StripSchemaMetaFields()` |
| `internal/provider/tool_params_test.go` | Untracked | Pre-existing tests for `StripSchemaMetaFields()` |
| `.codebro/` | Untracked | CodeBro workspace state |
| `P3.*_REPORT.md`, `CONDUCTOR_*_REPORT.md` | Untracked | Prior sprint report artifacts |
| **None of these are modified by this change.** | | |

---

## 5. Focused Gemini Tests

```
go test ./internal/provider/gemini/... -count=1 -v
```

**Results: 32 tests, all PASS (0 FAIL)**

Tests explicitly covering every required construct:

| Construct | Test Case | Status |
|---|---|---|
| exclusiveMinimum | `exclusiveMinimum removed` | PASS |
| exclusiveMaximum | `exclusiveMaximum removed`, `both exclusive constraints removed` | PASS |
| additionalProperties | `additionalProperties removed`, `additionalProperties true removed` | PASS |
| nullable type union | `nullable type union string null`, `nullable type union null string`, `nullable type union preserves existing nullable` | PASS |
| mixed type union | `anyOf mixed types falls back to first` | PASS |
| anyOf | `anyOf nullable union`, `anyOf single non-null option`, `anyOf mixed types falls back to first` | PASS |
| oneOf | `oneOf nullable union`, `oneOf single option` | PASS |
| allOf | `TestNormalizeGeminiAllOf` | PASS |
| nested schemas | `nested combinations` (16 sub-constructs in one input) | PASS |
| OpenCode regression | `TestOpenCodeRegressionFixture` (read_file + search_code) | PASS |

---

## 6. Full Test Suite

```
go test ./... -count=1
```

**44 packages, 0 failures.** Every package reports `ok`.

---

## 7. Race Detector

```
go test -race ./internal/provider/... -count=1
```

**17 provider packages, all PASS.** No data races detected.

---

## 8. Vet

```
go vet ./...
```

**Clean — no output, no warnings.**

---

## 9. Build

```
go build ./...
```

**Clean — no output, no errors.**

---

## 10. Schema Transformation Evidence

### A. $-prefixed metadata removed by generic normalizer
`internal/provider/tool_params.go:8-37` — `StripSchemaMetaFields()` iterates all keys, skips any starting with `$`, recursively descends into nested maps and arrays.

Called at `tool_mapper.go:228`:
```go
params := provider.StripSchemaMetaFields(t.Function.Parameters)
```

### B. Gemini-specific normalization removes rejected fields
`tool_mapper.go:266-268`:
```go
case "exclusiveMinimum", "exclusiveMaximum", "additionalProperties":
    continue  // silently dropped
```

### C. Nullable type union
`tool_mapper.go:285-296`:
```go
case "type":
    typ := normalizeGeminiType(v)
    out[k] = typ
    if arr, ok := v.([]interface{}); ok {
        for _, item := range arr {
            if s, ok := item.(string); ok && s == "null" {
                out["nullable"] = true
                break
            }
        }
    }
```
Input: `{"type": ["string", "null"]}` → Output: `{"type": "string", "nullable": true}`

### D. Mixed union deterministic fallback
`tool_mapper.go:361-379` (`normalizeGeminiType`):
- Picks first non-null type from array
- If all are null, returns empty string
- Deterministic: always picks first non-null

### E. anyOf / oneOf handled per documented policy
`tool_mapper.go:304-342` (`normalizeGeminiUnion`):
- Nullable union (`[type, null]`) → promoted to singular type + `nullable: true`
- Single non-null option → flattened to that option
- Multiple non-null options → first option kept (lossy, documented)

### F. allOf handled per documented policy
`tool_mapper.go:344-356` (`normalizeGeminiAllOf`):
- First clause extracted and normalized
- Other clauses discarded (lossy, documented)

### G. Standard supported fields pass through unchanged
`tool_mapper.go:297-299` (`default` case):
```go
default:
    out[k] = normalizeGeminiSchemaValue(v)
```
`normalizeGeminiSchemaValue` recurses into nested maps and arrays of maps. Primitives (strings, numbers, bools, enum arrays, required arrays) pass through untouched.

Verified preserved: `type`, `properties`, `items`, `enum`, `required`, `description`, `minimum`, `maximum`, `minLength`, `maxLength`, `pattern`, `minItems`, `maxItems`, `format`, `default`, `nullable`.

---

## 11. OpenCode Regression Fixture Mapping

**Test:** `TestOpenCodeRegressionFixture` (tool_mapper_test.go:428)

### Tool 1: `read_file`

| Original Error | Source Schema Construct | Gemini Normalization | Result |
|---|---|---|---|
| `Unknown name "exclusiveMinimum"` | `offset.exclusiveMinimum: 0` | Removed (line 266) | `offset.minimum: 0` preserved |
| `Unknown name "exclusiveMaximum"` | `limit.exclusiveMaximum: 10000` | Removed (line 266) | `limit.maximum: 10000` preserved |
| `Proto field is not repeating` | `encoding.type: ["string","null"]` | Flattened + nullable set (lines 285-296) | `encoding.type: "string"`, `encoding.nullable: true` |
| `Unknown name "additionalProperties"` | root `additionalProperties: false` | Removed (line 266) | Root schema valid without it |

### Tool 2: `search_code`

| Original Error | Source Schema Construct | Gemini Normalization | Result |
|---|---|---|---|
| `Unknown name "additionalProperties"` | `filters.items.additionalProperties: true` | Removed (line 266) | Items schema valid without it |
| `Unknown name "anyOf"` | `value.anyOf: [{type:string},{type:number}]` | Flattened to first option (lines 304-342) | `value.type: "string"` |

**Verified output:** No unsupported fields remain in serialized JSON. Tool names and descriptions preserved. Required fields preserved.

---

## 12. Provider Isolation

```
git diff HEAD --stat -- internal/provider/openai/ internal/provider/anthropic/ internal/router/ internal/handler/ internal/apitypes/
```

**Result: No output — zero files changed in any of these paths.**

OpenAI adapter: unchanged (still sends full JSON Schema to OpenAI upstream).  
Anthropic adapter: unchanged (still sends full JSON Schema to Anthropic upstream).  
Router, handler, virtual-model, API contract: untouched.

---

## 13. Live E2E Availability

```
LIVE_GEMINI_E2E = NOT_AVAILABLE
```

No Gemini API key or OpenCode CodeBro runtime is configured in this environment. Provider-level serialization tests (mocked HTTP) constitute the complete evidence.

---

## 14. Final Conclusion

| Gate | Status |
|---|---|
| Repository identity | CONFIRMED — EffNine/Conductor, main branch |
| Intended diff scope | CONFIRMED — only `tool_mapper.go` modified |
| New test file | CONFIRMED — `tool_mapper_test.go` (647 lines, untracked) |
| Focused Gemini tests | PASS — 32/32 |
| Full test suite | PASS — 44/44 packages |
| Race detector | PASS — 17/17 provider packages |
| Vet | CLEAN |
| Build | CLEAN |
| Provider isolation | CONFIRMED — no other packages touched |
| Regression fixture | PRESENT and PASSING |
| Live E2E | NOT_AVAILABLE (documented) |

**VERIFIED**

---

## 15. Independent Grill Verification Pass

A second, adversarial re-verification pass was performed against this evidence
report (originally recorded separately; consolidated here during P3 closeout).

Exact Gemini errors addressed:

1. **Unknown name "exclusiveMinimum"** — stripped (`exclusiveMinimum`,
   `exclusiveMaximum`, `additionalProperties` case)
2. **Unknown name "exclusiveMaximum"** — same case
3. **Unknown name "additionalProperties"** — same case
4. **Proto field is not repeating, cannot start list** — array `type` values
   such as `["string","null"]` rewritten to a single string type by
   `normalizeGeminiType`; array types would otherwise trigger this proto error

Additional transformations: `anyOf`/`oneOf` flattened via
`normalizeGeminiUnion`, `allOf` flattened via `normalizeGeminiAllOf`,
nullable unions rewritten to `type` + `nullable: true`.

Grill-pass verification results:

| Check | Result |
|-------|--------|
| Repository identity | EffNine/Conductor ✅ |
| Gemini focused tests | 32/32 PASS ✅ |
| Full test suite | All packages PASS (44 packages) ✅ |
| Race detector (`go test -race ./...`) | PASS ✅ |
| Vet (`go vet ./...`) | Clean ✅ |
| Build (`go build ./...`) | Clean ✅ |
| Provider isolation | Only gemini tool_mapper changed ✅ |

Live E2E remained NOT_AVAILABLE for both passes (no Gemini API key in this
environment); provider-level serialization tests with mocked HTTP constitute
the complete evidence.

```
VERIFIED_FOR_GRILL
```
