# Novexa Gateway

> A single-operator, self-hosted AI gateway that exposes one OpenAI-compatible API key and routes requests across multiple upstream AI provider subscriptions.

[![CI](https://github.com/EffNine/novexa-gateway/actions/workflows/ci.yaml/badge.svg)](https://github.com/EffNine/novexa-gateway/actions/workflows/ci.yaml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## One-Liner

```bash
docker run -d -p 8080:8080 \
  -e NOVEXA_API_KEY=my-key \
  -e OPENAI_API_KEY=sk-... \
  ghcr.io/effnine/novexa-gateway:latest
```

## Features

- **Single API Key** — Connect OpenAI-compatible clients (VS Code, Claude Code, Continue, Aider, Open WebUI, custom apps) to one endpoint with one key
- **Merged Model Picker** — `/v1/models` aggregates catalogs from all configured providers; every Model ID is provider-prefixed (e.g. `nvidia_nim/deepseek-ai/deepseek-v4-flash`) so it routes when selected
- **Model Online Status** — Optional probes hide unreachable models (especially NVIDIA NIM free endpoints that appear in `/models` but fail inference); status on `/api/models` and `/api/models/status`
- **Explicit Model Routing** — Map Model IDs to providers and optional upstream Provider Model IDs; aliases for operator convenience
- **Fallback Chains** — Try backup providers when the primary fails
- **Provider-Prefix Routing** — Use `provider/model-id` in clients; gateway strips the prefix before route lookup
- **Usage Tracking** — Per-request records with token counters, extra counters for non-token providers, latency, and cost source
- **Cost Estimation** — Cost resolved via provider per-request cost → `GetPricing` → manual `cost.rates` → unknown (USD only)
- **Dashboard API** — `/api/models`, `/api/models/status`, `/api/usage`, `/api/health`, `/api/logs`, all protected by the gateway API key
- **Docker Ready** — Single container with SQLite
- **Free Cloud Deploy** — Fly.io one-shot: `./scripts/fly-deploy.sh` (also Railway / Render)

## Quick Start

### Prerequisites

- Docker installed
- At least one AI provider API key

### Run the Gateway

```bash
# Create a .env file
cat > .env << EOF
NOVEXA_API_KEY=your-secret-gateway-key
OPENAI_API_KEY=sk-your-openai-key
EOF

# Run with Docker
docker run -d \
  --name novexa-gateway \
  -p 8080:8080 \
  --env-file .env \
  -v novexa-data:/app/data \
  ghcr.io/effnine/novexa-gateway:latest
```

### Test It

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-secret-gateway-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

## Configuration

### Minimal Setup

Provider API keys and the gateway key are required. Everything else has defaults.

```bash
export NOVEXA_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key
```

### Advanced Configuration

Create a `config.yaml` for routes, aliases, fallbacks, static model lists, and cost rates:

```yaml
routes:
  "gpt-4o":
    provider: openai
  "claude-sonnet-4-20250514":
    provider: anthropic
    model_id: "claude-3-5-sonnet-20241022"

aliases:
  "fast": "gpt-4o-mini"
  "smart": "gpt-4o"
  "coding": "deepseek-chat"

fallbacks:
  "deepseek-chat":
    - provider: deepseek
    - provider: openrouter
      model_id: "deepseek/deepseek-chat"

cost:
  rates:
    - provider: openai
      model_id: "gpt-4o"
      unit: token
      unit_size: 1000
      input_usd: 0.0025
      output_usd: 0.010
```

See [Configuration Reference](docs/configuration.md) for full details.

## Architecture

```
Client → API Key Check → Rate Limit → Validate → Route → Provider Adapter → Normalize → Response
```

The gateway is a Go/Fiber middleware pipeline. Stages are independent and testable.

- **Auth**: Single gateway API key via `NOVEXA_API_KEY`
- **Rate Limiter**: Global and per-provider limits
- **Router**: Model→provider mapping with alias resolution, provider prefix stripping, and fallback chains
- **Provider Adapters**: Common `Provider` interface
- **Catalog**: Merges provider model lists with prefix deduplication and static fallback
- **Usage Tracker**: Persists usage and estimated cost to SQLite

See [Architecture](docs/architecture.md) for details.

## Documentation

- [Quick Start Guide](docs/quickstart.md)
- [Configuration Reference](docs/configuration.md)
- [Provider Setup](docs/providers.md)
- [API Reference](docs/api.md)
- [Deployment Guide](docs/deployment.md)
- [Contributing](docs/contributing.md)

## Supported Providers

| Provider | Status | Chat | Embeddings | Streaming | List Models | Pricing |
|----------|--------|------|------------|-----------|-------------|---------|
| OpenAI | Implemented | ✅ | ✅ | ✅ | ✅ | ✅ (static map) |
| OpenCode | Implemented | ✅ | ✅ | ✅ | ✅ | ✅ (static map) |
| NVIDIA NIM | Implemented | ✅ | ✅ | ✅ | ✅ | ✅ (static map) |
| Nous Portal | Implemented | ✅ | ✅ | ✅ | ✅ | ✅ (empty map) |
| Anthropic | Stub | ❌ | ❌ | ❌ | ❌ | ❌ |
| Gemini | Stub | ❌ | ❌ | ❌ | ❌ | ❌ |
| DeepSeek | Stub | ❌ | ❌ | ❌ | ❌ | ❌ |
| OpenRouter | Stub | ❌ | ❌ | ❌ | ❌ | ❌ |
| Groq | Stub | ❌ | ❌ | ❌ | ❌ | ❌ |
| Ollama | Stub | ❌ | ❌ | ❌ | ❌ | ❌ |
| LM Studio | Stub | ❌ | ❌ | ❌ | ❌ | ❌ |
| Generic | Stub | ❌ | ❌ | ❌ | ❌ | ❌ |

Stubs are registered when enabled and can be completed by implementing the provider adapter. Routes, aliases, static model lists, and manual cost rates work for all providers even while adapters are stubs.

## Dashboard API

All endpoints use the same gateway API key:

```bash
# Merged model catalog (includes reachability when probing is enabled)
curl http://localhost:8080/api/models \
  -H "Authorization: Bearer your-key"

# Per-model online status cache
curl http://localhost:8080/api/models/status \
  -H "Authorization: Bearer your-key"

# Include models hidden from /v1/models
curl "http://localhost:8080/api/models?include_unreachable=true" \
  -H "Authorization: Bearer your-key"

# Provider health
curl http://localhost:8080/api/health \
  -H "Authorization: Bearer your-key"

# Usage totals and breakdowns
curl http://localhost:8080/api/usage \
  -H "Authorization: Bearer your-key"

# Recent request logs
curl http://localhost:8080/api/logs \
  -H "Authorization: Bearer your-key"
```

Model online status (especially for NVIDIA NIM free vs unreachable endpoints) is documented in [Configuration](docs/configuration.md#model-reachability), [API](docs/api.md#model-reachability), and [Providers](docs/providers.md#model-reachability-nvidia-nim).

## Development

```bash
# Clone
git clone https://github.com/EffNine/novexa-gateway.git
cd novexa-gateway

# Build
make build

# Test
make test

# Run
export NOVEXA_API_KEY=test-key
export OPENAI_API_KEY=sk-test
./bin/gateway
```

## License

MIT License — see [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! See [Contributing Guide](docs/contributing.md) for details.
