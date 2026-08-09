# Provider: Requesty

## Overview

Requesty is an AI inference API provider offering access to a variety of open and proprietary models at competitive prices. Conductor's Requesty adapter is a **stub** — the provider is registered and can advertise models via a static list, but request handling is not yet implemented.

Requesty's API is **OpenAI-compatible** at `https://requesty.ai/v1`.

## Authentication

Requesty authenticates via Bearer token. Obtain an API key from the Requesty dashboard.

```bash
export REQUESTY_API_KEY=rq-xxx
```

Conductor auto-enables the provider when `REQUESTY_API_KEY` is set.

## Base URL

`https://requesty.ai/v1`

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | ✅ Yes | `Bearer <REQUESTY_API_KEY>` |
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

Requesty supports SSE streaming at `/v1/chat/completions` with `stream: true`. The stream format follows the OpenAI convention. Full streaming support will be available once the adapter is implemented.

## Tool Calling

Tool calling support depends on the upstream model. Requesty passes through the OpenAI tool-calling schema. Support will be available once the adapter is implemented.

## Parallel Tool Calling

Supported by upstream models that support it. Will be forwarded through once the adapter is implemented.

## Structured Output

Requesty supports JSON mode via `response_format: { "type": "json_object" }` when the upstream model supports it.

## JSON Mode

Supported via the standard `response_format` parameter.

## Thinking / Reasoning

Some models available through Requesty support extended thinking (e.g., DeepSeek R1 variants). The adapter will pass through `reasoning_effort` and related fields once implemented.

## Vision

Vision support depends on the upstream model. Models that accept image inputs will work once the adapter forwards the content blocks correctly.

## Embeddings

Requesty may offer an embeddings endpoint. This is currently unverified and stubbed.

## Audio

Requesty does not currently offer audio models. ❌

## Usage Object

Requesty returns usage in OpenAI format:

```json
{
  "prompt_tokens": 20,
  "completion_tokens": 100,
  "total_tokens": 120
}
```

## Finish Reasons

Standard OpenAI finish reasons: `stop`, `length`, `tool_calls`.

## Error Format

Requesty uses the OpenAI-compatible error shape:

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

Rate limits vary by plan and model. Requesty publishes limits in the response headers (`X-RateLimit-*`). Check the [Requesty documentation](https://requesty.ai/docs) for current limits.

## Known Quirks

- **Newer provider.** Requesty is a relatively new inference API; documentation and community resources are limited compared to established providers.
- **Limited public documentation.** API details are sparse — the OpenAI-compatible contract is the primary reference.
- **Model catalog varies.** The available models may change frequently as Requesty adds new providers and models.

## Compatibility Notes

- Conductor registers Requesty as a stub. When `REQUESTY_API_KEY` is set, the provider auto-enables.
- Configure a static model list for catalog visibility:

```yaml
providers:
  requesty:
    enabled: true
    models:
      - "deepseek/deepseek-chat"
      - "deepseek/deepseek-coder"
```

- Since the API is OpenAI-compatible, implementing the adapter will be a thin `openaibase` wrapper.

## Production Readiness

🔲 **Not ready for production.** The adapter is a stub. Chat and streaming requests will return `not implemented`. Suitable for catalog advertising and route testing only.

## Open Issues

- [ ] Implement full OpenAI-compatible adapter using `openaibase.Base`
- [ ] Verify embeddings endpoint availability
- [ ] Add static pricing map once model pricing is published
- [ ] Test with live models to confirm API contract
