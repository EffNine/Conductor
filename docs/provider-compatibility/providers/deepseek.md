# Provider: DeepSeek

## Overview
DeepSeek provides Chinese-origin language models with an OpenAI-compatible API. Conductor uses the `openaibase.Base` adapter, meaning all standard OpenAI chat completion features work. DeepSeek offers both standard chat models (`deepseek-chat`) and reasoning models (`deepseek-reasoner`, DeepSeek-R1 series). The reasoning models have a notable quirk: they return reasoning content in a separate `reasoning_content` field rather than in `content`.

## Authentication
DeepSeek uses Bearer token authentication. Set `DEEPSEEK_API_KEY` as an environment variable or configure it in `config.yaml` under `providers.deepseek.api_key`. The key is sent as `Authorization: Bearer <key>`.

## Base URL
Default: `https://api.deepseek.com/v1`

Override with the `base_url` field in provider configuration.

## Headers
- `Authorization: Bearer <api_key>` — required
- `Content-Type: application/json` — required

## Endpoints
- `POST /chat/completions` — full implementation via `openaibase.Base`
- `POST /embeddings` — **not available** on DeepSeek; returns error
- `GET /models` — full implementation via `openaibase.Base.ListModels`
- `GET /models` (health) — used by `HealthCheck`

## Supported APIs
- Chat Completion: ✅
- Streaming: ✅
- Embeddings: 🔲 (stub — DeepSeek does not offer embeddings via this endpoint)
- List Models: ✅

## Streaming
Fully supported. Conductor sets `stream_options.include_usage: true` automatically. DeepSeek's streaming format matches OpenAI's. For reasoning models, thinking tokens are streamed in `reasoning_content` fields on delta chunks.

## Tool Calling
✅ Supported on `deepseek-chat` and newer models. DeepSeek-R1 reasoning models may have limited or no tool calling support. Conductor forwards tool definitions and normalizes responses through the standard path.

## Parallel Tool Calling
✅ Supported on models that support tool calling (primarily `deepseek-chat`).

## Structured Output
🟡 Partial. DeepSeek supports JSON mode on some models. Conductor forwards `response_format` but behavior varies by model. Test with your target model.

## JSON Mode
🟡 Partial. JSON mode is supported on `deepseek-chat` but may not work on reasoning models. Conductor forwards the request unchanged.

## Thinking / Reasoning
✅ Supported. DeepSeek has two categories of reasoning models:
- **DeepSeek-R1 / DeepSeek-V3 reasoning models**: Return `reasoning_content` containing the chain-of-thought, separate from `content`.
- **deepseek-chat**: Standard chat model without explicit reasoning mode.

Conductor normalizes reasoning content via `Message.Normalize()`, copying `reasoning_content` into `content` when the latter is empty. This ensures clients that only read `content` still receive the full response from reasoning models.

Thinking mode is controlled via `reasoning_effort` and the `reasoning` field. DeepSeek's reasoning models require specific kwargs for thinking mode; Conductor forwards these through the standard fields.

## Vision
❌ Not supported. DeepSeek's OpenAI-compatible API does not expose vision capabilities.

## Embeddings
❌ Not supported. DeepSeek does not offer embeddings through their OpenAI-compatible endpoint. Conductor returns a `BadRequest` error for embedding requests.

## Usage Object
✅ Full usage reporting. DeepSeek returns `prompt_tokens`, `completion_tokens`, and `total_tokens`. Reasoning models may also return `reasoning_tokens` in `completion_tokens_details`. Streaming usage is included in the final chunk.

## Finish Reasons
✅ Standard finish reasons:
- `stop` — natural completion
- `length` — exceeded max tokens
- `tool_calls` — tool calls issued
- `content_filter` — content filtered

## Error Format
OpenAI-compatible error format. Conductor parses `error.type`, `error.message`, and `error.code`. DeepSeek may return specific error codes for rate limiting and model access.

## Rate Limits
Rate limits are enforced by DeepSeek and returned as HTTP 429 responses. DeepSeek's rate limits are model-dependent and generally generous for their pricing tier. Conductor surfaces these as `rate_limit_error` type errors.

## Known Quirks
- **`reasoning_content` vs `content`.** Reasoning models return the chain-of-thought in `reasoning_content` and the final answer may be empty or minimal in `content`. Conductor's `NormalizeChoices` copies `reasoning_content` into `content` when `content` is empty, so clients see the full response.
- **Thinking mode kwargs.** Reasoning models may require specific request parameters to enable thinking. Conductor forwards `reasoning_effort` and `reasoning` fields; if the model needs additional kwargs, they must be passed via `chat_template_kwargs`.
- **No embeddings.** Embedding requests fail with a clear error.
- **No vision.** Vision is not available.
- **Model IDs differ.** DeepSeek uses IDs like `deepseek-chat` and `deepseek-reasoner`, not the full model names found on other platforms.

## Compatibility Notes
DeepSeek is fully OpenAI-compatible, so Conductor's `openaibase.Base` adapter works without provider-specific translation. The main adaptation is the reasoning content normalization (`reasoning_content` → `content`), which is handled in the base type layer, not the provider adapter.

## Production Readiness
✅ Production-ready for chat and streaming. Reasoning models work correctly with Conductor's normalization. Tool calling works on `deepseek-chat`. Embeddings and vision are not available.

## Open Issues
- No open issues specific to the DeepSeek adapter. The reasoning content normalization is tested but edge cases with mixed content (both `reasoning_content` and `content` populated) should be verified.
