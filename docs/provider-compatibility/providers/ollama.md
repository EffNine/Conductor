# Provider: Ollama

## Overview
Ollama provides local inference for open-source language models. Conductor supports both local Ollama instances (running on `localhost:11434`) and Ollama Cloud (`ollama.com`). The local Ollama API is OpenAI-compatible on the `/v1` endpoint, so Conductor uses the `openaibase.Base` adapter. Ollama Cloud requires an API key and uses the same OpenAI-compatible format.

## Authentication
- **Local Ollama**: No API key required. Set `OLLAMA_BASE_URL` to point at your local instance (e.g., `http://localhost:11434/v1`).
- **Ollama Cloud**: Set `OLLAMA_API_KEY` as an environment variable. The base URL defaults to `https://ollama.com/v1`.

In `config.yaml`, configure under `providers.ollama` with `api_key` (cloud) and/or `base_url` (local override).

## Base URL
- Local default: `http://localhost:11434/v1`
- Cloud default: `https://ollama.com/v1`

Override with `OLLAMA_BASE_URL` environment variable or the `base_url` field in `config.yaml`. If both `OLLAMA_API_KEY` and `OLLAMA_BASE_URL` are set, the base URL wins and the key is still sent as `Authorization: Bearer`.

## Headers
- `Authorization: Bearer <api_key>` — required for cloud, optional for local
- `Content-Type: application/json` — required

## Endpoints
- `POST /chat/completions` — full implementation via `openaibase.Base`
- `POST /embeddings` — full implementation via `openaibase.Base`
- `GET /models` — full implementation via `openaibase.Base.ListModels` (local) or static list (cloud)
- `GET /models` (health) — used by `HealthCheck`

## Supported APIs
- Chat Completion: ✅
- Streaming: ✅
- Embeddings: ✅
- List Models: ✅

## Streaming
Fully supported. Conductor sets `stream_options.include_usage: true` automatically. Ollama's streaming format matches OpenAI's. Note: local Ollama may not return usage tokens in streams; the final chunk may have zero tokens.

## Tool Calling
🟡 Partial. Ollama's tool calling support depends on the model. Recent versions of Llama 3.1 and Mistral support function calling via the OpenAI-compatible endpoint, but not all models do. Conductor forwards tool definitions; the upstream may ignore them for models that don't support tools.

## Parallel Tool Calling
🟡 Partial. Depends on the model. Some Ollama models support parallel tool calls; others do not.

## Structured Output
🟡 Partial. Ollama's support for `response_format` varies by model and version. Conductor forwards the field but behavior is model-dependent.

## JSON Mode
🟡 Partial. Similar to structured output — depends on the model.

## Thinking / Reasoning
🟡 Partial. Some Ollama models support reasoning/thinking modes, but this is model-specific. Conductor forwards `reasoning_effort` and `reasoning` fields, but most local Ollama models do not honor them. Check individual model documentation.

## Vision
❌ Not supported. Ollama's OpenAI-compatible endpoint does not expose vision capabilities. Image content in messages is ignored or may cause errors.

## Embeddings
✅ Supported on local Ollama. Ollama provides embedding models (e.g., `nomic-embed-text`, `snowflake-arctic-embed`) accessible via `/v1/embeddings`. Ollama Cloud may have different embedding model availability.

## Audio
❌ Not supported. Ollama does not offer audio capabilities through its OpenAI-compatible endpoint.

## Usage Object
🟡 Partial. Local Ollama returns usage in non-streaming responses. Streaming usage is often incomplete — the final chunk may report `completion_tokens: 0` even when content was generated. Conductor sets `stream_options.include_usage: true` but local Ollama may not honor it.

## Finish Reasons
✅ Standard finish reasons:
- `stop` — natural completion
- `length` — exceeded max tokens
- `tool_calls` — tool calls issued (when supported)

## Error Format
OpenAI-compatible error format. Conductor parses `error.type`, `error.message`, and `error.code`. Local Ollama errors may include Ollama-specific messages (e.g., model not found, context full).

## Rate Limits
Local Ollama has no rate limits (bounded by your hardware). Ollama Cloud has rate limits based on your subscription tier. HTTP 429 responses from cloud are surfaced as `rate_limit_error`.

## Known Quirks
- **Model IDs are Ollama-specific.** Use Ollama's model naming convention (e.g., `llama3.1:8b`, `mistral:7b`, `codellama:7b`). These differ from Hugging Face or provider IDs.
- **No vision support.** Image content is not processed by local Ollama's OpenAI-compatible endpoint.
- **Streaming usage may be zero.** Local Ollama often does not return token counts in stream chunks.
- **Tool calling is model-dependent.** Only recent models with native function calling support will honor tool definitions.
- **Context window varies by model.** Ollama models have different context limits (e.g., 8K, 32K, 128K). Ollama returns an error if exceeded.
- **Local requires running server.** The Ollama daemon must be running (`ollama serve`) for local connections. Cloud requires `OLLAMA_API_KEY`.
- **`OLLAMA_BASE_URL` does not enable the provider.** You must also enable Ollama via YAML config or `OLLAMA_API_KEY`.

## Compatibility Notes
Ollama (local) is fully OpenAI-compatible, so Conductor's `openaibase.Base` adapter works without translation. The main limitations are feature-related (no vision, limited tool calling, incomplete streaming usage) rather than compatibility-related. Ollama Cloud uses the same adapter but may have different model availability.

## Production Readiness
🟡 Production-ready for local inference with supported models. Best suited for development, testing, and workloads where local inference is acceptable. Cloud usage is production-ready but subject to Ollama's service limits. Tool calling and streaming usage may be unreliable on some models.

## Open Issues
- Streaming usage tokens are often zero on local Ollama.
- Tool calling support is inconsistent across models.
- No vision support in the OpenAI-compatible endpoint.
