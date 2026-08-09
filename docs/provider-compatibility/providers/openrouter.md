# Provider: OpenRouter

## Overview
OpenRouter is a multi-provider gateway that aggregates models from OpenAI, Anthropic, Google, Meta, DeepSeek, and many others behind a single OpenAI-compatible API. Conductor uses the `openaibase.Base` adapter, so all standard OpenAI features work. The key advantage is access to a vast model catalog through one API key; the key tradeoff is that behavior varies by underlying provider.

## Authentication
OpenRouter uses Bearer token authentication. Set `OPENROUTER_API_KEY` as an environment variable or configure it in `config.yaml` under `providers.openrouter.api_key`. The key is sent as `Authorization: Bearer <key>`.

## Base URL
Default: `https://openrouter.ai/api/v1`

Override with the `base_url` field in provider configuration.

## Headers
- `Authorization: Bearer <api_key>` — required
- `Content-Type: application/json` — required
- `HTTP-Referer` — optional but recommended (used by OpenRouter for analytics)
- `X-Title` — optional (used by OpenRouter for analytics)

## Endpoints
- `POST /chat/completions` — full implementation via `openaibase.Base`
- `POST /embeddings` — available for providers that support embeddings
- `GET /models` — full implementation via `openaibase.Base.ListModels` (returns full OpenRouter catalog)
- `GET /models` (health) — used by `HealthCheck`

## Supported APIs
- Chat Completion: ✅
- Streaming: ✅
- Embeddings: ✅ (depends on underlying provider)
- List Models: ✅

## Streaming
Fully supported. Conductor sets `stream_options.include_usage: true` automatically. OpenRouter's streaming format matches OpenAI's. Some upstream providers may emit additional SSE events (e.g., `openrouter` processing comments); Conductor skips empty events to prevent stream interruptions.

## Tool Calling
✅ Supported for models that have tool calling capability. OpenRouter forwards tool definitions to the underlying provider. Support varies by model — check the model's page on openrouter.ai for tool calling availability.

## Parallel Tool Calling
🟡 Partial. Depends on the underlying provider. OpenRouter forwards the request, but parallel tool call support is determined by the upstream model (e.g., works with GPT-4o and Claude 3.5 Sonnet, may not work with all open-source models).

## Structured Output
🟡 Partial. Depends on the underlying provider. OpenRouter forwards `response_format` to the upstream model. JSON mode works with models that support it (e.g., GPT-4o, Claude 3.5 Sonnet) but may not work with all models in the catalog.

## JSON Mode
🟡 Partial. Same as structured output — depends on the underlying provider's support.

## Thinking / Reasoning
✅ Supported for models that have reasoning capabilities. OpenRouter forwards `reasoning_effort` and `reasoning` fields. Models like DeepSeek-R1, Claude 3.7 Sonnet (extended thinking), and OpenAI o-series support reasoning through OpenRouter. Conductor normalizes reasoning content via `Message.Normalize()`.

## Vision
✅ Supported for vision-capable models. OpenRouter forwards image content through the standard `image_url` format. Availability depends on the underlying model (e.g., GPT-4o, Claude 3.5 Sonnet, Gemini models).

## Embeddings
✅ Supported for providers that offer embeddings. OpenRouter aggregates embedding models from various providers. Conductor forwards embedding requests; availability depends on the model selected.

## Audio
❌ Not supported. OpenRouter does not expose audio transcription or translation endpoints through their OpenAI-compatible API.

## Usage Object
✅ Full usage reporting. OpenRouter returns `prompt_tokens`, `completion_tokens`, and `total_tokens`. For streaming, usage is included in the final chunk when `stream_options.include_usage` is set. OpenRouter may also return `completion_tokens_details` with breakdowns by provider.

## Finish Reasons
✅ Standard finish reasons, mapped from upstream:
- `stop` — natural completion
- `length` — exceeded max tokens
- `tool_calls` — tool calls issued
- `content_filter` — content filtered

## Error Format
OpenAI-compatible error format. Conductor parses `error.type`, `error.message`, and `error.code`. OpenRouter may return additional error types specific to provider routing (e.g., model unavailable, rate limit on underlying provider).

## Rate Limits
Rate limits are enforced by OpenRouter and the underlying providers. HTTP 429 responses are surfaced as `rate_limit_error`. OpenRouter's rate limits are based on your account tier and the underlying provider's limits. Backoff and retry are handled at the router level.

## Known Quirks
- **Model IDs are provider-prefixed.** OpenRouter model IDs include the provider prefix (e.g., `openai/gpt-4o`, `anthropic/claude-3.5-sonnet`, `deepseek/deepseek-chat`). Use the full ID from `/v1/models`.
- **Behavior varies by underlying provider.** Each model in the catalog may have different capabilities, error formats, and quirks. Conductor's normalization handles most differences, but edge cases exist.
- **Pricing is per-model.** OpenRouter pricing varies by model and provider. Conductor has static pricing for a few popular models; use `cost.rates` to override or extend.
- **Some models are slower.** Open-source models routed through OpenRouter may have higher latency than direct provider access.
- **Provider-specific features may not work.** Features like Anthropic's extended thinking or OpenAI's function calling may not be fully supported depending on how OpenRouter forwards them.

## Compatibility Notes
OpenRouter is a proxy over the `openaibase.Base` adapter. It benefits from all the base adapter's features (streaming, usage normalization, error handling) but is limited by what OpenRouter's aggregation layer supports. Since OpenRouter itself is OpenAI-compatible, Conductor's adapter works without any provider-specific translation code.

## Production Readiness
✅ Production-ready for chat and streaming. OpenRouter is widely used and well-supported. Tool calling and vision work on capable models. Some edge cases with provider-specific features may require testing.

## Open Issues
- Static pricing is only configured for a subset of popular models. Most models rely on `cost.rates` configuration.
- Provider-specific edge cases (e.g., Anthropic tool schema differences when routed through OpenRouter) are not fully tested.
