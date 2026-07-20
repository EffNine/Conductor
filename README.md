# Novexa Gateway

> A single-operator, self-hosted AI gateway that exposes one OpenAI-compatible API key and routes requests across multiple upstream AI provider subscriptions.

[![CI](https://github.com/EffNine/novexa-gateway/actions/workflows/ci.yaml/badge.svg)](https://github.com/EffNine/novexa-gateway/actions/workflows/ci.yaml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## One-Liner

```bash
docker run -d -p 8080:8080 \
  -e NOVEXA_API_KEY=my-key \
  -e OPENAI_API_KEY=sk-... \
  -v novexa-data:/app/data \
  novexa/gateway:latest
```

## Features

- **Single API Key** — Point OpenAI-compatible clients (Continue, Aider, Open WebUI, custom apps) at one endpoint with one key
- **Merged Model Picker** — `/v1/models` aggregates catalogs from all configured providers; every Model ID is provider-prefixed (e.g. `openai/gpt-4o`, `nvidia_nim/deepseek-ai/deepseek-v4-flash`) so the listed ID routes when selected
- **Curated Models Only** — Optional `catalog.curated_only` advertises only the Static Model List under `providers.*.models` (shrink large catalogs such as NVIDIA NIM)
- **Model Online Status** — Optional probes hide unreachable models (especially NVIDIA NIM); startup pass then once per day by default, with status on `/api/models` and `/api/models/status`
- **Explicit Model Routing** — Optional `routes` and `aliases` for bare Model IDs; provider-prefixed catalog IDs need no route entry- **Fallback Chains** — Try backup providers when the primary fails
- **Usage & Cost Tracking** — Per-request records with tokens, latency, and USD cost (provider per-request → `GetPricing` → manual `cost.rates` → unknown)
- **Dashboard API** — Models, model status, usage, costs, health, providers, and logs behind the same gateway key
- **Docker & Fly.io** — Single container with SQLite; one-shot deploy via `./scripts/fly-deploy.sh`

## Quick Start

### Prerequisites

- Docker, **or** Go 1.21+, `gcc`, and CGO enabled (SQLite via `mattn/go-sqlite3`)
- At least one upstream provider API key

### Run with Docker

```bash
cat > .env << EOF
NOVEXA_API_KEY=your-secret-gateway-key
OPENAI_API_KEY=sk-your-openai-key
EOF

docker run -d \
  --name novexa-gateway \
  -p 8080:8080 \
  --env-file .env \
  -v novexa-data:/app/data \
  novexa/gateway:latest
```

### Test It

Catalog IDs are always provider-prefixed. Use the ID returned by `/v1/models`:

```bash
# List models
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer your-secret-gateway-key"

# Chat (no config.yaml routes required for prefixed IDs)
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-secret-gateway-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "openai/gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

Bare IDs like `gpt-4o` only work if you add a matching `routes` entry (or alias) in `config.yaml`.

## Configuration

### Minimal Setup

Only `NOVEXA_API_KEY` is required to boot. Setting a provider env var auto-enables that provider (no `config.yaml` needed):

```bash
export NOVEXA_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key
# Also supported: ANTHROPIC_API_KEY, GEMINI_API_KEY, DEEPSEEK_API_KEY,
# OPENROUTER_API_KEY, GROQ_API_KEY, OPENCODE_API_KEY, NVIDIA_NIM_API_KEY,
# NOUS_PORTAL_API_KEY, OLLAMA_API_KEY (Ollama Cloud → https://ollama.com/v1)
```

LM Studio is enabled via `config.yaml` (`enabled` / `base_url`). Local Ollama uses the same (optional `OLLAMA_BASE_URL` host override). Setting `OLLAMA_API_KEY` alone enables Ollama Cloud.

### Advanced Configuration

Copy [`config/config.example.yaml`](config/config.example.yaml) to `config.yaml` (searched in `.`, `./config`, `/etc/novexa`) for routes, aliases, fallbacks, static model lists, and cost rates:

```yaml
routes:
  "gpt-4o":
    provider: openai
  "claude-sonnet":
    provider: anthropic
    model_id: "claude-3-5-sonnet-20241022"

aliases:
  "fast": "gpt-4o-mini"
  "smart": "gpt-4o"

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

**Routing tip:** Prefer provider-prefixed Model IDs from `/v1/models` for upstream slugs that contain dots (e.g. `meta/llama-3.1-8b-instruct`). Viper’s `.` key delimiter can silently break route keys that contain a dot.

See [Configuration Reference](docs/configuration.md) for full details.

## Architecture

```
Client → API Key Check → Rate Limit → Validate → Route → Provider Adapter → Normalize → Response
```

- **Auth** — Single gateway API key via `NOVEXA_API_KEY`
- **Rate Limiter** — Global and per-provider limits
- **Router** — Alias → route → provider-prefix dispatch → fallbacks
- **Provider Adapters** — Common `Provider` interface
- **Catalog** — Merges provider model lists (always provider-prefixed) with static fallback; optional curated-only allowlist and reachability filter
- **Model Prober** — Minimal chat probes (default: `nvidia_nim`) to hide unreachable catalog entries; recurring probe interval defaults to `24h`
- **Usage Tracker** — Persists usage and estimated cost to SQLite
See [Architecture](docs/architecture.md) for details.

## Supported Providers

| Provider | Chat | Embeddings | Streaming | List Models | Pricing |
|----------|------|------------|-----------|-------------|---------|
| OpenAI | ✅ | ✅ | ✅ | ✅ | ✅ (static map) |
| Anthropic | ✅ | ❌ | ✅ | ✅ (static) | ✅ (static map) |
| Gemini | ✅ | ✅ | ✅ | ✅ | ✅ (static map) |
| DeepSeek | ✅ | ✅ | ✅ | ✅ | ✅ (static map) |
| OpenRouter | ✅ | ✅ | ✅ | ✅ | ✅ (static map) |
| Groq | ✅ | ✅ | ✅ | ✅ | ✅ (static map) |
| OpenCode | ✅ | ✅ | ✅ | ✅ | ✅ (static map) |
| NVIDIA NIM | ✅ | ✅ | ✅ | ✅ | ✅ (static map) |
| Nous Portal | ✅ | ✅ | ✅ | ✅ | ✅ (empty map; use `cost.rates`) |
| Ollama | ✅ | ✅ | ✅ | ✅ | — (use `cost.rates`) |
| LM Studio | ✅ | ✅ | ✅ | ✅ | — (use `cost.rates`) |

Gemini, DeepSeek, OpenRouter, Groq, OpenCode, NVIDIA NIM, Nous Portal, Ollama, and LM Studio use the shared OpenAI-compatible adapter. Anthropic uses the Messages API (embeddings are not supported).

## Dashboard API

Authenticated with the same gateway API key (`GET /health` is public):

```bash
# Merged catalog (includes reachability when probing is enabled)
curl http://localhost:8080/api/models \
  -H "Authorization: Bearer your-key"

# Per-model online status cache
curl http://localhost:8080/api/models/status \
  -H "Authorization: Bearer your-key"

# Include models hidden from /v1/models
curl "http://localhost:8080/api/models?include_unreachable=true" \
  -H "Authorization: Bearer your-key"

curl http://localhost:8080/api/health \
  -H "Authorization: Bearer your-key"

curl http://localhost:8080/api/providers \
  -H "Authorization: Bearer your-key"

curl http://localhost:8080/api/usage \
  -H "Authorization: Bearer your-key"

curl http://localhost:8080/api/usage/costs \
  -H "Authorization: Bearer your-key"

curl http://localhost:8080/api/logs \
  -H "Authorization: Bearer your-key"
```

Model online status (especially for NVIDIA NIM free vs unreachable endpoints) is documented in [Configuration](docs/configuration.md#model-reachability), [API](docs/api.md#model-reachability), and [Providers](docs/providers.md#model-reachability-nvidia-nim).

`GET /api/config` and `PUT /api/config/reload` exist; config JSON is still a stub (`coming soon`). Reload works when a reload callback is wired at startup.

## Deploy

### Fly.io (recommended free deploy)

```bash
export NOVEXA_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key   # or another provider key
./scripts/fly-deploy.sh
# or: make fly-deploy
```

Canonical config is [`fly.toml`](fly.toml) (`primary_region = "sin"`). Match the volume region when creating one:

```bash
REGION=sin APP_NAME=novexa-gateway-you ./scripts/fly-deploy.sh
```

Gateway URL: `https://<app-name>.fly.dev` (use `/v1` as the OpenAI base URL). Machines may cold-start when `min_machines_running = 0`.

See [Deployment Guide](docs/deployment.md) for Railway, Render, and other options.

## Development

```bash
git clone https://github.com/EffNine/novexa-gateway.git
cd novexa-gateway

# Requires Go 1.21+, gcc, and CGO (do not set CGO_ENABLED=0)
make build
make test

export NOVEXA_API_KEY=test-key
export OPENAI_API_KEY=sk-test
make run
# binary: ./bin/novexa-gateway
```

Useful targets: `make lint`, `make docker-build` (uses context `.`; prefer `docker build -f deployments/Dockerfile -t novexa/gateway:latest .`).

## Documentation

- [Quick Start Guide](docs/quickstart.md)
- [Configuration Reference](docs/configuration.md)
- [Provider Setup](docs/providers.md)
- [API Reference](docs/api.md)
- [Deployment Guide](docs/deployment.md)
- [Contributing](docs/contributing.md)

## License

MIT License — see [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! See [Contributing Guide](docs/contributing.md) for details.
