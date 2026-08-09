# Provider: Z.AI

## Overview

Z.AI (formerly 01.AI) is a Chinese AI lab that develops large language models including the Yi series and GLM-4 models hosted on their platform. Conductor's Z.AI adapter is a **stub** — the provider is registered and can advertise models via a static list, but request handling is not yet implemented.

Z.AI's API is **OpenAI-compatible** at `https://z.ai/v1`.

## Authentication

Z.AI authenticates via Bearer token. Obtain an API key from the Z.AI console.

```bash
export ZAI_API_KEY=zai-xxx
```

Conductor auto-enables the provider when `ZAI_API_KEY` is set.

## Base URL

`https://z.ai/v1`

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | ✅ Yes | `Bearer <ZAI_API_KEY>` |
| `Content-Type` | ✅ Yes | `application/json` |

## Endpoints

| Conductor Endpoint | Supported |
|--------------------|-----------|
| `POST /v1/chat/completions` | 🔲 Stub |
| `GET /v1/models` | ✅ (static list) |
| `POST /v1/embeddings` | 🔲 Stub |

## Supported APIs

- Chat Completion: 🔲
- Streaming: 🔲
- Embeddings: 🔲
- List Models: ✅ (static config only)

## Streaming

Z.AI supports SSE streaming at `/v1/chat/completions` with `stream: true`. The stream format follows the OpenAI convention. Full streaming support will be available once the adapter is implemented.

## Tool Calling

Z.AI models support tool calling with OpenAI-compatible function schemas. Support will be available once the adapter is implemented.

## Parallel Tool Calling

Supported by upstream models. Will be forwarded through once the adapter is implemented.

## Structured Output

Z.AI supports JSON mode via `response_format: { "type": "json_object" }`.

## JSON Mode

Supported via the standard `response_format` parameter.

## Thinking / Reasoning

Some Z.AI models (notably GLM variants) support extended thinking/reasoning modes. The adapter will pass through `reasoning_effort` and related fields once implemented. Note that NVIDIA NIM's GLM models require specific `chat_template_kwargs` — if Z.AI hosts similar models, similar handling may be needed.

## Vision

Z.AI's Yi series includes vision-capable models. Vision support will be available once the adapter is implemented.

## Embeddings

Z.AI provides embeddings via an OpenAI-compatible endpoint. Support is currently stubbed.

## Audio

Z.AI does not currently offer audio models. ❌

## Usage Object

Z.AI returns usage in OpenAI format:

```json
{
  "prompt_tokens": 30,
  "completion_tokens": 150,
  "total_tokens": 180
}
```

## Finish Reasons

Standard OpenAI finish reasons: `stop`, `length`, `tool_calls`.

## Error Format

Z.AI uses the OpenAI-compatible error shape:

```json
{
  "error": {
    "type": "invalid_request_error",
    "message": "model not found",
    "param": null,
    "code": null
  }
}
```

## Rate Limits

Rate limits vary by model and plan. Z.AI publishes limits in API response headers. Check the [Z.AI documentation](https://platform.z.ai/docs) for current limits.

## Known Quirks

- **Formerly 01.AI.** The provider rebranded from 01.AI to Z.AI; some documentation and URLs may still reference the old name.
- **Chinese-origin provider.** API documentation and support are primarily in Chinese. English-language resources are limited.
- **Model IDs use Z.AI naming.** Models are identified as `yi-lightning`, `yi-large`, `glm-4-plus`, etc. — not the Hugging Face model names.
- **Potential latency.** Depending on your location, API latency may be higher than Western-hosted providers due to network distance.

## Compatibility Notes

- Conductor registers Z.AI as a stub. When `ZAI_API_KEY` is set, the provider auto-enables.
- Configure a static model list for catalog visibility:

```yaml
providers:
  zai:
    enabled: true
    models:
      - "yi-lightning"
      - "yi-large"
      - "glm-4-plus"
```

- Since the API is OpenAI-compatible, implementing the adapter will be a thin `openaibase` wrapper.

## Production Readiness

🔲 **Not ready for production.** The adapter is a stub. Chat and streaming requests will return `not implemented`. Suitable for catalog advertising and route testing only.

## Open Issues

- [ ] Implement full OpenAI-compatible adapter using `openaibase.Base`
- [ ] Add static pricing map for Z.AI models
- [ ] Test thinking/reasoning model handling (GLM family)
- [ ] Verify embeddings endpoint contract
