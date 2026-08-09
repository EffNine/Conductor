# Provider: OpenAI

## Overview
OpenAI is the reference provider for Conductor. Its API is natively OpenAI-compatible, so Conductor uses the full `openaibase` adapter with no format translation. All standard OpenAI chat completion features are supported end-to-end, including streaming, tool calling, structured output, and reasoning models.

## Authentication
OpenAI uses Bearer token authentication. Set `OPENAI_API_KEY` as an environment variable or configure it in `config.yaml` under `providers.openai.api_key`. The key is sent as `Authorization: Bearer <key>` on every request.

## Base URL
Default: `https://api.openai.com/v1`

Override with the `base_url` field in provider configuration.

## Headers
- `Authorization: Bearer <api_key>` — required
- `Content-Type: application/json` — required

## Endpoints
- `POST /v1/chat/completions` — full implementation via `openaibase.Base.ChatCompletion` and `ChatCompletionStream`
- `POST /v1/embeddings` — full implementation via `openaibase.Base.Embeddings`
- `GET /v1/models` — full implementation via `openaibase.Base.ListModels`
- `GET /v1/models` (health) — used by `HealthCheck`

## Supported APIs
- Chat Completion: ✅
- Streaming: ✅
- Embeddings: ✅
- List Models: ✅

## Streaming
OpenAI streaming is fully supported. Conductor sets `stream_options.include_usage: true` automatically so that the final stream chunk includes token usage. Without this flag, the upstream omits usage and clients report `completion_tokens: 0`. Streaming uses a separate `http.Client` without the global timeout to avoid cutting off long reasoning model outputs mid-stream; cancellation is handled via the request context.

## Tool Calling
Native tool calling is supported. Tools are forwarded as-is in the `tools` field, and tool calls in responses are normalized through `apitypes.NormalizeChoices`. Both single and parallel tool calling work because the upstream OpenAI API handles both natively.

## Parallel Tool Calling
✅ Supported. OpenAI natively supports parallel tool calls in a single response choice. Conductor forwards the request without modification and normalizes the response.

## Structured Output
✅ Supported via `response_format: { "type": "json_schema", ... }`. Conductor forwards the `ResponseFormat` field from `ChatCompletionRequest` directly to the upstream. JSON mode (`{ "type": "json_object" }`) is also supported.

## JSON Mode
✅ Supported. Pass `response_format: { "type": "json_object" }` in the request. Conductor forwards this unchanged to the OpenAI API.

## Thinking / Reasoning
✅ Supported for o1, o3, and o3-mini series models. Conductor forwards `reasoning_effort` and `reasoning` fields to the upstream. Reasoning models return reasoning content that Conductor normalizes through `Message.Normalize()`, copying `reasoning` into `content` when the latter is empty.

## Vision
✅ Supported for GPT-4o, GPT-4 Turbo, and other vision-capable models. Multimodal content is forwarded as `[]ContentPart` arrays with `image_url` parts. Base64-encoded and URL-based images are both accepted.

## Embeddings
✅ Supported. Conductor forwards embedding requests to `/v1/embeddings` and returns the normalized response. Static pricing is configured for `text-embedding-3-small` and `text-embedding-3-large`.

## Audio
❌ Not supported. OpenAI's audio transcription and translation endpoints are not exposed through Conductor's gateway.

## Usage Object
✅ Full usage reporting. Conductor returns `prompt_tokens`, `completion_tokens`, and `total_tokens` from the upstream response. For streaming, the final chunk includes usage when `stream_options.include_usage` is set (which Conductor enforces automatically). `prompt_tokens_details` and `completion_tokens_details` (including `cached_tokens` and `reasoning_tokens`) are preserved when present.

## Finish Reasons
✅ All standard finish reasons are supported:
- `stop` — natural completion
- `length` — exceeded max tokens
- `tool_calls` — one or more tool calls
- `content_filter` — content filtered by upstream

Anthropic-style `end_turn` is not applicable (OpenAI does not use this value).

## Error Format
OpenAI-compatible error format. Conductor parses the upstream `error.type`, `error.message`, and `error.code` fields and maps them to Conductor's normalized `ProviderError` types:
- `authentication_error` — 401
- `invalid_request_error` — 400
- `rate_limit_error` — 429
- `server_error` — 5xx

## Rate Limits
Rate limits are enforced by OpenAI and returned as HTTP 429 responses. Conductor surfaces these as `rate_limit_error` type errors. Backoff and retry are handled at the router level via the fallback chain mechanism. No provider-specific rate limit metadata is exposed through the gateway.

## Known Quirks
- **`stream_options.include_usage` is required for usage in streams.** Without it, the upstream omits token counts from stream chunks. Conductor sets this automatically via `EnsureStreamUsage()`.
- **`developer` role is not supported.** OpenAI rejects messages with `role: "developer"`. Conductor does not remap this role for OpenAI (unlike NVIDIA NIM), so users must use `system` for system messages.
- **Reasoning models (o1/o3) have fixed temperature.** The upstream ignores `temperature` for these models; Conductor forwards it but the value has no effect.
- **Context window varies by model.** o1/o3 models have different context limits than GPT-4o. Conductor does not enforce context limits; the upstream returns an error if exceeded.

## Compatibility Notes
OpenAI is the reference implementation. All other OpenAI-compatible providers (Groq, DeepSeek, OpenRouter, Ollama, NVIDIA NIM, etc.) are validated against the same `openaibase.Base` contract. Conductor's normalization layer (`NormalizeChoices`, `Message.Normalize`) was primarily written to handle deviations in non-OpenAI providers; for OpenAI itself, the data flows through with minimal transformation.

## Production Readiness
✅ Production-ready. OpenAI is the most thoroughly tested provider in Conductor. The adapter is the reference implementation, pricing is statically configured, and health checks exercise the actual chat endpoint.

## Open Issues
- No open issues specific to the OpenAI adapter. General issues with streaming token details or tool calling edge cases should be tracked in the main repository.
