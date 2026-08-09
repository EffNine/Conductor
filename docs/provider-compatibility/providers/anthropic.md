# Provider: Anthropic

## Overview
Anthropic's Claude models are accessed through Conductor's native Messages API adapter. This is **not** an OpenAI-compatible endpoint — Conductor translates between the OpenAI request schema and Anthropic's native `/v1/messages` format. The adapter fully implements chat completion and streaming, with translation for messages, tools, images, and usage.

## Authentication
Anthropic uses API keys sent via the `x-api-key` header (not `Authorization: Bearer`). Set `ANTHROPIC_API_KEY` as an environment variable or configure it in `config.yaml` under `providers.anthropic.api_key`.

## Base URL
Default: `https://api.anthropic.com`

The full endpoint path is `https://api.anthropic.com/v1/messages` for chat and `https://api.anthropic.com/v1/messages` for streaming.

Override with the `base_url` field in provider configuration.

## Headers
- `x-api-key: <api_key>` — required
- `anthropic-version: 2023-06-01` — required (hardcoded in adapter)
- `Content-Type: application/json` — required

## Endpoints
- `POST /v1/messages` — chat completion (non-streaming), fully implemented
- `POST /v1/messages` (with `stream: true`) — streaming, fully implemented
- `GET /v1/models` — **not implemented**; Conductor uses a static model list
- `POST /v1/embeddings` — **not available** on Anthropic; returns error

## Supported APIs
- Chat Completion: ✅
- Streaming: ✅
- Embeddings: 🔲 (stub — returns `ErrorTypeInvalidRequest`)
- List Models: 🔲 (static list only; no dynamic listing)

## Streaming
Fully supported. Conductor translates Anthropic's SSE stream into OpenAI-compatible stream chunks. The adapter parses `message_start`, `content_block_delta`, and `message_delta` events and emits chunks with the standard `chat.completion.chunk` object shape. Usage is emitted in the final `message_delta` chunk. Stop reasons are mapped: Anthropic's `end_turn` becomes `stop`.

## Tool Calling
✅ Supported. Conductor translates OpenAI-style `tools` and `tool_choice` into Anthropic's native tool format. Tool definitions are converted from OpenAI's `FunctionDef` schema to Anthropic's `tool` blocks. Tool calls in responses are parsed and normalized back to OpenAI's `ToolCall` format.

Note: Anthropic's tool schema differs from OpenAI's in subtle ways (e.g., required field handling, nested object definitions). The adapter performs best-effort translation; complex schemas may lose some constraints.

## Parallel Tool Calling
🟡 Partial. Anthropic supports multiple tool calls in a single response, but the translation layer may not preserve all parallel call semantics perfectly. In practice, most tool calling workloads function correctly.

## Structured Output
🔲 Not supported. Anthropic does not have a native JSON mode equivalent to OpenAI's `response_format`. Conductor does not translate `response_format` for Anthropic requests; passing it has no effect on the upstream.

## JSON Mode
🔲 Not supported. Same limitation as structured output above.

## Thinking / Reasoning
✅ Supported for Claude 3.7 Sonnet and later with extended thinking. Conductor forwards reasoning configuration through the `reasoning` field. Anthropic's extended thinking returns `thinking` content blocks which Conductor attempts to include in the normalized response. The adapter maps `max_tokens` in the `ReasoningConfig` to Anthropic's `max_tokens` when present.

## Vision
✅ Supported. Image content is translated from OpenAI's `image_url` format to Anthropic's `source` block format (base64 or URL). Both `data:` URLs and direct URLs are handled by `imageURLToAnthropicBlock`.

## Embeddings
❌ Not supported. Anthropic does not offer an embeddings API. Conductor returns a `BadRequest` error with `ErrorTypeInvalidRequest` and message `"Anthropic does not provide embeddings"`.

## Usage Object
✅ Full usage reporting. Anthropic returns `input_tokens` and `output_tokens` in both the `message_start` and `message_delta` stream events. Conductor maps these to `prompt_tokens` and `completion_tokens`. The `total_tokens` is computed as the sum. Streaming usage is emitted in the final chunk.

## Finish Reasons
✅ Supported with mapping:
- Anthropic `end_turn` → `stop`
- Anthropic `max_tokens` → `length`
- Anthropic `tool_use` → `tool_calls`
- Anthropic `stop_sequence` → `stop`

## Error Format
Anthropic-specific error format. Conductor parses the upstream response shape:
```json
{ "error": { "message": "...", "type": "..." } }
```
Error types are mapped to Conductor's normalized types:
- `authentication_error` — 401
- `rate_limit_error` — 429
- `invalid_request_error` — 400
- `server_error` — 5xx

If the error body cannot be parsed as Anthropic's format, a generic error is returned.

## Rate Limits
Rate limits are enforced by Anthropic and returned as HTTP 429 responses. Conductor surfaces these as `rate_limit_error` type errors. Anthropic's rate limits are per-token and per-request; the upstream error message typically includes retry-after information. Backoff and retry are handled at the router level via the fallback chain.

## Known Quirks
- **Different message format.** Anthropic uses a conversation history model where each message has a `role` (`user` or `assistant`) and `content` (string or array of blocks). Conductor translates OpenAI's `system` role into Anthropic's top-level `system` parameter.
- **`developer` role not supported.** If a request includes `role: "developer"`, Conductor does not remap it (unlike NVIDIA NIM). Use `system` role for system messages.
- **Different tool schema.** Anthropic's tool definition format differs from OpenAI's. Complex nested schemas may not translate perfectly.
- **Different error format.** Anthropic errors use `error.type` and `error.message` rather than OpenAI's shape.
- **No embeddings API.** Any embedding request to Anthropic will fail with a clear error.
- **Static model list.** Conductor does not fetch models dynamically from Anthropic; it uses a hardcoded list of known Claude models.

## Compatibility Notes
The Anthropic adapter is a full translation layer, not a passthrough. It converts OpenAI request shapes to Anthropic Messages API shapes and normalizes responses back to OpenAI format. This means:
- Features supported by both APIs work correctly
- Features unique to one API (e.g., OpenAI's `logit_bias`, Anthropic's extended thinking) may have limited or no support
- The adapter is well-tested but some edge cases in tool schemas may surface differences

## Production Readiness
✅ Production-ready for chat and streaming. The adapter has been used in production with Claude 3.5 Sonnet and Claude 3.7 Sonnet. Tool calling and vision are functional. Embeddings and dynamic model listing are not available.

## Open Issues
- Dynamic model listing from Anthropic API is not implemented (stub with static list).
- `logit_bias` from OpenAI requests is silently dropped during translation.
- Some edge cases in tool schema translation for deeply nested objects are untested.
