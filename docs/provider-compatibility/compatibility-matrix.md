# Provider Compatibility Matrix

**Last updated:** 2026-08-08

Compares all 17 registered and planned providers across the capabilities Conductor normalizes and tracks.

## Symbols

| Symbol | Meaning |
|--------|---------|
| ✅ | Native — fully supported with a working adapter |
| 🟡 | Partial — adapter exists but feature is incomplete or unverified |
| 🔲 | Stub — provider registered; interface defined, logic not yet implemented |
| ❌ | Not Supported — provider does not expose this capability |
| ❓ | Unknown — not in the Conductor codebase; assessed from public provider documentation |

## Implementation Status Reference

| Provider | Adapter Type |
|----------|-------------|
| openai | ✅ Full — OpenAI-compatible `openaibase.Base` with custom pricing |
| anthropic | ✅ Full — Native Messages API adapter (not OpenAI-compatible) |
| ollama | ✅ Full — OpenAI-compatible `openaibase.Base` (local + Cloud) |
| xai | ✅ Full — OpenAI-compatible `openaibase.Base` |
| nvidia_nim | ✅ Full — OpenAI-compatible `openaibase.Base` with NIM-specific request shaping |
| opencode | ✅ Full — OpenAI-compatible `openaibase.Base` (OpenCode Zen) |
| nous_portal | ✅ Full — OpenAI-compatible `openaibase.Base` |
| agnesai | ✅ Full — OpenAI-compatible `openaibase.Base` |
| deepseek | 🔲 Stub — embeds `openaibase.Base`; OpenAI-compatible endpoint |
| groq | 🔲 Stub — embeds `openaibase.Base`; OpenAI-compatible endpoint |
| openrouter | 🔲 Stub — embeds `openaibase.Base`; OpenAI-compatible endpoint |
| gemini | 🔲 Stub — embeds `openaibase.Base`; OpenAI-compatible endpoint |
| mistral | ❓ Unknown — no adapter in codebase |
| cerebras | ❓ Unknown — no adapter in codebase |
| requesty | ❓ Unknown — no adapter in codebase |
| kilo | ❓ Unknown — no adapter in codebase |
| zai | ❓ Unknown — no adapter in codebase |
| aliyun-maas | ❓ Unknown — no adapter in codebase |
| cloudflare-gateway | ❓ Unknown — no adapter in codebase |
| opencode-zen | ✅ Full — see `opencode` |

## Compatibility Matrix

| Provider | Auth | Chat | Stream | Tool Call | Parallel Tool | JSON Mode | Structured | Thinking | Vision | Embed | Audio | Func Call | Usage Track | Finish Reasons | Rate Limit Headers | Error Format | OpenAI Compat | Native API | Notes |
|----------|------|------|--------|-----------|---------------|-----------|------------|----------|--------|-------|-------|---------|-------------|----------------|------------------|--------------|---------------|------------|-------|
| **openai** | Bearer token | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | 🟡 | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | ✅ | OpenAI format | ✅ | Full OpenAI-compatible adapter |
| **anthropic** | API key + header | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ | Anthropic format | ❌ | Native Messages API; full chat/stream/tool/vision/reasoning |
| **gemini** | API key (query) | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | ❌ | 🔲 | 🔲 | 🔲 | 🔲 | Gemini format | ❌ | Stub using OpenAI-compatible base; real API is REST-native |
| **groq** | Bearer token | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | ❌ | ❌ | 🔲 | ❌ | 🔲 | 🔲 | 🔲 | 🔲 | OpenAI-compatible | ✅ | Stub using OpenAI-compatible base; native API matches OpenAI shape |
| **deepseek** | Bearer token | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | ✅ | ❌ | 🔲 | ❌ | 🔲 | 🔲 | 🔲 | 🔲 | OpenAI-compatible | ✅ | Stub using OpenAI-compatible base; supports reasoning models |
| **openrouter** | Bearer token | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | 🔲 | OpenAI-compatible | ✅ | Stub using OpenAI-compatible base; proxy across many providers |
| **ollama** | None / Bearer | ✅ | ✅ | 🟡 | 🟡 | 🟡 | 🟡 | ❌ | 🟡 | ✅ | ❌ | 🟡 | ✅ | ✅ | ✅ | OpenAI-compatible | ✅ | Full OpenAI-compatible adapter; local and Cloud |
| **mistral** | Bearer token | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ✅ | ✅ | ❌ | ❓ | ❓ | ❓ | ❓ | OpenAI-compatible | ✅ | No adapter in codebase; native API is OpenAI-compatible |
| **cerebras** | Bearer token | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❌ | ❌ | ❓ | ❌ | ❓ | ❓ | ❓ | ❓ | OpenAI-compatible | ✅ | No adapter in codebase; native API is OpenAI-compatible |
| **requesty** | API key | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | Unknown | ❓ | No adapter in codebase; limited public documentation |
| **kilo** | API key / Bearer | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | Unknown | ❓ | No adapter in codebase; limited public documentation |
| **zai** | Bearer token | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ✅ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | OpenAI-compatible | ✅ | No adapter in codebase; GLM models on NVIDIA NIM |
| **nvidia** | Bearer token | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | ✅ | OpenAI-compatible | ✅ | Full adapter with NIM-specific request shaping for reasoning models |
| **agnes** | Bearer token | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❓ | ❓ | ✅ | ❓ | ✅ | ✅ | ✅ | ✅ | OpenAI-compatible | ✅ | Full OpenAI-compatible adapter |
| **aliyun-maas** | API key | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | Proprietary | ❌ | No adapter in codebase; Alibaba Cloud MaaS API is not OpenAI-compatible |
| **cloudflare-gateway** | Service token | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | ❓ | OpenAI-compatible | ✅ | No adapter in codebase; proxies through Cloudflare AI Gateway |
| **opencode-zen** | Bearer token | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | ✅ | OpenAI-compatible | ✅ | Full OpenAI-compatible adapter; proxy across multiple providers |

## Legend — Column Definitions

| Column | Description |
|--------|-------------|
| **Auth** | Authentication method used by the provider |
| **Chat** | Non-streaming chat completions (`/chat/completions` or native equivalent) |
| **Streaming** | Server-sent events (SSE) streaming support |
| **Tool Call** | Single tool/function calling via `tools` parameter |
| **Parallel Tool** | Multiple simultaneous tool calls in one response |
| **JSON Mode** | `response_format: { type: "json_object" }` or native equivalent |
| **Structured** | Schema-enforced structured output (e.g. JSON Schema, Pydantic) |
| **Thinking** | Chain-of-thought / reasoning mode (extended thinking tokens) |
| **Vision** | Multi-modal input with image URLs or base64 images |
| **Embed** | Text embeddings endpoint |
| **Audio** | Audio input (whisper-style) or output |
| **Func Call** | Legacy `functions` parameter (pre-`tools` API) |
| **Usage Track** | Token usage returned in response for cost tracking |
| **Finish Reasons** | Standardized `finish_reason` values (`stop`, `tool_calls`, `length`, etc.) |
| **Rate Limit Headers** | `Retry-After` or provider-specific rate limit headers parsed |
| **Error Format** | Normalized error shape (OpenAI-compatible or provider-native) |
| **OpenAI Compat** | Whether the native API shape matches the OpenAI chat completions format |
| **Native API** | Whether the provider has its own non-OpenAI API shape |
| **Notes** | Additional context about the provider's capabilities or adapter status |

## How to Read This Matrix

1. **✅ Full adapter** — The provider has a complete, tested adapter in `internal/provider/<name>/`. All capabilities marked ✅ are expected to work through Conductor.
2. **🔲 Stub** — The provider is registered and appears in `/v1/models` if configured with a static model list, but requests will return "not implemented" errors until the adapter is completed.
3. **❓ Unknown** — The provider is not in the Conductor codebase. Capability assessments are based on public documentation and may not reflect Conductor-specific behavior.

## Adding a New Provider

See `docs/provider-compatibility/provider-interface.md` and `internal/provider/interface.go` for the implementation contract. Use an existing adapter as a template — most new providers can embed `openaibase.Base` if they expose an OpenAI-compatible endpoint.
