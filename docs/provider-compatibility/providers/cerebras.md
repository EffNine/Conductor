# Provider: Cerebras

## Overview

Cerebras provides cloud inference powered by its custom Wafer-Scale Engine (WSE), delivering very fast token throughput on large language models including its own Llama-based models (Llama 3.3 70B, Llama 3.1 8B) and other open models. Conductor's Cerebras adapter is a **stub** — the provider is registered and can advertise models via a static list, but request handling is not yet implemented.

Cerebras' API is **OpenAI-compatible** at `https://api.cerebras.ai/v1`, making the path to a full adapter a straightforward `openaibase` wrapper.

## Authentication

Cerebras authenticates via Bearer token. Obtain an API key from [cloud.cerebras.ai](https://cloud.cerebras.ai/).

```bash
export CEREBRAS_API_KEY=cskb-xxx
```

Conductor auto-enables the provider when `CEREBRAS_API_KEY` is set.

## Base URL

`https://api.cerebras.ai/v1`

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | ✅ Yes | `Bearer <CEREBRAS_API_KEY>` |
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

Cerebras supports SSE streaming at `/v1/chat/completions` with `stream: true`. The stream format follows the OpenAI convention. Full streaming support will be available once the adapter is implemented.

## Tool Calling

Cerebras models support tool calling with OpenAI-compatible function schemas. Support will be available once the adapter is implemented.

## Parallel Tool Calling

Cerebras supports parallel tool calls. This will be forwarded through once the adapter is implemented.

## Structured Output

Cerebras supports JSON mode via `response_format: { "type": "json_object" }`. Available once the adapter is implemented.

## JSON Mode

Supported via the standard `response_format` parameter.

## Thinking / Reasoning

Cerebras models do not expose native extended thinking tokens. No special handling is required.

## Vision

Cerebras currently does not offer vision-capable models. ❌

## Embeddings

Cerebras does not provide an embeddings endpoint. ❌

## Audio

Cerebras does not offer audio models. ❌

## Usage Object

Cerebras returns usage in OpenAI format:

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

Cerebras uses the OpenAI-compatible error shape:

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

Cerebras rate limits depend on the plan and model. The API imposes requests-per-minute and tokens-per-minute limits. Check [Cerebras documentation](https://inference-docs.cerebras.ai/) for current limits. Conductor's rate-limit and fallback features can help manage hits.

## Known Quirks

- **Fast inference on large models.** Cerebras is known for very high token throughput, especially on 70B-class models, due to its WSE hardware.
- **Limited model selection.** The available model catalog is smaller than OpenAI or Anthropic — primarily Cerebras-hosted Llama variants and a few selected open models.
- **No embeddings endpoint.** Cerebras focuses on chat/inference only.
- **Model ID format.** Cerebras uses IDs like `llama-3.3-70b-specdec`, `llama-3.1-8b-instruct`. Configure routes with these exact IDs.

## Compatibility Notes

- Conductor registers Cerebras as a stub. When `CEREBRAS_API_KEY` is set, the provider auto-enables.
- Configure a static model list for catalog visibility:

```yaml
providers:
  cerebras:
    enabled: true
    models:
      - "llama-3.3-70b-specdec"
      - "llama-3.1-8b-instruct"
```

- Once the `openaibase` wrapper is added, Cerebras will work as a drop-in OpenAI-compatible provider.

## Production Readiness

🔲 **Not ready for production.** The adapter is a stub. Chat and streaming requests will return `not implemented`. Suitable for catalog advertising and route testing only.

## Open Issues

- [ ] Implement full OpenAI-compatible adapter using `openaibase.Base`
- [ ] Add static pricing map for Cerebras models
- [ ] Add health-check integration with `api.cerebras.ai/v1/models`
