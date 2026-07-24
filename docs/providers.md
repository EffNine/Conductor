# Provider Setup Guide

Conductor has provider adapters for 11 upstream services. The following table shows what is currently implemented versus stubbed.

## Provider Status

| Provider | Chat | Embeddings | Streaming | List Models | GetPricing | Notes |
|----------|------|------------|-----------|-------------|------------|-------|
| OpenAI | ✅ | ✅ | ✅ | ✅ | ✅ (static map) | Reference adapter |
| Anthropic | ❌ Planned | ❌ | ❌ | ❌ | ❌ | Stub registered when enabled |
| Gemini | ❌ Planned | ❌ | ❌ | ❌ | ❌ | Stub registered when enabled |
| DeepSeek | ❌ Planned | ❌ | ❌ | ❌ | ❌ | Stub registered when enabled |
| OpenRouter | ❌ Planned | ❌ | ❌ | ❌ | ❌ | Stub registered when enabled |
| Groq | ❌ Planned | ❌ | ❌ | ❌ | ❌ | Stub registered when enabled |
| Ollama | ✅ | ✅ | ✅ | ✅ | — (use `cost.rates`) | Local `localhost:11434/v1` or Cloud via `OLLAMA_API_KEY` → `ollama.com/v1` |
| LM Studio | ❌ Planned | ❌ | ❌ | ❌ | ❌ | Stub registered when enabled |
| Generic | ❌ Planned | ❌ | ❌ | ❌ | ❌ | Stub registered when enabled |
| OpenCode | ✅ | ✅ | ✅ | ✅ | ✅ (static map) | Zen base `https://opencode.ai/zen/v1`; chat/completions models only |
| NVIDIA NIM | ✅ | ✅ | ✅ | ✅ | ✅ (static map) | Hosted `integrate.api.nvidia.com` or self-hosted; see [Model Reachability](#model-reachability-nvidia-nim) |
| Nous Portal | ✅ | ✅ | ✅ | ✅ | ✅ (empty map) | Subscription service; configure cost.rates |
| xAI | ✅ | ✅ | ✅ | ✅ | ✅ (static map) | OpenAI-compatible base `https://api.x.ai/v1` |
| Agnes AI | ✅ | ✅ | ✅ | ✅ | ✅ (empty map) | OpenAI-compatible base `https://apihub.agnes-ai.com/v1`; configure cost.rates |

A stub provider is present in the registry and will appear in health checks and the model catalog if you configure a **static model list** for it. You can also define routes and aliases pointing to stub providers; chat/embeddings requests to them will return errors until the adapter is implemented.

## OpenAI

### Setup

1. Get an API key from [platform.openai.com](https://platform.openai.com/api-keys)
2. Set the environment variable:

```bash
export OPENAI_API_KEY=sk-your-key-here
```

### Configuration

```yaml
providers:
  openai:
    enabled: true
    api_key: "${OPENAI_API_KEY}"
    base_url: "https://api.openai.com/v1"
    timeout: 60s
    max_retries: 3
```

### Pricing

OpenAI returns a static pricing map for known chat and embedding models. Prices are in USD per 1,000 tokens. You can override or extend rates with `cost.rates`.

## Ollama (local and Cloud)

Ollama uses the shared OpenAI-compatible adapter (`/v1/chat/completions`).

### Ollama Cloud

1. Create an API key at [ollama.com/settings/keys](https://ollama.com/settings/keys)
2. Set the environment variable (auto-enables and defaults to `https://ollama.com/v1`):

```bash
export OLLAMA_API_KEY=your_api_key
```

### Local Ollama

Enable in YAML (or set `OLLAMA_BASE_URL`). No API key is required locally:

```yaml
providers:
  ollama:
    enabled: true
    base_url: "http://localhost:11434/v1"
    models:
      - "llama3.1:8b"
      - "mistral:7b"
```

`OLLAMA_BASE_URL` overrides the host when Ollama is already enabled (YAML or `OLLAMA_API_KEY`). It does not enable the provider by itself. If both `OLLAMA_API_KEY` and `OLLAMA_BASE_URL` are set, the base URL wins and the key is still sent as `Authorization: Bearer`.

## Other Providers

Several adapters are stubs or share the OpenAI-compatible base. You can still configure them for:

- **Static model lists** advertised in `/v1/models`
- **Routes and aliases** resolved by the router
- **Manual cost rates** under `cost.rates`
- **Health checks** (return `not implemented` for stubs)

To complete a stub adapter, implement the `provider.Provider` interface in `internal/provider/<name>/provider.go`.

## Static Model Lists

For providers without dynamic `ListModels` support, configure a static list:

```yaml
providers:
  ollama:
    enabled: true
    base_url: "http://localhost:11434/v1"
    models:
      - "llama3.1:8b"
      - "mistral:7b"

  lmstudio:
    enabled: true
    base_url: "http://localhost:1234/v1"
    models:
      - "loaded-model-name"
```

Static models appear in `/v1/models` when the provider's dynamic listing is unavailable.

### Curated-only mode

To shrink large dynamic catalogs such as NVIDIA NIM while keeping other
providers dynamic, set `catalog.curated_only: true` and list Model IDs under
that provider's `models` field. See [Configuration — Curated catalog](configuration.md#curated-catalog).

```yaml
catalog:
  curated_only: true

providers:
  nvidia_nim:
    enabled: true
    models:
      - "deepseek-ai/deepseek-v4-flash"
      - "meta/llama-3.1-8b-instruct"
```

## Model Routing

Routes map a user-facing **Model ID** to a provider and optional upstream **Provider Model ID**.

```yaml
routes:
  "gpt-4o":
    provider: openai
  "claude-sonnet":
    provider: anthropic
    model_id: "claude-3-5-sonnet-20241022"
```

When a request uses a provider-prefixed ID such as `openai/gpt-4o`, the gateway strips the prefix and resolves `gpt-4o` against the route. The provider prefix must match the route's provider.

## Aliases

Aliases are operator shortcuts. They do not appear in `/v1/models`.

```yaml
aliases:
  "fast": "gpt-4o-mini"
  "smart": "gpt-4o"
  "coding": "deepseek-chat"
```

## Fallback Chains

If a provider fails, the gateway tries the next configured provider:

```yaml
fallbacks:
  "deepseek-chat":
    - provider: deepseek
    - provider: openrouter
      model_id: "deepseek/deepseek-chat"
    - provider: groq
      model_id: "deepseek-r1-distill-llama-70b"
```

## Provider Health Monitoring

`/api/health` calls `HealthCheck` on each registered provider (provider-level liveness, typically `GET /models`). This does **not** prove that every listed model accepts chat completions.

```bash
curl http://localhost:8080/api/health \
  -H "Authorization: Bearer your-key"
```

Example response:

```json
{
  "providers": [
    {
      "name": "openai",
      "healthy": true,
      "latency_ms": 245,
      "last_error": null,
      "checked_at": "2026-07-19T10:30:00Z"
    }
  ]
}
```

## Model Reachability (NVIDIA NIM)

NVIDIA NIM’s `GET /v1/models` returns the full catalog — including free hosted endpoints that are temporarily down, retired, or not chat-capable. There is no catalog field for “callable right now.”

Conductor probes models with a minimal `POST /chat/completions` (`max_tokens: 16`) and can auto-hide failures from `/v1/models`.

### Defaults

- Enabled for **all registered providers** by default (`providers: []`)
- During the first probe pass, `/v1/models` keeps the full catalog (no flicker)
- After that pass, never-probed models stay visible (`unknown_as_reachable: true`); recovering/unhealthy models are hidden
- Failed probes retry on exponential backoff (default `30s` → capped at `12h`)
- `unhealthy_threshold: 1`, full-pass interval `2h`, concurrency `3`

### Configuration

```yaml
health:
  models:
    enabled: true
    hide_unreachable: true
    check_interval: 2h
    timeout: 60s
    concurrency: 3
    unhealthy_threshold: 1
    providers: []
    unknown_as_reachable: true
    backoff:
      enabled: true
      initial_delay: 30s
      max_delay: 12h
      multiplier: 3.5
      jitter_fraction: 0.2
    error_tracking:
      enabled: true
      window: 5m
      unhealthy_threshold: 0.15
      recovery_threshold: 0.05
```

Disable entirely with `health.models.enabled: false`. To probe only NIM:

```yaml
health:
  models:
    providers:
      - nvidia_nim
```

### Inspect status

```bash
# Catalog with reachability fields
curl http://localhost:8080/api/models \
  -H "Authorization: Bearer your-key"

# Models hidden from /v1/models
curl "http://localhost:8080/api/models?include_unreachable=true" \
  -H "Authorization: Bearer your-key"

# Probe cache only
curl http://localhost:8080/api/models/status \
  -H "Authorization: Bearer your-key"
```

Rate limits (`429`) and auth errors do not mark a model offline. Live chat successes/failures also update the cache, so a model can be hidden (or restored) without waiting for the next probe cycle.

See [Configuration — Model reachability](configuration.md#model-reachability) and [API — Model Reachability](api.md#model-reachability).

## Troubleshooting

### Provider Returns 401 Unauthorized

- Check the API key is correct
- Ensure the environment variable is set
- Verify no typos in config

### Provider Returns 429 Too Many Requests

- You've hit the provider's rate limit
- Wait and retry, or configure a fallback chain
- For NIM model probes, lower `health.models.concurrency` or raise `check_interval`

### Local Provider (Ollama/LM Studio) Not Responding

- Ensure the service is running
- Check `base_url` is correct
- Increase `timeout` in config

### Model Not in `/v1/models`

- Add the provider's model list if the adapter is a stub
- Check the route uses the bare Model ID, not a provider prefix
- Aliases are intentionally excluded from the model list
- For NVIDIA NIM: the model may have failed reachability probes — check `/api/models?include_unreachable=true` or `/api/models/status`
