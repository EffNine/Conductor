# Canonical Contract

Conductor's internal, provider-agnostic types. Provider adapters translate upstream native formats into these shapes so routing, usage tracking, cost calculation, and the dashboard API work uniformly.

> **Principles**
> 1. Canonical types are Conductor-native — they are neither OpenAI's nor Anthropic's shapes.
> 2. Every field needed by the gateway (routing, cost, health, features) has a canonical home.
> 3. Provider-specific extras survive in `metadata` maps rather than breaking the contract.

---

## CanonicalRequest

The normalized inbound request shape. The router uses this to pick a provider and model; the adapter fills it from the provider's native format before dispatch.

| Field | Type | Description |
|-------|------|-------------|
| `model` | `string` | Conductor-resolved model ID (after route/alias lookup). E.g. `openai/gpt-4o`. |
| `messages` | `[]CanonicalMessage` | Ordered conversation. System message first if present. |
| `tools` | `[]CanonicalTool` | Available tools for the model. Empty when not using tool calling. |
| `stream` | `bool` | `true` for streaming responses. |
| `temperature` | `*float64` | nil means provider default. |
| `max_tokens` | `*int` | nil means provider default. |
| `top_p` | `*float64` | nil means provider default. |
| `frequency_penalty` | `*float64` | nil means provider default. |
| `presence_penalty` | `*float64` | nil means provider default. |
| `response_format` | `*CanonicalStructuredOutput` | Present when structured/json output is requested. |
| `metadata` | `map[string]string` | Provider-agnostic passthrough (e.g. user id, request id). |

**Example**

```json
{
  "model": "openai/gpt-4o",
  "messages": [
    {"role": "system", "content": [{"type": "text", "text": "You are helpful."}]},
    {"role": "user",   "content": [{"type": "text", "text": "Hello"}]}
  ],
  "stream": false,
  "tools": [],
  "metadata": {"user_id": "u-123"}
}
```

---

## CanonicalResponse

The normalized full (non-streaming) response shape emitted by an adapter after the upstream call completes.

| Field | Type | Description |
|-------|------|-------------|
| `model` | `string` | Echo of the resolved model. |
| `choices` | `[]CanonicalChoice` | Usually one choice; preserved as a slice for parity with multi-choice providers. |
| `usage` | `CanonicalUsage` | Token usage for cost tracking. |
| `id` | `string` | Provider-supplied response id (persisted for tracing). |
| `created` | `int64` | Unix timestamp from provider. |
| `metadata` | `map[string]interface{}` | Provider-specific extra fields not captured canonically. |

### CanonicalChoice

| Field | Type | Description |
|-------|------|-------------|
| `index` | `int` | Choice index. |
| `message` | `CanonicalMessage` | The assistant message. |
| `finish_reason` | `CanonicalFinishReason` | Why the model stopped. |
| `logprobs` | `*CanonicalLogprobs` | Optional log-probability data. |

### CanonicalLogprobs

| Field | Type | Description |
|-------|------|-------------|
| `content` | `[]CanonicalLogprobToken` | Per-token logprob entries. |

### CanonicalLogprobToken

| Field | Type | Description |
|-------|------|-------------|
| `token` | `string` | The token string. |
| `logprob` | `float64` | Log probability. |
| `bytes` | `[]byte` | Optional raw bytes. |
| `top_logprobs` | `[]CanonicalTopLogprob` | Top-N alternatives from the provider. |

### CanonicalTopLogprob

| Field | Type | Description |
|-------|------|-------------|
| `token` | `string` | Alternative token. |
| `logprob` | `float64` | Its log probability. |

---

## CanonicalMessage

A single message in the conversation with a unified role and content representation.

| Field | Type | Description |
|-------|------|-------------|
| `role` | `string` | `"system"`, `"user"`, `"assistant"`, or `"tool"`. |
| `content` | `[]CanonicalContentBlock` | Unified content blocks. Text-only messages have a single `text` block. |
| `name` | `string` | Optional author/tool name. |
| `tool_calls` | `[]CanonicalToolCall` | Populated on assistant messages when the model invoked tools. |
| `tool_call_id` | `string` | Populated on tool messages to link back to the call. |
| `thinking` | `*CanonicalThinking` | Present when the model emits chain-of-thought / reasoning content. |

**Example**

```json
{
  "role": "assistant",
  "content": [
    {"type": "text", "text": "Let me check that for you."}
  ],
  "tool_calls": [
    {
      "id": "call_abc",
      "type": "function",
      "function": {"name": "get_weather", "arguments": "{\"city\":\"SF\"}"}
    }
  ],
  "thinking": {
    "type": "thinking",
    "thinking": "I should call the weather tool.",
    "signature": "eyJhbGci..."
  }
}
```

---

## CanonicalContentBlock

A single piece of content inside a message. Supports text, image, tool input, tool result, and thinking.

| Field | Type | Description |
|-------|------|-------------|
| `type` | `string` | Block kind: `"text"`, `"image"`, `"tool_use"`, `"tool_result"`, `"thinking"`. |
| `text` | `string` | Populated when `type == "text"`. |
| `source` | `*CanonicalContentSource` | Populated when `type == "image"`. |
| `tool_use_id` | `string` | Populated when `type == "tool_use"` or `"tool_result"`. |
| `name` | `string` | Tool name (for `tool_use`). |
| `input` | `map[string]interface{}` | Tool input arguments (for `tool_use`). |
| `output` | `string` | Tool output / result (for `tool_result`). |
| `thinking` | `string` | Reasoning/thinking text (for `thinking` block). |
| `signature` | `string` | Optional verification signature for thinking blocks. |

### CanonicalContentSource

| Field | Type | Description |
|-------|------|-------------|
| `type` | `string` | `"base64"` or `"url"`. |
| `media_type` | `string` | MIME type, e.g. `image/jpeg`. |
| `data` | `string` | Base64 image data or URL string. |

---

## CanonicalTool

A tool definition available to the model. Mirrors OpenAI's function-calling schema with room for provider extensions in `metadata`.

| Field | Type | Description |
|-------|------|-------------|
| `type` | `string` | Always `"function"`. |
| `function` | `CanonicalFunction` | Tool name, description, and parameter schema. |
| `metadata` | `map[string]interface{}` | Provider-specific extras. |

### CanonicalFunction

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | Tool identifier. |
| `description` | `string` | Human-readable description. |
| `parameters` | `map[string]interface{}` | JSON Schema object. |

---

## CanonicalToolCall

A tool invocation emitted by the model inside an assistant message.

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Unique call identifier (provider-supplied). |
| `type` | `string` | Always `"function"`. |
| `function` | `CanonicalFunctionCall` | Name and JSON-stringified arguments. |

### CanonicalFunctionCall

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | Tool name. |
| `arguments` | `string` | JSON-encoded arguments object. |

---

## CanonicalToolResult

A tool response message. Used as a follow-up `user`-role message carrying the tool output.

| Field | Type | Description |
|-------|------|-------------|
| `tool_call_id` | `string` | Links to the original `CanonicalToolCall.id`. |
| `name` | `string` | Tool name (redundant but kept for convenience). |
| `content` | `string` | Plain-text tool output. |
| `is_error` | `bool` | `true` if the tool execution failed. |

---

## CanonicalUsage

Token usage counters for cost tracking and rate-limit enforcement.

| Field | Type | Description |
|-------|------|-------------|
| `prompt_tokens` | `int` | Input tokens. |
| `completion_tokens` | `int` | Output tokens. |
| `total_tokens` | `int` | Sum of prompt + completion. |
| `cache_read_tokens` | `int` | Cached prompt tokens (e.g. Anthropic prompt caching). |
| `cache_creation_tokens` | `int` | Tokens written to cache. |
| `completion_tokens_details` | `map[string]int` | Breakdown (e.g. `reasoning_tokens`). |
| `prompt_tokens_details` | `map[string]int` | Provider-specific prompt breakdown. |

**Example**

```json
{
  "prompt_tokens": 42,
  "completion_tokens": 17,
  "total_tokens": 59,
  "cache_read_tokens": 20,
  "completion_tokens_details": {"reasoning_tokens": 8}
}
```

---

## CanonicalStreamChunk

A single chunk in a streaming response. Chunks are concatenated by the gateway into the final canonical response shape for consumers that request `stream: false`, or forwarded raw to the client when `stream: true`.

| Field | Type | Description |
|-------|------|-------------|
| `index` | `int` | Choice index for this chunk. |
| `delta` | `CanonicalDelta` | Incremental content or tool call fragment. |
| `finish_reason` | `*CanonicalFinishReason` | Present only on the final chunk. |
| `usage` | `*CanonicalUsage` | Present only on the final chunk (if the provider reports per-request usage at the end). |
| `model` | `string` | Model that produced the chunk. |
| `provider` | `string` | Provider name (e.g. `"openai"`, `"anthropic"`). |
| `metadata` | `map[string]interface{}` | Extra chunk-level data. |

### CanonicalDelta

| Field | Type | Description |
|-------|------|-------------|
| `content` | `[]CanonicalContentBlock` | Incremental content blocks. |
| `role` | `string` | Role, usually `"assistant"` on the first chunk. |
| `tool_calls` | `[]CanonicalToolCallDelta` | Incremental tool call fragments. |
| `thinking` | `*CanonicalThinkingDelta` | Streaming reasoning content. |

### CanonicalToolCallDelta

| Field | Type | Description |
|-------|------|-------------|
| `index` | `int` | Which tool call this fragment belongs to. |
| `id` | `string` | Call id (usually present on the first fragment). |
| `function` | `CanonicalFunctionCallDelta` | Name and argument fragments. |

### CanonicalFunctionCallDelta

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | Tool name (first fragment). |
| `arguments` | `string` | JSON argument fragment (concatenate across chunks). |

### CanonicalThinkingDelta

| Field | Type | Description |
|-------|------|-------------|
| `thinking` | `string` | Reasoning text fragment. |
| `signature` | `string` | Signature fragment (if streamed). |

---

## CanonicalThinking

Reasoning / chain-of-thought content emitted by models that support it (e.g. Claude Opus, o-series). Kept separate from `content` so the gateway can surface it independently in the dashboard.

| Field | Type | Description |
|-------|------|-------------|
| `type` | `string` | Always `"thinking"`. |
| `thinking` | `string` | The reasoning text. |
| `signature` | `string` | Optional verification signature. |

---

## CanonicalFinishReason

Why the model stopped generating.

| Value | Description |
|-------|-------------|
| `"stop"` | Natural end of output. |
| `"length"` | Output hit `max_tokens`. |
| `"tool_calls"` | Model emitted one or more tool calls. |
| `"content_filter"` | Content filter triggered (e.g. OpenAI). |
| `"interrupt"` | Stream interrupted by the client. |
| `"unknown"` | Provider returned an unrecognizable reason. |

---

## CanonicalCitation

A citation / source reference returned by the model (e.g. RAG grounded outputs).

| Field | Type | Description |
|-------|------|-------------|
| `type` | `string` | Citation kind, e.g. `"url"`, `"document"`. |
| `url` | `string` | Source URL, if applicable. |
| `title` | `string` | Source title. |
| `content` | `string` | Excerpt from the source. |
| `start_index` | `int` | Start position in the generated text. |
| `end_index` | `int` | End position in the generated text. |
| `metadata` | `map[string]interface{}` | Provider-specific citation fields. |

---

## CanonicalStructuredOutput

Request for a constrained / JSON-schema response format.

| Field | Type | Description |
|-------|------|-------------|
| `type` | `string` | `"json_object"` or `"json_schema"`. |
| `json_schema` | `*CanonicalJsonSchema` | Full schema definition (when `type == "json_schema"`). |

### CanonicalJsonSchema

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | Schema name. |
| `description` | `string` | Schema description. |
| `schema` | `map[string]interface{}` | JSON Schema object. |
| `strict` | `bool` | Whether to enforce strict mode. |

---

## CanonicalError

A provider-agnostic error shape. Adapters map upstream errors into this structure so the gateway can return consistent error responses and the dashboard can aggregate failures.

| Field | Type | Description |
|-------|------|-------------|
| `code` | `string` | Conductor error code (e.g. `"provider_unreachable"`, `"rate_limit"`, `"invalid_request"`). |
| `message` | `string` | Human-readable message. |
| `provider` | `string` | Upstream provider name. |
| `provider_code` | `string` | Provider's own error code, if available. |
| `provider_message` | `string` | Provider's own error message, if available. |
| `status` | `int` | HTTP status to return to the client. |
| `retryable` | `bool` | Whether Conductor should retry on another provider. |
| `metadata` | `map[string]interface{}` | Extra error context (e.g. retry-after header). |

**Example**

```json
{
  "code": "rate_limit",
  "message": "Provider rate limit exceeded",
  "provider": "openai",
  "provider_code": "rate_limit_exceeded",
  "provider_message": "You have exceeded your current quota...",
  "status": 429,
  "retryable": true,
  "metadata": {"retry_after": "1.5"}
}
```

---

## CanonicalProviderCapabilities

Declares what features a given provider/model combination supports. Populated from `internal/provider/metadata.go` `Capabilities` and resolved per-model by the adapter's `SupportsModel()` implementation.

| Field | Type | Description |
|-------|------|-------------|
| `streaming` | `bool` | Supports SSE streaming. |
| `vision` | `bool` | Accepts image content blocks. |
| `reasoning` | `bool` | Emits `CanonicalThinking` content. |
| `tool_calling` | `bool` | Supports `CanonicalTool` / `CanonicalToolCall`. |
| `structured_output` | `bool` | Supports `CanonicalStructuredOutput`. |
| `long_context` | `bool` | Context window > 128k tokens. |
| `embeddings` | `bool` | Has an embeddings endpoint. |
| `images` | `bool` | Can generate images (future). |
| `audio` | `bool` | Supports audio input/output (future). |
| `functions` | `bool` | Legacy function-calling format (pre-tools). |

**Example**

```json
{
  "streaming": true,
  "vision": true,
  "reasoning": false,
  "tool_calling": true,
  "structured_output": true,
  "long_context": true,
  "embeddings": true,
  "images": false,
  "audio": false,
  "functions": true
}
```

---

## Mapping Notes

### From OpenAI → Canonical

- `choices[i].message.content` (string) → `[{type: "text", text: "..."}]`
- `choices[i].message.tool_calls` → `tool_calls` + `content` blocks of type `tool_use`
- `response_format.type == "json_object"` → `CanonicalStructuredOutput{type: "json_object"}`
- `usage.completion_tokens_details.reasoning_tokens` → `usage.completion_tokens_details`
- Error codes: `"invalid_api_key"` → `code: "authentication"`, `"rate_limit_exceeded"` → `code: "rate_limit"`, etc.

### From Anthropic → Canonical

- `content` blocks map 1:1 by `type` (`text`, `tool_use`, `tool_result`, `thinking`)
- `message.stop_reason == "end_turn"` → `FinishReason: "stop"`
- `message.stop_reason == "tool_use"` → `FinishReason: "tool_calls"`
- Prompt caching fields (`cache_read_input_tokens`, `cache_creation_input_tokens`) → `usage.cache_read_tokens` / `usage.cache_creation_tokens`
- Error `type` values map: `"missing_permission"` → `code: "permission"`, `"invalid_request_error"` → `code: "invalid_request"`, etc.

### From Ollama → Canonical

- Ollama does not stream `finish_reason`; the adapter emits `stop` when the stream ends normally.
- Ollama has no native tool calling in all variants; `SupportsModel()` must reflect this.
- Embeddings are a separate endpoint; the adapter calls it directly rather than through chat.

### From NVIDIA NIM → Canonical

- NIM follows the OpenAI chat completions shape closely; most mapping is 1:1.
- NIM may return `model` in the response that differs from the request; preserve the request model in canonical `model`.
