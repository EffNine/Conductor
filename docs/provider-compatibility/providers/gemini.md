# Provider: Google Gemini

## Overview
Google Gemini is accessed through Conductor's OpenAI-compatible base adapter. Gemini provides an OpenAI-compatible endpoint at `generativelanguage.googleapis.com/v1beta/openai/`, which Conductor targets via the `openaibase.Base`. This means chat completions, embeddings, and model listing work as OpenAI-compatible requests. However, Gemini's native REST API has a different shape, and Conductor does not implement a native Gemini adapter.

## Authentication
Gemini uses API keys passed as query parameters (`?key=...`) or via the `Authorization: Bearer` header. Conductor sends the key as `Authorization: Bearer <key>` via the standard OpenAI-compatible base. Set `GEMINI_API_KEY` as an environment variable or configure it in `config.yaml` under `providers.gemini.api_key`.

## Base URL
Default: `https://generativelanguage.googleapis.com/v1beta/openai`

The OpenAI-compatible endpoint is at `/v1beta/openai/` under the Gemini API host.

Override with the `base_url` field in provider configuration.

## Headers
- `Authorization: Bearer <api_key>` — required
- `Content-Type: application/json` — required

## Endpoints
- `POST /chat/completions` — implemented via `openaibase.Base`
- `POST /embeddings` — implemented via `openaibase.Base`
- `GET /models` — implemented via `openaibase.Base` (returns Gemini model IDs)
- `GET /models` (health) — used by `HealthCheck`

## Supported APIs
- Chat Completion: ✅
- Streaming: ✅
- Embeddings: ✅
- List Models: ✅

## Streaming
Fully supported through the OpenAI-compatible streaming endpoint. Conductor sets `stream_options.include_usage: true` automatically. Gemini's streaming response shape matches OpenAI's, so chunks pass through with minimal transformation.

## Tool Calling
🟡 Partial. Gemini supports function calling through its native API, but the OpenAI-compatible endpoint has limited tool calling support. Conductor forwards tool definitions and receives tool calls, but complex tool schemas and parallel tool calling may not work consistently across all Gemini models.

## Parallel Tool Calling
🟡 Partial. Depends on the specific Gemini model and the OpenAI-compatible endpoint's support. Simple cases work; complex parallel calls may fall back to sequential behavior.

## Structured Output
🟡 Partial. Gemini supports JSON mode through its native API, but the OpenAI-compatible endpoint's support for `response_format` is model-dependent. Conductor forwards the `response_format` field, but behavior varies by model.

## JSON Mode
🟡 Partial. Similar to structured output — forwarding depends on the upstream model's support for the OpenAI-compatible JSON mode parameter.

## Thinking / Reasoning
❌ Not supported. Gemini models do not expose a reasoning/thinking mode analogous to OpenAI's o-series or Anthropic's extended thinking. Conductor's `reasoning` and `reasoning_effort` fields are forwarded but have no effect on Gemini models.

## Vision
✅ Supported. Gemini is a native multimodal model. Image content is forwarded as `image_url` content parts through the OpenAI-compatible endpoint. Both base64-encoded and URL-based images are supported.

## Embeddings
✅ Supported. Gemini provides embedding models (e.g., `text-embedding-004`, `text-embedding-005`) accessible through the OpenAI-compatible `/embeddings` endpoint. Static pricing is configured for `gemini-1.5-pro`, `gemini-1.5-flash`, and `gemini-1.5-flash-8b`.

## Audio
❌ Not supported. Gemini's audio capabilities are not exposed through the OpenAI-compatible endpoint.

## Usage Object
✅ Full usage reporting. Gemini returns token usage in the standard OpenAI format (`prompt_tokens`, `completion_tokens`, `total_tokens`). For streaming, the final chunk includes usage when `stream_options.include_usage` is set.

## Finish Reasons
✅ Supported with standard OpenAI finish reasons:
- `stop` — natural completion
- `length` — exceeded max tokens
- `tool_calls` — tool calls issued
- `content_filter` — content filtered

## Error Format
OpenAI-compatible error format. Conductor parses the upstream `error.type`, `error.message`, and `error.code` fields. Gemini may return additional error codes specific to quota and model access; these are surfaced as-is in the error message.

## Rate Limits
Rate limits are enforced by Google and returned as HTTP 429 responses. Gemini has separate rate limits per model and per API key. Conductor surfaces these as `rate_limit_error` type errors. Backoff and retry are handled at the router level.

## Known Quirks
- **OpenAI-compatible endpoint is a subset.** Gemini's native API has features not available through the OpenAI-compatible endpoint (e.g., advanced function calling, safety settings, caching). Conductor only accesses what the OpenAI-compatible endpoint exposes.
- **Model IDs differ.** Gemini model IDs use Google's naming convention (e.g., `gemini-1.5-pro`, `gemini-2.0-flash`) rather than OpenAI's. Use the full model ID from `/v1/models`.
- **Tool calling is limited.** The OpenAI-compatible endpoint's tool support is narrower than Gemini's native function calling. Complex schemas may fail.
- **No native thinking mode.** Gemini does not have a reasoning/thinking feature; `reasoning_effort` and `reasoning` fields have no effect.
- **Context window is large.** Gemini supports up to 1M tokens on some models, but the OpenAI-compatible endpoint may have lower effective limits.

## Compatibility Notes
Gemini is accessed through the same `openaibase.Base` as OpenAI, Groq, DeepSeek, and OpenRouter. This means it benefits from all the base adapter's features (streaming, usage normalization, error handling) but is also limited to whatever the Gemini OpenAI-compatible endpoint supports. It is not a full Gemini API integration.

## Production Readiness
🟡 Production-ready for basic chat and embedding workloads. Tool calling and structured output work for simple cases but may have edge cases. Not recommended for workloads requiring Gemini-specific features (advanced function calling, safety settings, etc.).

## Open Issues
- Tool calling edge cases with complex nested schemas are untested.
- No native Gemini API adapter exists; features unique to Gemini's native API are unavailable.
- Dynamic model listing may include models not accessible via the OpenAI-compatible endpoint.
