# Provider: NVIDIA NIM

## Overview
NVIDIA NIM (Network Inference Microservices) provides hosted inference for a large catalog of AI models, including Llama, DeepSeek, GLM, Kimi, Qwen, Nemotron, and others. Conductor uses a customized OpenAI-compatible adapter (`nvidianim.Provider`) that extends `openaibase.Base` with NIM-specific request shaping. The catalog is very large and includes many models that may be unreachable; Conductor probes models on startup and periodically to filter out failures.

## Authentication
NVIDIA NIM uses API keys (NVAIE keys or cloud API keys). Set `NVIDIA_API_KEY` as an environment variable or configure it in `config.yaml` under `providers.nvidia_nim.api_key`. The key is sent as `Authorization: Bearer <key>`.

## Base URL
Default: `https://integrate.api.nvidia.com/v1`

Override with the `base_url` field in provider configuration. Self-hosted NIM instances can use a custom URL.

## Headers
- `Authorization: Bearer <api_key>` — required
- `Content-Type: application/json` — required

## Endpoints
- `POST /chat/completions` — full implementation via `nvidianim.Provider` (with NIM-specific shaping)
- `POST /embeddings` — implemented via `openaibase.Base` (available for embedding models)
- `GET /models` — full implementation via `openaibase.Base.ListModels` (returns full NIM catalog)
- `GET /models` (health) — used by `HealthCheck`

## Supported APIs
- Chat Completion: ✅
- Streaming: ✅
- Embeddings: ✅
- List Models: ✅

## Streaming
Fully supported. Conductor sets `stream_options.include_usage: true` automatically. A separate `http.Client` without the global timeout is used for streaming to avoid cutting off long reasoning model outputs. NIM reasoning models (e.g., Seed-OSS) can stream for extended periods.

## Tool Calling
✅ Supported for models that support tool calling. Conductor forwards tool definitions through the OpenAI-compatible endpoint. Not all NIM models support tools; check individual model documentation.

## Parallel Tool Calling
🟡 Partial. Depends on the model. Some NIM models support parallel tool calls; others do not.

## Structured Output
🟡 Partial. NIM's support for `response_format` varies by model. Conductor forwards the field but behavior is model-dependent.

## JSON Mode
🟡 Partial. Similar to structured output — depends on the model.

## Thinking / Reasoning
✅ Supported with automatic injection. NIM has extensive automatic thinking support across many model families. Conductor's `nvidianim` adapter automatically injects `chat_template_kwargs` for reasoning models when the client omits them. This prevents hangs and empty content responses.

### Automatic Thinking Injection
The adapter classifies models into thinking profiles and injects the appropriate kwargs:

| Family | Example Model IDs | Injected kwargs |
|--------|------------------|-----------------|
| DeepSeek V4 | `deepseek-ai/deepseek-v4-flash` | `{ thinking: true, reasoning_effort: "high" }` |
| DeepSeek V3 / R1 | `deepseek-ai/deepseek-v3.2`, `…/deepseek-r1-distill-*` | `{ thinking: true }` |
| GLM (Z.AI) | `z-ai/glm5`, `z-ai/glm4.7` | `{ enable_thinking: true, clear_thinking: false }` |
| Kimi | `moonshotai/kimi-k2.6`, `…/kimi-k2-thinking` | `{ thinking: true }` |
| Qwen3 reasoning | `qwen/qwen3-*-thinking`, `…/qwen3-coder-*` | `{ enable_thinking: true }` |
| Nemotron super/ultra | `nvidia/llama-3.3-nemotron-super-*` | `{ thinking: true }` |
| Nemotron-3 | `nvidia/nemotron-3-*` | `{ enable_thinking: true }` |
| MiniMax M3 | `minimaxai/minimax-m3` | `{ thinking_mode: "enabled" }` |
| Inkling | `thinkingmachines/inkling` | `{ reasoning_effort: "high" }` |
| Phi / Magistral | `microsoft/phi-*-reasoning`, `mistralai/magistral-*` | `{ enable_thinking: true }` |

Pass `reasoning_effort: "none"` (or disable via `chat_template_kwargs`) to force non-thinking mode. Instruct-only Qwen3 IDs (e.g., `qwen/qwen3-next-80b-a3b-instruct`) are left untouched.

## Vision
✅ Supported for vision-capable models. NIM offers many vision models (e.g., Llama 3.2 Vision, Nemotron vision variants). Image content is forwarded as `image_url` content parts.

## Embeddings
✅ Supported for embedding models. NIM provides embedding models accessible via `/v1/embeddings`. Static pricing is configured for a few common models; use `cost.rates` to extend.

## Audio
❌ Not supported. NIM does not expose audio transcription or translation through the OpenAI-compatible endpoint.

## Usage Object
✅ Full usage reporting. NIM returns `prompt_tokens`, `completion_tokens`, and `total_tokens`. Reasoning models may include `reasoning_tokens` in `completion_tokens_details`. Streaming usage is included in the final chunk.

## Finish Reasons
✅ Standard finish reasons:
- `stop` — natural completion
- `length` — exceeded max tokens
- `tool_calls` — tool calls issued
- `content_filter` — content filtered

## Error Format
OpenAI-compatible error format. Conductor parses `error.type`, `error.message`, and `error.code`. NIM may return specific error codes for model unavailability, quota limits, and invalid requests.

## Rate Limits
Rate limits are enforced by NVIDIA and returned as HTTP 429 responses. NIM rate limits vary by model and account tier. Conductor surfaces these as `rate_limit_error` type errors. Backoff and retry are handled at the router level.

## Known Quirks
- **Large catalog with unreachable models.** NIM's `GET /v1/models` returns the full catalog including retired, free-tier-only, and temporarily down endpoints. Conductor probes all models on startup and every 2 hours; failed models are hidden from `/v1/models` via exponential backoff retries.
- **`developer` role causes 500 errors.** NIM returns HTTP 500 when `developer` role is combined with `chat_template_kwargs`. Conductor remaps `developer` → `system` in `prepareChatRequest`.
- **Reasoning models need kwargs.** Without `chat_template_kwargs`, many NIM reasoning models hang or return empty `content`. The adapter injects these automatically.
- **Reasoning tokens in stream chunks.** Some NIM reasoning models emit reasoning content as separate stream chunks before content chunks. Conductor does not promote `reasoning` → `content` on stream deltas to avoid concatenating thinking into the visible reply.
- **Model IDs are NIM-specific.** Use the full NIM model ID (e.g., `meta/llama-3.1-8b-instruct`, `deepseek-ai/deepseek-v4-flash`) from `/v1/models`. Short aliases may not work.
- **Auto-probe behavior.** Unprobed models stay visible by default (`unknown_as_reachable: true`). Confirmed failures drop out of `/v1/models` immediately. Use `POST /api/models/force-probe` to trigger a probe pass.

## Compatibility Notes
NVIDIA NIM is OpenAI-compatible at the API level, but has significant model-specific quirks around reasoning modes. Conductor's `nvidianim.Provider` adds a request-shaping layer on top of `openaibase.Base` to handle:
1. `developer` → `system` role remapping
2. Automatic `chat_template_kwargs` injection for reasoning families
3. Reasoning effort normalization (`max`/`xhigh`/`high`/`medium`/`low`/`minimal`/`none` → NIM's `none`/`high`/`max`)

These adaptations make NIM models work correctly without requiring clients to know NIM-specific details.

## Production Readiness
✅ Production-ready with caveats. The adapter is well-tested across many model families. Model reachability probing is essential due to the large and sometimes unstable catalog. Use `catalog.curated_only: true` with a static model list to reduce probe noise in production. Health monitoring via `/api/models/status` is recommended.

## Open Issues
- Some edge-case reasoning models may not have complete `chat_template_kwargs` profiles.
- Probe concurrency (`health.models.concurrency`) may need tuning for very large catalogs.
- Embedding model pricing is sparse; most models rely on `cost.rates` configuration.
