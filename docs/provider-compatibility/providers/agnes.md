# Provider: Agnes AI

## Overview

Agnes AI is an AI inference provider offering a range of open and proprietary models through an OpenAI-compatible API. Conductor has a **full adapter** implemented in `internal/provider/agnesai/`, which wraps the `openaibase.Base` adapter with Agnes AI-specific configuration.

## Authentication

Agnes AI authenticates via Bearer token. Obtain an API key from the Agnes AI platform.

```bash
export AGNES_API_KEY=aganessk-xxx
```

Conductor auto-enables the provider when `AGNES_API_KEY` is set.

## Base URL

`https://apihub.agnes-ai.com/v1`

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | ✅ Yes | `Bearer <AGNES_API_KEY>` |
| `Content-Type` | ✅ Yes | `application/json` |

## Endpoints

| Conductor Endpoint | Supported |
|--------------------|-----------|
| `POST /v1/chat/completions` | ✅ |
| `GET /v1/models` | ✅ |
| `POST /v1/embeddings` | ✅ |

## Supported APIs

- Chat Completion: ✅
- Streaming: ✅
- Embeddings: ✅
- List Models: ✅

## Streaming

Agnes AI supports full SSE streaming via `stream: true`. The adapter forwards stream chunks through Conductor's canonical `StreamChunk` type.

## Tool Calling

Agnes AI supports tool calling with OpenAI-compatible function schemas. The adapter forwards `tools` and `tool_choice` parameters directly to the upstream API.

## Parallel Tool Calling

Supported. Agnes AI accepts and returns parallel tool calls in the OpenAI format.

## Structured Output

Agnes AI supports structured output via `response_format: { "type": "json_object" }` on models that support it.

## JSON Mode

Supported via the standard `response_format` parameter.

## Thinking / Reasoning

Thinking/reasoning support depends on the upstream model. The adapter passes through `reasoning_effort` and related fields when present in the request.

## Vision

Vision support depends on the upstream model. Image content blocks are forwarded to the Agnes AI API.

## Embeddings

Agnes AI provides embeddings via `POST /v1/embeddings`. The adapter maps Conductor's `EmbeddingRequest` to the upstream format and normalizes the response.

## Audio

Agnes AI does not currently offer audio models. ❌

## Usage Object

Agnes AI returns usage in OpenAI format:

```json
{
  "prompt_tokens": 25,
  "completion_tokens": 120,
  "total_tokens": 145
}
```

Conductor normalizes this into its canonical `Usage` struct for cost tracking.

## Finish Reasons

Standard OpenAI finish reasons: `stop`, `length`, `tool_calls`, `content_filter`.

## Error Format

Agnes AI uses the OpenAI-compatible error shape:

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

Conductor normalizes this into a `ProviderError` with the appropriate `ErrorType`.

## Rate Limits

Rate limits vary by plan and model. Agnes AI publishes limits in response headers. Check the [Agnes AI documentation](https://apihub.agnes-ai.com/docs) for current limits. Conductor's circuit breaker and fallback chains can mitigate rate-limit hits.

## Known Quirks

- **Newer provider.** Agnes AI is a relatively new platform; model availability and documentation may evolve.
- **API key via `AGNES_API_KEY`.** Conductor's auto-detection looks for this specific environment variable.
- **No published pricing map.** Agnes AI does not publish per-token pricing publicly. The adapter returns an empty pricing map; operators should configure `cost.rates` in `config.yaml` for cost tracking.

## Compatibility Notes

- Conductor has a dedicated provider package at `internal/provider/agnesai/`.
- The adapter extends `openaibase.Base` with Agnes AI-specific configuration.
- Configure in `config.yaml`:

```yaml
providers:
  agnesai:
    enabled: true
    api_key: "${AGNES_API_KEY}"
    base_url: "https://apihub.agnes-ai.com/v1"
```

- Add `cost.rates` for known models to enable accurate cost tracking:

```yaml
cost:
  rates:
    "agnes-model-id":
      input: 0.000002
      output: 0.000008
```

## Production Readiness

✅ **Ready for production.** The adapter is fully implemented and passes the provider interface contract. Chat, streaming, embeddings, and model listing all work. Operators should configure `cost.rates` manually for cost tracking.

## Open Issues

- [ ] Add static pricing map once Agnes AI publishes public rates
- [ ] Add provider-specific unit tests with mock server
- [ ] Document exact model IDs available on the platform
