# Provider: OpenCode Zen

## Overview

OpenCode Zen is a multi-provider AI gateway that aggregates models from Grok (xAI), DeepSeek, MiniMax, GLM (Z.AI), Kimi (Moonshot), Qwen (Alibaba), and others through a single OpenAI-compatible API. Conductor has a **full adapter** implemented in `internal/provider/opencode/`, which wraps the `openaibase.Base` adapter with OpenCode Zen-specific configuration and a static pricing map.

## Authentication

OpenCode Zen authenticates via Bearer token. Obtain an API key from the OpenCode platform.

```bash
export OPENCODE_API_KEY=oc-xxx
```

Conductor auto-enables the provider when `OPENCODE_API_KEY` is set.

## Base URL

`https://opencode.ai/zen/v1`

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | ✅ Yes | `Bearer <OPENCODE_API_KEY>` |
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

OpenCode Zen supports full SSE streaming via `stream: true`. The adapter forwards stream chunks through Conductor's canonical `StreamChunk` type.

## Tool Calling

OpenCode Zen supports tool calling with OpenAI-compatible function schemas across its model catalog. The adapter forwards `tools` and `tool_choice` parameters directly to the upstream API.

## Parallel Tool Calling

Supported. OpenCode Zen accepts and returns parallel tool calls in the OpenAI format.

## Structured Output

OpenCode Zen supports structured output via `response_format: { "type": "json_object" }` on models that support it.

## JSON Mode

Supported via the standard `response_format` parameter.

## Thinking / Reasoning

OpenCode Zen supports reasoning models including DeepSeek R1/V4 variants and other thinking-capable models. The adapter passes through `reasoning_effort` and related fields. Note that some reasoning models (particularly DeepSeek V4 via NVIDIA NIM) require specific `chat_template_kwargs` — the OpenCode Zen path typically handles this automatically.

## Vision

OpenCode Zen provides vision-capable models (e.g., GPT-4o Vision, Claude variants). Image content blocks are forwarded to the upstream provider.

## Embeddings

OpenCode Zen provides embeddings via `POST /v1/embeddings`. The adapter maps Conductor's `EmbeddingRequest` to the upstream format and normalizes the response.

## Audio

OpenCode Zen does not currently offer audio models. ❌

## Usage Object

OpenCode Zen returns usage in OpenAI format:

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

OpenCode Zen uses the OpenAI-compatible error shape:

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

Rate limits vary by model and OpenCode subscription tier. The gateway enforces per-model limits. Check the [OpenCode Zen documentation](https://opencode.ai/docs/zen/) for current limits. Conductor's circuit breaker and fallback chains can mitigate rate-limit hits.

## Known Quirks

- **Multi-provider gateway itself.** OpenCode Zen aggregates Grok, DeepSeek, MiniMax, GLM, Kimi, Qwen and more behind a single API key. You are not connecting to these providers directly — OpenCode Zen is the intermediary.
- **Has dedicated provider package.** Conductor includes `internal/provider/opencode/` with full adapter logic.
- **Static pricing map.** The adapter includes a built-in pricing map for known Zen models (GPT-5.6 variants, Grok 4.5, DeepSeek V4, MiniMax M2.7, GLM-5.2, Kimi K2.6). Prices are in USD per 1,000 tokens.
- **Model ID format.** Zen model IDs are lowercase with hyphens, e.g., `gpt-5.6-sol`, `grok-4.5`, `deepseek-v4-flash`, `minimax-m2.7`, `glm-5.2`, `kimi-k2.6`.
- **GPT and Claude routing.** GPT models route through OpenAI's `/responses` endpoint internally; Claude models use `/messages`. These are abstracted away — Conductor only sees the OpenAI-compatible `/v1` surface.
- **API key via `OPENCODE_API_KEY`.** Conductor's auto-detection looks for this specific environment variable.

## Compatibility Notes

- Conductor has a dedicated provider package at `internal/provider/opencode/`.
- The adapter extends `openaibase.Base` with OpenCode Zen-specific configuration and pricing.
- Configure in `config.yaml`:

```yaml
providers:
  opencode:
    enabled: true
    api_key: "${OPENCODE_API_KEY}"
    base_url: "https://opencode.ai/zen/v1"
```

- Pricing is built into the adapter. No `cost.rates` override is needed for known models, but you can override individual rates if needed:

```yaml
cost:
  rates:
    "deepseek-v4-flash":
      input: 0.00014
      output: 0.00028
```

## Production Readiness

✅ **Ready for production.** The adapter is fully implemented and passes the provider interface contract. Chat, streaming, embeddings, model listing, and cost tracking all work. The static pricing map covers the most commonly used Zen models.

## Open Issues

- [ ] Expand pricing map to cover all Zen models as new ones are added
- [ ] Add provider-specific unit tests with mock server
- [ ] Document exact model availability per Zen tier
