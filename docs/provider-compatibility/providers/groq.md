# Provider: Groq

## Overview
Groq provides fast inference for open-source language models via an OpenAI-compatible API. Conductor uses the `openaibase.Base` adapter, meaning all standard OpenAI chat completion features work out of the box. Groq is known for very low latency and supports models from Meta (Llama), Mistral, and other open-source providers.

## Authentication
Groq uses Bearer token authentication. Set `GROQ_API_KEY` as an environment variable or configure it in `config.yaml` under `providers.groq.api_key`. The key is sent as `Authorization: Bearer <key>`.

## Base URL
Default: `https://api.groq.com/openai/v1`

Override with the `base_url` field in provider configuration.

## Headers
- `Authorization: Bearer <api_key>` — required
- `Content-Type: application/json` — required

## Endpoints
- `POST /chat/completions` — full implementation via `openaibase.Base`
- `POST /embeddings` — **not available** on Groq; returns error
- `GET /models` — full implementation via `openaibase.Base.ListModels`
- `GET /models` (health) — used by `HealthCheck`

## Supported APIs
- Chat Completion: ✅
- Streaming: ✅
- Embeddings: 🔲 (stub — Groq does not offer embeddings)
- List Models: ✅

## Streaming
Fully supported. Conductor sets `stream_options.include_usage: true` automatically. Groq's streaming format matches OpenAI's exactly, so chunks pass through with minimal transformation.

## Tool Calling
✅ Supported. Groq supports tool calling on compatible models (Llama 3.1 and later, Mixtral 8x7b). Conductor forwards tool definitions and normalizes tool call responses through the standard `NormalizeChoices` path.

## Parallel Tool Calling
✅ Supported on models that support tool calling. Groq's Llama 3.1 models support parallel tool execution.

## Structured Output
🟡 Partial. Groq's support for `response_format` with JSON mode varies by model. Conductor forwards the field, but not all Groq models honor it. Test with your target model.

## JSON Mode
🟡 Partial. Similar to structured output — JSON mode works on some models but not all. Conductor forwards `response_format` unchanged.

## Thinking / Reasoning
❌ Not supported. Groq does not offer reasoning/thinking models. Conductor's `reasoning` and `reasoning_effort` fields are forwarded but have no effect.

## Vision
❌ Not supported. Groq currently does not offer vision-capable models through its OpenAI-compatible endpoint.

## Embeddings
❌ Not supported. Groq does not provide an embeddings API. Conductor returns a `BadRequest` error for embedding requests.

## Usage Object
✅ Full usage reporting. Groq returns `prompt_tokens`, `completion_tokens`, and `total_tokens` in the standard OpenAI format. Streaming usage is included in the final chunk when `stream_options.include_usage` is set.

## Finish Reasons
✅ All standard finish reasons:
- `stop` — natural completion
- `length` — exceeded max tokens
- `tool_calls` — tool calls issued
- `content_filter` — content filtered (rare on Groq)

## Error Format
OpenAI-compatible error format. Conductor parses `error.type`, `error.message`, and `error.code`. Groq may return additional error codes for model-specific limitations (e.g., a model not supporting a requested feature).

## Rate Limits
Rate limits are enforced by Groq and returned as HTTP 429 responses. Groq has generous rate limits compared to other providers, but they vary by model and plan. Conductor surfaces these as `rate_limit_error` type errors. Backoff and retry are handled at the router level.

## Known Quirks
- **Limited model selection.** Groq offers a smaller catalog than OpenAI or Anthropic. Only models listed on Groq's platform are available.
- **No embeddings.** Any embedding request will fail.
- **No vision.** Vision-capable models are not available on Groq.
- **Fast inference.** Groq's LPU inference is notably fast; streaming tokens per second can be significantly higher than other providers.
- **Some models lack tool calling.** Not all Groq models support tools; check the model documentation.
- **Context window varies.** Different models have different context limits (e.g., 8K, 32K, 128K). Conductor does not enforce these; the upstream returns an error if exceeded.

## Compatibility Notes
Groq is fully OpenAI-compatible, so Conductor's `openaibase.Base` adapter works without any provider-specific translation. This is the same adapter used by OpenAI, DeepSeek, OpenRouter, and Ollama Cloud. The main differences are the model catalog and the absence of embeddings and vision.

## Production Readiness
✅ Production-ready for chat and streaming. Groq is well-suited for low-latency use cases. Tool calling works on supported models. Embeddings and vision are not available.

## Open Issues
- No open issues specific to the Groq adapter. General issues with the OpenAI-compatible base apply.
