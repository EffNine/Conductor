# Provider: Kilo

## Overview

Kilo is an AI inference API provider focused on coding models, offering access to specialized code generation and completion models. Conductor's Kilo adapter is a **stub** — the provider is registered and can advertise models via a static list, but request handling is not yet implemented.

Kilo's API is **OpenAI-compatible** at `https://kilo.ai/v1`.

## Authentication

Kilo authenticates via Bearer token. Obtain an API key from the Kilo dashboard.

```bash
export KILO_API_KEY=kilo-xxx
```

Conductor auto-enables the provider when `KILO_API_KEY` is set.

## Base URL

`https://kilo.ai/v1`

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | ✅ Yes | `Bearer <KILO_API_KEY>` |
| `Content-Type` | ✅ Yes | `application/json` |

## Endpoints

| Conductor Endpoint | Supported |
|--------------------|-----------|
| `POST /v1/chat/completions` | 🔲 Stub |
| `GET /v1/models` | ✅ (static list) |
| `POST /v1/embeddings` | ❌ Not available |

## Supported APIs

- Chat Completion: 🔲
- Streaming: 🔲
- Embeddings: ❌
- List Models: ✅ (static config only)

## Streaming

Kilo supports SSE streaming at `/v1/chat/completions` with `stream: true`. The stream format follows the OpenAI convention. Full streaming support will be available once the adapter is implemented.

## Tool Calling

Tool calling may be supported by Kilo's coding models. The OpenAI tool schema will be forwarded through once the adapter is implemented.

## Parallel Tool Calling

Will be supported if upstream models support it. Will be forwarded through once the adapter is implemented.

## Structured Output

JSON mode via `response_format: { "type": "json_object" }` will be passed through once the adapter is implemented.

## JSON Mode

Supported via the standard `response_format` parameter.

## Thinking / Reasoning

Kilo's coding-focused models may not expose extended thinking tokens. No special handling is expected.

## Vision

Kilo is focused on code models and does not offer vision capabilities. ❌

## Embeddings

Kilo does not provide an embeddings endpoint. ❌

## Audio

Kilo does not offer audio models. ❌

## Usage Object

Kilo returns usage in OpenAI format:

```json
{
  "prompt_tokens": 40,
  "completion_tokens": 200,
  "total_tokens": 240
}
```

## Finish Reasons

Standard OpenAI finish reasons: `stop`, `length`, `tool_calls`.

## Error Format

Kilo uses the OpenAI-compatible error shape:

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

Rate limits depend on the Kilo plan. Check the [Kilo documentation](https://kilo.ai/docs) for current limits. Conductor's circuit breaker and fallback chains can mitigate rate-limit hits.

## Known Quirks

- **Focuses on coding models.** Kilo's catalog is centered on code generation, completion, and reasoning models rather than general-purpose chat.
- **Smaller model catalog.** Expect fewer models compared to generalist providers like OpenAI or OpenRouter.
- **No embeddings.** The service is inference-only.

## Compatibility Notes

- Conductor registers Kilo as a stub. When `KILO_API_KEY` is set, the provider auto-enables.
- Configure a static model list for catalog visibility:

```yaml
providers:
  kilo:
    enabled: true
    models:
      - "kilo-coder-large"
      - "kilo-coder-small"
```

- Since the API is OpenAI-compatible, implementing the adapter will be a thin `openaibase` wrapper.

## Production Readiness

🔲 **Not ready for production.** The adapter is a stub. Chat and streaming requests will return `not implemented`. Suitable for catalog advertising and route testing only.

## Open Issues

- [ ] Implement full OpenAI-compatible adapter using `openaibase.Base`
- [ ] Add static pricing map for Kilo models
- [ ] Verify exact model IDs available on the platform
