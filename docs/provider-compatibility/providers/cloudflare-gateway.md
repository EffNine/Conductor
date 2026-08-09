# Provider: Cloudflare AI Gateway

## Overview

Cloudflare AI Gateway is a routing and observability layer that sits in front of multiple AI providers (OpenAI, Anthropic, Cohere, etc.). It provides rate limiting, caching, logging, and failover across upstream providers — all through a single OpenAI-compatible endpoint. Conductor's Cloudflare Gateway adapter is a **stub**.

## Authentication

Cloudflare AI Gateway authenticates via Cloudflare API key or token. You need:

1. A Cloudflare account with AI Gateway enabled
2. An API key with `Account.AI_Gateway.write` permission
3. A gateway configured with upstream providers

```bash
export CLOUDFLARE_API_KEY=xxx
export CLOUDFLARE_ACCOUNT_ID=xxx
```

Conductor auto-enables the provider when `CLOUDFLARE_API_KEY` and `CLOUDFLARE_ACCOUNT_ID` are set.

## Base URL

`https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/v1`

The full URL is constructed by Conductor from `CLOUDFLARE_ACCOUNT_ID`.

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | ✅ Yes | `Bearer <CLOUDFLARE_API_KEY>` |
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

Cloudflare AI Gateway proxies streaming from upstream providers. SSE streams are forwarded transparently. Full streaming support will be available once the adapter is implemented.

## Tool Calling

Tool calling is forwarded to the upstream provider by Cloudflare AI Gateway. Support depends on the configured upstream provider.

## Parallel Tool Calling

Supported if the upstream provider supports it. Forwarded through the gateway.

## Structured Output

JSON mode is forwarded to the upstream provider. Support depends on the upstream model.

## JSON Mode

Supported via the standard `response_format` parameter, forwarded to upstream.

## Thinking / Reasoning

Thinking/reasoning is forwarded to the upstream provider. Support depends on the configured upstream model.

## Vision

Vision is forwarded to the upstream provider. Support depends on the configured upstream model.

## Embeddings

Embeddings are proxied to the upstream provider. Support depends on the configured upstream provider.

## Audio

Audio is not supported through the OpenAI-compatible gateway endpoint. ❌

## Usage Object

Cloudflare AI Gateway returns usage from the upstream provider in OpenAI format:

```json
{
  "prompt_tokens": 25,
  "completion_tokens": 120,
  "total_tokens": 145
}
```

Cloudflare also adds its own observability headers (`CF-Ray`, etc.) for debugging.

## Finish Reasons

Standard OpenAI finish reasons, forwarded from the upstream provider: `stop`, `length`, `tool_calls`.

## Error Format

Cloudflare AI Gateway returns errors in OpenAI-compatible format when the upstream returns an error:

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

Gateway-level errors (e.g., rate limit exceeded, upstream timeout) may include additional Cloudflare-specific fields.

## Rate Limits

Rate limits are determined by:

1. **Cloudflare AI Gateway plan limits** — set in the Cloudflare dashboard per gateway
2. **Upstream provider limits** — the gateway aggregates and enforces limits per upstream

The gateway can be configured with per-provider rate limits, caching TTLs, and failover policies. Check the [Cloudflare AI Gateway documentation](https://developers.cloudflare.com/ai-gateway/) for configuration details.

## Known Quirks

- **Gateway layer, not a provider.** Cloudflare AI Gateway is a proxy that aggregates multiple providers. Conductor sees only the gateway endpoint, not the individual upstream providers directly.
- **Aggregates multiple providers.** A single Conductor route to `cloudflare_gateway` can reach OpenAI, Anthropic, Cohere, or any configured upstream.
- **Rate limits from upstream.** The gateway enforces its own limits AND passes through upstream limits. Configure gateway rate limits appropriately.
- **Requires Cloudflare Zero Trust setup.** AI Gateway is part of Cloudflare Zero Trust. You need a Cloudflare account with Zero Trust enabled and a gateway created in the dashboard.
- **Account ID required.** The base URL includes `{account_id}`, which Conductor constructs from `CLOUDFLARE_ACCOUNT_ID`.
- **No native model catalog.** The gateway does not expose a `/v1/models` listing of upstream models. Conductor must use a static model list.

## Compatibility Notes

- Conductor registers Cloudflare AI Gateway as a stub. When `CLOUDFLARE_API_KEY` and `CLOUDFLARE_ACCOUNT_ID` are set, the provider auto-enables.
- Configure in `config.yaml`:

```yaml
providers:
  cloudflare_gateway:
    enabled: true
    api_key: "${CLOUDFLARE_API_KEY}"
    account_id: "${CLOUDFLARE_ACCOUNT_ID}"
    models:
      - "openai/gpt-4o"
      - "anthropic/claude-3-5-sonnet-20241022"
```

- Since the gateway is OpenAI-compatible, implementing the adapter will be a thin `openaibase` wrapper with account ID injection into the base URL.

## Production Readiness

🔲 **Not ready for production.** The adapter is a stub. Chat and streaming requests will return `not implemented`. Suitable for catalog advertising and route testing only.

## Open Issues

- [ ] Implement full OpenAI-compatible adapter using `openaibase.Base` with account ID URL construction
- [ ] Add static model list for commonly routed upstream models
- [ ] Document Cloudflare AI Gateway setup steps for new users
- [ ] Add health check that verifies gateway accessibility
