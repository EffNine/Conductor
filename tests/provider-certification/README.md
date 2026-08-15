# Provider Certification Suite

**Initial release** — Testing framework design for certifying provider adapter compliance with Conductor's canonical contract.

---

## Overview

The Provider Certification Suite validates that each upstream provider adapter correctly implements Conductor's canonical contract. It ensures every provider behaves identically from the gateway's perspective, regardless of internal adapter differences.

**Purpose:** Certify that each provider adapter:
1. Translates native request formats into canonical request shapes
2. Produces canonical response shapes from native responses
3. Handles errors, streams, and edge cases consistently
4. Reports accurate usage and cost data
5. Supports declared capabilities (vision, tool calling, reasoning, etc.)

**Scope:** Design document only — no implementation code.

---

## Test Categories and Scenarios

Each scenario defines a contract test that all certified providers must pass.

---

### 1. Basic Chat

**Purpose:** Verify the adapter handles a simple single-turn text-only chat completion.

**Input:**
```json
{
  "model": "openai/gpt-4o",
  "messages": [
    {"role": "system", "content": [{"type": "text", "text": "You are a helpful assistant."}]},
    {"role": "user", "content": [{"type": "text", "text": "Say hello in one word."}]}
  ],
  "stream": false
}
```

**Expected Canonical Output:**
- `CanonicalResponse` with one `CanonicalChoice`
- `finish_reason == "stop"`
- `usage.prompt_tokens > 0`, `usage.completion_tokens > 0`
- Content block is `type: "text"` with non-empty `text`

**Failure Criteria:**
- Empty or nil `choices`
- `finish_reason` is nil or unknown
- Token counts are zero or negative
- Content block is missing or empty

---

### 2. Streaming

**Purpose:** Verify SSE streaming with multiple chunks produces correct canonical stream output.

**Input:**
```json
{
  "model": "openai/gpt-4o",
  "messages": [{"role": "user", "content": [{"type": "text", "text": "Count from 1 to 3."}]}],
  "stream": true
}
```

**Expected Canonical Output:**
- At least 3 `CanonicalStreamChunk` events received
- Each chunk has `index == 0`
- `delta.content` contains text fragments that concatenate to the full response
- Final chunk has `finish_reason` set (non-nil)
- Final chunk includes `usage` with non-zero token counts
- Stream terminates cleanly (no goroutine leaks, no partial writes)

**Failure Criteria:**
- Less than 2 chunks received
- Missing `finish_reason` on final chunk
- Missing `usage` on final chunk
- Goroutine leak or context cancellation not propagated

---

### 3. Tool Calling (Single)

**Purpose:** Verify single tool invocation request and response flow.

**Input:**
```json
{
  "model": "openai/gpt-4o",
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "What is the weather in San Francisco?"}]}
  ],
  "tools": [{
    "type": "function",
    "function": {
      "name": "get_weather",
      "description": "Get current weather for a city",
      "parameters": {
        "type": "object",
        "properties": {"city": {"type": "string"}},
        "required": ["city"]
      }
    }
  }]
}
```

**Expected Canonical Output:**
- One `CanonicalToolCall` in `choices[0].message.tool_calls`
- `function.name == "get_weather"`
- `function.arguments` is valid JSON with `city` key
- `finish_reason == "tool_calls"`
- No content text blocks in the assistant message

**Failure Criteria:**
- Zero tool calls when one was expected
- Wrong tool name or missing arguments
- `finish_reason` is not `"tool_calls"`
- Arguments are not valid JSON

---

### 4. Parallel Tool Calls

**Purpose:** Verify the adapter handles multiple tool calls in a single response.

**Input:**
```json
{
  "model": "openai/gpt-4o",
  "messages": [{"role": "user", "content": [{"type": "text", "text": "Get weather in SF and NYC."}]}],
  "tools": [{
    "type": "function",
    "function": {
      "name": "get_weather",
      "description": "Get weather",
      "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}
    }
  }]
}
```

**Expected Canonical Output:**
- Two or more `CanonicalToolCall` entries in `tool_calls`
- Each has a unique `id`
- Each has valid `function.name` and `function.arguments`
- `finish_reason == "tool_calls"`

**Failure Criteria:**
- Fewer tool calls than the model's response indicates
- Duplicate call IDs
- Missing or malformed arguments

---

### 5. JSON Output

**Purpose:** Verify `response_format: {"type": "json_object"}` produces valid JSON.

**Input:**
```json
{
  "model": "openai/gpt-4o",
  "messages": [{"role": "user", "content": [{"type": "text", "text": "Return a JSON object with keys 'name' and 'value'."}]}],
  "response_format": {"type": "json_object"}
}
```

**Expected Canonical Output:**
- Response content block is parseable as JSON
- JSON object contains at minimum keys `name` and `value`
- No markdown fencing (```json ... ```) around the output
- `finish_reason == "stop"`

**Failure Criteria:**
- Content is not valid JSON
- Required keys are missing
- Markdown fencing is present
- `finish_reason` indicates truncation

---

### 6. Structured Output (JSON Schema)

**Purpose:** Verify `response_format` with a full JSON schema constraint.

**Input:**
```json
{
  "model": "openai/gpt-4o",
  "messages": [{"role": "user", "content": [{"type": "text", "text": "Describe a user."}]}],
  "response_format": {
    "type": "json_schema",
    "json_schema": {
      "name": "user",
      "schema": {
        "type": "object",
        "properties": {
          "name": {"type": "string"},
          "age": {"type": "integer", "minimum": 0}
        },
        "required": ["name", "age"],
        "additionalProperties": false
      },
      "strict": true
    }
  }
}
```

**Expected Canonical Output:**
- Response is valid JSON matching the schema
- `name` is a string, `age` is an integer >= 0
- No extra properties present
- `finish_reason == "stop"`

**Failure Criteria:**
- JSON does not match schema
- Required fields are missing
- Extra properties are present when `strict: true`
- Schema validation fails

---

### 7. Thinking / Reasoning

**Purpose:** Verify models that emit chain-of-thought reasoning content.

**Input:**
```json
{
  "model": "anthropic/claude-sonnet-4-20250514",
  "messages": [{"role": "user", "content": [{"type": "text", "text": "Solve: what is 2+2?"}]}],
  "max_tokens": 500
}
```

**Expected Canonical Output:**
- Assistant message contains a `thinking` field (non-nil)
- `thinking.thinking` is non-empty
- `thinking.type == "thinking"`
- A `content` text block with the final answer is also present
- `finish_reason == "stop"`

**Failure Criteria:**
- `thinking` is nil when the model supports it
- `thinking.thinking` is empty
- Missing final answer content block
- Thinking block lacks a valid type

---

### 8. Vision

**Purpose:** Verify image + text input is accepted and processed.

**Input:**
```json
{
  "model": "openai/gpt-4o",
  "messages": [{
    "role": "user",
    "content": [
      {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "<base64-data>"}},
      {"type": "text", "text": "What is in this image?"}
    ]
  }]
}
```

**Expected Canonical Output:**
- Response contains a text content block
- `finish_reason == "stop"`
- Content describes the image (non-generic answer)

**Failure Criteria:**
- Adapter rejects image input (should support if capabilities declare `vision: true`)
- Empty response content
- Error code indicates unsupported feature when vision is claimed

---

### 9. Long Context

**Purpose:** Verify the adapter handles requests near the model's context window.

**Input:**
- Prompt constructed with ~120k tokens of filler text (or provider's max context)
- Simple question appended at the end

**Expected Canonical Output:**
- Request is accepted without context-length errors
- Response is generated for the final question
- `usage.prompt_tokens` reflects the actual input length
- `finish_reason == "stop"` or `"length"` (acceptable)

**Failure Criteria:**
- Provider returns context-length exceeded error
- Adapter does not forward the full context
- Truncation occurs silently before the final question

---

### 10. Error Handling

**Purpose:** Verify the adapter maps upstream errors into canonical error shapes.

**Test Matrix:**
- Invalid API key → 401
- Model not found → 404
- Rate limit → 429
- Internal server error → 500
- Invalid request (bad JSON) → 400

**Input:** Varied per error type (e.g., wrong key, nonexistent model ID)

**Expected Canonical Output:**
- `CanonicalError` with correct `code`, `status`, and `provider`
- `retryable` is `true` for 429 and 5xx, `false` for 4xx
- Original provider error code and message preserved in `provider_code` / `provider_message`

**Failure Criteria:**
- Missing or incorrect error code
- Wrong HTTP status
- `retryable` flag is incorrect
- Provider error details are lost

---

### 11. Rate Limits

**Purpose:** Verify 429 handling and retry behavior.

**Input:**
- Mock provider that returns 429 on first request, 200 on second

**Expected Canonical Output:**
- First request returns `CanonicalError{code: "rate_limit", status: 429, retryable: true}`
- Retry succeeds on second attempt
- `usage` is recorded only on the successful request
- Total latency includes retry delay

**Failure Criteria:**
- No retry attempt
- Retry exceeds configured limit
- Usage is double-counted
- Error is not marked `retryable`

---

### 12. Timeout Recovery

**Purpose:** Verify provider timeout triggers fallback or error, not a hang.

**Input:**
- Mock provider with 1-second timeout that sleeps for 5 seconds

**Expected Canonical Output:**
- Request completes within timeout + margin (e.g., 3 seconds)
- Returns `CanonicalError{code: "provider_timeout"}` or triggers fallback
- Context cancellation is propagated

**Failure Criteria:**
- Request hangs beyond timeout
- No error returned
- Context not cancelled on timeout

---

### 13. Retry

**Purpose:** Verify transient error retry logic with backoff.

**Input:**
- Mock provider that fails with 500 on first 2 attempts, succeeds on 3rd

**Expected Canonical Output:**
- Final response is successful
- 3 total attempts made
- Backoff delays between retries (exponential or configurable)
- `usage` recorded only on final success

**Failure Criteria:**
- Fewer retries than configured max
- No backoff (all retries instantaneous)
- Usage recorded on failed attempts
- Final request returns error

---

### 14. Cancellation

**Purpose:** Verify mid-stream cancellation cleans up resources.

**Input:**
- Start a streaming request
- Cancel the context after receiving 2 chunks

**Expected Canonical Output:**
- Stream terminates cleanly
- No goroutine leak
- Context error is returned to the caller
- Upstream connection is closed

**Failure Criteria:**
- Goroutine count increases after cancellation
- Upstream connection remains open
- Error is not propagated

---

### 15. Failover

**Purpose:** Verify primary provider failure activates fallback.

**Input:**
- Configured with two providers: primary (mock fails) and fallback (mock succeeds)
- Request to a model route that has a fallback

**Expected Canonical Output:**
- Primary attempt fails with error
- Fallback provider is selected automatically
- Response comes from fallback
- `metadata.provider` reflects the fallback provider
- Error is recorded for primary, success for fallback

**Failure Criteria:**
- Fallback is not attempted
- Response originates from failed primary
- No failover metadata

---

### 16. Multi-turn Tool Loop

**Purpose:** Verify a conversation with alternating user → tool → assistant → tool turns.

**Input:**
- Turn 1: User asks a question requiring a tool
- Tool result is fed back as a tool message
- Turn 2: Model should produce a final text response

**Expected Canonical Output:**
- Turn 1: `finish_reason == "tool_calls"`, one tool call
- Turn 2: `finish_reason == "stop"`, text response present
- Tool call IDs match between request and result messages

**Failure Criteria:**
- Tool call IDs do not match
- Missing tool result message
- Turn 2 has no text content
- `finish_reason` is incorrect on either turn

---

### 17. Agent Workflow

**Purpose:** Verify a full agent loop: think → tool → think → respond.

**Input:**
- Model with reasoning + tool calling enabled
- Request that requires reasoning, then a tool call, then final response

**Expected Canonical Output:**
- First response: `thinking` block present, `tool_calls` present, `finish_reason == "tool_calls"`
- After tool result is submitted: second response has `thinking` block and text content, `finish_reason == "stop"`
- All canonical shapes are correct across both turns

**Failure Criteria:**
- Missing reasoning in either turn
- Tool calls not present when expected
- Final response missing or empty
- Canonical shape violations in any turn

---

## Certification Levels

| Level | Minimum Scenarios | Description |
|-------|------------------|-------------|
| **Platinum** | 17/17 | Full contract compliance. All features certified. |
| **Gold** | 14/17 | Core features certified. Up to 3 non-critical gaps allowed. |
| **Silver** | 10/17 | Basic compliance. Streaming, tools, and errors pass. |
| **Bronze** | 5/17 | Minimal compliance. Chat and basic errors pass. |
| **Untested** | <5 | No certification. Adapter should not be used in production. |

### Required Scenarios by Level

| Scenario | Platinum | Gold | Silver | Bronze |
|----------|----------|------|--------|--------|
| 1. Basic Chat | Required | Required | Required | Required |
| 2. Streaming | Required | Required | Required | Optional |
| 3. Tool Calling | Required | Required | Required | Optional |
| 4. Parallel Tool Calls | Required | Required | Optional | — |
| 5. JSON Output | Required | Required | Optional | — |
| 6. Structured Output | Required | Optional | — | — |
| 7. Thinking / Reasoning | Required | Optional | — | — |
| 8. Vision | Required | Optional | — | — |
| 9. Long Context | Required | Optional | — | — |
| 10. Error Handling | Required | Required | Required | Required |
| 11. Rate Limits | Required | Required | Optional | — |
| 12. Timeout Recovery | Required | Required | Optional | — |
| 13. Retry | Required | Required | Required | Optional |
| 14. Cancellation | Required | Optional | — | — |
| 15. Failover | Required | Optional | — | — |
| 16. Multi-turn Tool Loop | Optional | Optional | — | — |
| 17. Agent Workflow | Optional | — | — | — |

---

## Runner Design

### Configuration Per Provider

```yaml
# tests/provider-certification/config.yaml
providers:
  openai:
    base_url: "https://api.openai.com/v1"
    api_key_env: "OPENAI_API_KEY"
    models:
      - "gpt-4o"
      - "gpt-4o-mini"
    capabilities:
      streaming: true
      vision: true
      tool_calling: true
      reasoning: false
      structured_output: true

  anthropic:
    base_url: "https://api.anthropic.com/v1"
    api_key_env: "ANTHROPIC_API_KEY"
    models:
      - "claude-sonnet-4-20250514"
    capabilities:
      streaming: true
      vision: true
      tool_calling: true
      reasoning: true
      structured_output: true

  ollama:
    base_url: "http://localhost:11434/v1"
    models:
      - "llama3.2"
    capabilities:
      streaming: true
      vision: false
      tool_calling: false
      reasoning: false
      structured_output: false
```

### Test Matrix

The runner generates a Cartesian product:

```
Matrix = Providers × Models × Scenarios
```

Each cell is a single test execution. Skip combinations that contradict declared capabilities (e.g., skip Vision test if `vision: false`).

### Result Reporting

```
tests/provider-certification/results/
├── 2026-01-15T10-00-00Z-openai-gpt4o.json
├── 2026-01-15T10-00-00Z-anthropic-claude.json
└── latest/
    └── summary.json
```

**Per-test result shape:**
```json
{
  "provider": "openai",
  "model": "gpt-4o",
  "scenario": "basic_chat",
  "status": "pass",
  "latency_ms": 342,
  "error": null,
  "metadata": {
    "tokens_used": 58,
    "chunks_received": 1
  }
}
```

**Summary shape:**
```json
{
  "run_id": "2026-01-15T10-00-00Z",
  "certification_level": "Platinum",
  "total": 17,
  "passed": 17,
  "failed": 0,
  "skipped": 0,
  "providers_tested": ["openai", "anthropic", "ollama"]
}
```

### Integration with CI

```yaml
# .github/workflows/provider-certification.yaml
name: Provider Certification
on:
  push:
    branches: [main]
  pull_request:
  schedule:
    - cron: '0 2 * * *'  # Daily at 2 AM UTC

jobs:
  certify:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        provider: [openai, anthropic, ollama, nvidia_nim]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - name: Run certification suite
        run: go test ./tests/provider-certification/... -run Certification -v
        env:
          OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
          OLLAMA_BASE_URL: http://localhost:11434/v1
      - name: Upload results
        uses: actions/upload-artifact@v4
        with:
          name: cert-results-${{ matrix.provider }}
          path: tests/provider-certification/results/
```

### Local Runner Commands

```bash
# Run all certifications
go test ./tests/provider-certification/... -run Certification -v

# Run a specific provider
go test ./tests/provider-certification/... -run Certification/OpenAI -v

# Run a specific scenario
go test ./tests/provider-certification/... -run Certification/OpenAI/BasicChat -v

# Generate a summary report
go run ./cmd/cert-report/main.go tests/provider-certification/results/latest/summary.json
```

---

## Notes

- This document is a **design specification**. No test code is included.
- Mock providers should be implemented as part of the test infrastructure to ensure reproducible results.
- Capability declarations from `internal/provider/metadata.go` drive skip logic — do not test features a provider does not claim.
- Certification levels are reviewed quarterly; thresholds may adjust as the contract evolves.
