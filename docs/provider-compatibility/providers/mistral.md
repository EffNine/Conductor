# Provider: Mistral

## Overview

Mistral AI is a European AI lab known for its efficient open-weight models such as Mistral 7B, Mixtral 8x7B, Mistral Large, and Codestral. Conductor's Mistral adapter is a **stub** — the provider is registered in the registry and can appear in `/v1/models` when a static model list is configured, but chat/embeddings requests will return `not implemented` until the adapter is fully developed.

Mistral's API is **OpenAI-compatible** at the `/v1` endpoint, which means the path to a full adapter is straightforward: a thin wrapper around the `openaibase` adapter with Mistral-specific model normalization.

## Authentication

Mistral authenticates via Bearer token in the `Authorization` header. Obtain an API key from [console.mistral.ai](https://console.mistral.ai/api-keys).

```bash
export MISTRAL_API_KEY=sk-mistral-xxx
```

Conductor auto-enables the provider when `MISTRAL_API_KEY` is set.

## Base URL

- **Catalog:** `https://api.mistral.ai/models`
- **Chat completions:** `https://api.mistral.ai/v1`

Default base URL in Conductor: `https://api.mistral.ai/v1`

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | ✅ Yes | `Bearer <MISTRAL_API_KEY>` |
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

Mistral supports Server-Sent Events (SSE) streaming at `/v1/chat/completions` with `stream: true`. The response format is OpenAI-compatible. Once the adapter is implemented, streaming will be natively supported.

## Tool Calling

Mistral Large and Codestral support tool/function calling with an OpenAI-compatible schema. The provider is currently a stub; tool calling support will be available once the adapter is implemented.

## Parallel Tool Calling

Not yet supported (adapter is a stub). Mistral's API supports parallel tool calls; the Conductor adapter will forward the capability once implemented.

## Structured Output

Mistral supports JSON mode via `response_format: { "type": "json_object" }`. This will be passed through once the adapter is implemented.

## JSON Mode

Supported via `response_format` parameter when the adapter is implemented.

## Thinking / Reasoning

Mistral models do not expose native extended thinking tokens. No special handling is required.

## Vision

Mistral's vision-capable models (e.g., `mistral-large-latest`, `pixtral-12b`) accept image URLs or base64-encoded images in the content array. Vision support will be available once the adapter is implemented.

## Embeddings

Mistral provides embeddings via `POST /v1/embeddings` (e.g., `mistral-embed`). This endpoint is OpenAI-compatible. Embeddings support is currently a stub.

## Audio

Mistral does not currently offer audio/speech models through its API. ❌

## Usage Object

Mistral returns usage in the OpenAI format:

```json
{
  "prompt_tokens": 25,
  "completion_tokens": 120,
  "total_tokens": 145
}
```

## Finish Reasons

Mistral returns standard OpenAI finish reasons: `stop`, `length`, `tool_calls`, `content_filter`. The `function_call` reason is not used; tool calls use `tool_calls` instead.

## Error Format

Mistral uses the OpenAI-compatible error shape:

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

Mistral rate limits vary by plan. The free tier allows 200 requests per minute. Refer to [Mistral AI pricing](https://mistral.ai和技术/) for current limits. Conductor's circuit breaker and fallback chains can mitigate rate-limit hits.

## Known Quirks

- **Different model naming convention.** Mistral uses names like `mistral-large-latest`, `codestral-latest`, `mistral-embed` rather than OpenAI's `gpt-4o` style. Route model IDs should match Mistral's naming.
- **Has its own native SDK.** The official Python/JS SDKs are available but not required — the OpenAI-compatible `/v1` endpoint works with any OpenAI SDK client.
- **`/models` catalog endpoint.** The catalog endpoint at `https://api.mistral.ai/models` returns a different shape than OpenAI's `/v1/models`. A full adapter may need to fetch and normalize this list.

## Compatibility Notes

- Conductor registers Mistral as a stub provider. When `MISTRAL_API_KEY` is set, the provider auto-enables.
- Configure a static model list in `config.yaml` to have Mistral models appear in `/v1/models`:

```yaml
providers:
  mistral:
    enabled: true
    models:
      - "mistral-large-latest"
      - "codestral-latest"
      - "mistral-embed"
```

- Routes and aliases can point to the `mistral` provider; chat requests will return `not implemented` until the adapter is developed.

## Production Readiness

🔲 **Not ready for production.** The provider is a stub. Chat, streaming, and embeddings will all fail with `not implemented` errors. Suitable only for model catalog advertising and route resolution testing.

## Open Issues

- [ ] Implement full OpenAI-compatible adapter (likely a thin wrapper around `openaibase.Base`)
- [ ] Add dynamic model listing from `https://api.mistral.ai/models`
- [ ] Add static pricing map for known Mistral models
- [ ] Test vision and tool calling with live Mistral models
