# Provider: Alibaba Cloud MaaS (DashScope)

## Overview

Alibaba Cloud's Model-as-a-Service (MaaS) platform exposes AI models through DashScope, China's largest AI inference platform. The OpenAI-compatible endpoint at `dashscope.aliyuncs.com` allows Conductor to route requests to models like Qwen, GLM, and others hosted on Alibaba's infrastructure. Conductor's DashScope adapter is a **stub**.

## Authentication

DashScope authenticates via Bearer token using an Alibaba Cloud API key. Obtain a key from [dashscope.console.aliyun.com](https://dashscope.console.aliyun.com/).

```bash
export DASHSCOPE_API_KEY=sk-xxx
```

Conductor auto-enables the provider when `DASHSCOPE_API_KEY` is set.

## Base URL

`https://dashscope.aliyuncs.com/compatible-mode/v1`

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | ✅ Yes | `Bearer <DASHSCOPE_API_KEY>` |
| `Content-Type` | ✅ Yes | `application/json` |
| `X-DashScope-Stream` | Optional | Set to `true` for streaming |

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

DashScope supports SSE streaming. The compatible-mode endpoint follows the OpenAI stream format. Full streaming support will be available once the adapter is implemented.

## Tool Calling

Qwen and other models on DashScope support tool calling with OpenAI-compatible schemas. Support will be available once the adapter is implemented.

## Parallel Tool Calling

Supported by upstream models. Will be forwarded through once the adapter is implemented.

## Structured Output

DashScope supports JSON mode via `response_format: { "type": "json_object" }` on compatible models.

## JSON Mode

Supported via the standard `response_format` parameter.

## Thinking / Reasoning

Qwen reasoning models (e.g., `qwen-plus-thinking`, `qwen-max-thinking`) support extended thinking. The adapter will pass through `reasoning_effort` and related fields once implemented. Note that NVIDIA NIM's Qwen reasoning models require specific `chat_template_kwargs` — DashScope-hosted Qwen models may have different requirements.

## Vision

Qwen-VL models on DashScope support vision. Vision support will be available once the adapter is implemented.

## Embeddings

DashScope provides embeddings via its API. Support is currently stubbed.

## Audio

DashScope does not currently offer audio models through the compatible-mode endpoint. ❌

## Usage Object

DashScope returns usage in OpenAI format:

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

DashScope uses the OpenAI-compatible error shape:

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

DashScope rate limits vary by model and subscription tier. The free tier has restrictive limits; paid tiers offer higher throughput. Check the [DashScope documentation](https://help.aliyun.com/zh/dashscope/) for current limits.

## Known Quirks

- **Chinese provider.** API documentation and console are primarily in Chinese. English resources are limited.
- **Requires Alibaba Cloud account.** You must have an Alibaba Cloud account with DashScope service enabled.
- **Different model IDs.** DashScope uses IDs like `qwen-turbo`, `qwen-plus`, `qwen-max`, `qwen-vl-max`, `glm-4-plus` — these differ from Hugging Face model names.
- **`compatible-mode` endpoint.** The `/compatible-mode/v1` path is DashScope's OpenAI-compatible layer. The native DashScope API has a different shape.
- **Regional restrictions.** Some models may only be available from certain regions (mainland China).

## Compatibility Notes

- Conductor registers DashScope as a stub. When `DASHSCOPE_API_KEY` is set, the provider auto-enables.
- Configure a static model list for catalog visibility:

```yaml
providers:
  aliyun_maas:
    enabled: true
    models:
      - "qwen-turbo"
      - "qwen-plus"
      - "qwen-max"
      - "qwen-vl-max"
```

- Since the compatible-mode API is OpenAI-compatible, implementing the adapter will be a thin `openaibase` wrapper.

## Production Readiness

🔲 **Not ready for production.** The adapter is a stub. Chat and streaming requests will return `not implemented`. Suitable for catalog advertising and route testing only.

## Open Issues

- [ ] Implement full OpenAI-compatible adapter using `openaibase.Base`
- [ ] Add static pricing map for DashScope models
- [ ] Test thinking model handling for Qwen reasoning variants
- [ ] Verify embeddings endpoint contract
- [ ] Add tests with mock DashScope server
