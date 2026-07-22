# Conductor

> One API key. One endpoint. Every model you pay for — routed, metered, and kept honest.

[![CI](https://github.com/EffNine/conductor/actions/workflows/ci.yaml/badge.svg)](https://github.com/EffNine/conductor/actions/workflows/ci.yaml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## One-Liner

```bash
docker run -d -p 8080:8080 \
  -e CONDUCTOR_API_KEY=my-key \
  -e OPENAI_API_KEY=sk-... \
  -v conductor-data:/app/data \
  effnine/conductor:latest
```

## What It Is

Conductor is a single-operator, self-hosted AI gateway. Drop it between your coding tools and the dozen AI subscriptions you already own. Clients see a tidy, merged model picker. You see unified usage, cost, and health — all behind one gateway key.

Think of it as a tiny traffic controller for your AI spend: route by model, fall back when a provider hiccups, and stop guessing which endpoint is actually online.

## Features

- **One Key to Rule Them All** — Point OpenAI-compatible clients (Continue, Aider, Open WebUI, Claude Code, custom apps) at a single endpoint with one key.
- **Merged Model Picker** — `GET /v1/models` aggregates every configured provider. Each Model ID is provider-prefixed (e.g. `openai/gpt-4o`, `nvidia_nim/meta/llama-3.1-8b-instruct`) so the listed ID routes directly.
- **Curated Catalog Mode** — Set `catalog.curated_only: true` and advertise only the Static Model List under each provider. Perfect for shrinking giant catalogs like NVIDIA NIM down to the models you actually use.
- **Model Online Status** — Background reachability probes run on startup and on interval, hide models that fail, and expose status on `/api/models` and `/api/models/status`. No more picking a model only to discover it is retired.
- **Explicit Routing + Aliases** — `routes` and `aliases` map bare Model IDs to providers and upstream slugs. Provider-prefixed catalog IDs need no route entry.
- **Fallback Chains** — Try backup providers when the primary fails, without the client lifting a finger.
- **Usage & Cost Tracking** — Per-request records with tokens, latency, extra counters (duration, characters), and USD cost. Aggregates totals plus per-provider and per-model breakdowns in SQLite.
- **Auto Model Selection** — Send `"model": "auto"` and let the gateway pick the best available model from a configured provider using task classification, reachability, cost, and probe latency.
- **Dashboard API** — Models, model status, usage, costs, provider health, and request logs — all behind the same gateway key.
- **Docker & Fly.io** — Single container with embedded SQLite; one-shot deploy via `./scripts/fly-deploy.sh`.

## Quick Start

### Prerequisites

- Docker, **or** Go 1.21+, `gcc`, and CGO enabled (SQLite via `mattn/go-sqlite3`)
- At least one upstream provider API key

### Run with Docker

```bash
cat > .env << EOF
CONDUCTOR_API_KEY=your-secret-gateway-key
OPENAI_API_KEY=sk-your-openai-key
EOF

docker run -d \
  --name conductor \
  -p 8080:8080 \
  --env-file .env \
  -v conductor-data:/app/data \
  effnine/conductor:latest
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

Only `CONDUCTOR_API_KEY` is required to boot. Setting a provider env var auto-enables that provider (no `config.yaml` needed):

```bash
export CONDUCTOR_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key
# Also supported: ANTHROPIC_API_KEY, GEMINI_API_KEY, DEEPSEEK_API_KEY,
# OPENROUTER_API_KEY, GROQ_API_KEY, OPENCODE_API_KEY, NVIDIA_NIM_API_KEY,
# NOUS_PORTAL_API_KEY, OLLAMA_API_KEY (Ollama Cloud → https://ollama.com/v1)
```

LM Studio is enabled via `config.yaml` (`enabled` / `base_url`). Local Ollama uses the same (optional `OLLAMA_BASE_URL` host override). Setting `OLLAMA_API_KEY` alone enables Ollama Cloud.

### Advanced Configuration

Copy [`config/config.example.yaml`](config/config.example.yaml) to `config.yaml` (searched in `.`, `./config`, `/etc/conductor`) for routes, aliases, fallbacks, static model lists, auto mode, and cost rates:

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

catalog:
  curated_only: false

health:
  models:
    enabled: true
    providers: []
    check_interval: 12h
    hide_unreachable: true
    unknown_as_reachable: false
    unhealthy_threshold: 1

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
                                       ↓              ↓                              ↓
                                   Alias/Routes   Health/Probes               Usage + Cost → SQLite
```

- **Auth** — Single gateway API key via `CONDUCTOR_API_KEY`.
- **Rate Limiter** — Global and per-provider limits.
- **Router** — Alias → route → provider-prefix dispatch → fallbacks.
- **Provider Adapters** — Common `Provider` interface; OpenAI-compatible passthrough plus a custom Anthropic Messages adapter.
- **Catalog** — Merges provider model lists (always provider-prefixed) with static fallback; optional curated-only allowlist and reachability filter.
- **Model Prober** — Probes providers on startup/redeploy and on interval; advertises only probe-passed models after the first pass.
- **Auto Selector** — Classifies requests by task and scores candidates by health, cost, and latency for `"model": "auto"`.
- **Usage Tracker** — Persists usage and estimated cost to SQLite.

See [Architecture](docs/architecture.md) for details.

## Supported Providers

| Provider | Chat | Embeddings | Streaming | List Models | Pricing |
|----------|------|------------|-----------|-------------|---------|
| OpenAI | ✅ | ✅ | ✅ | ✅ | ✅ |
| Anthropic | ✅ | ❌ | ✅ | ✅ (static) | ✅ |
| Gemini | ✅ | ✅ | ✅ | ✅ | ✅ |
| DeepSeek | ✅ | ✅ | ✅ | ✅ | ✅ |
| OpenRouter | ✅ | ✅ | ✅ | ✅ | ✅ |
| Groq | ✅ | ✅ | ✅ | ✅ | ✅ |
| OpenCode | ✅ | ✅ | ✅ | ✅ | ✅ |
| NVIDIA NIM | ✅ | ✅ | ✅ | ✅ | ✅ |
| Nous Portal | ✅ | ✅ | ✅ | ✅ | — (subscription; use `cost.rates`) |
| Ollama | ✅ | ✅ | ✅ | ✅ | — (configure `cost.rates`) |
| LM Studio | ✅ | ✅ | ✅ | ✅ | — (configure `cost.rates`) |
| Generic OpenAI-compatible | ✅ | ✅ | ✅ | ✅ | — (configure `cost.rates`) |

Most providers use the shared OpenAI-compatible adapter (`internal/provider/openaibase`). Anthropic uses a dedicated Messages API adapter. Embeddings are not supported by Anthropic.

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

# Auto mode status
curl http://localhost:8080/api/auto/status \
  -H "Authorization: Bearer your-key"

# Provider health
curl http://localhost:8080/api/health \
  -H "Authorization: Bearer your-key"

# Registered providers
curl http://localhost:8080/api/providers \
  -H "Authorization: Bearer your-key"

# Usage totals and breakdowns
curl http://localhost:8080/api/usage \
  -H "Authorization: Bearer your-key"

# Cost summary (stub; full breakdown coming soon)
curl http://localhost:8080/api/usage/costs \
  -H "Authorization: Bearer your-key"

# Recent request logs
curl http://localhost:8080/api/logs \
  -H "Authorization: Bearer your-key"
```

Model online status (especially for NVIDIA NIM free vs unreachable endpoints) is documented in [Configuration](docs/configuration.md#model-reachability), [API](docs/api.md#model-reachability), and [Providers](docs/providers.md#model-reachability-nvidia-nim).

`GET /api/config` and `PUT /api/config/reload` are currently stubs; a restart is required for config changes.

## Deploy

### Fly.io (recommended free deploy)

```bash
export CONDUCTOR_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key   # or another provider key
./scripts/fly-deploy.sh
# or: make fly-deploy
```

Canonical config is [`fly.toml`](fly.toml) (`primary_region = "sin"`). Match the volume region when creating one:

```bash
REGION=sin APP_NAME=conductor-you ./scripts/fly-deploy.sh
```

Gateway URL: `https://<app-name>.fly.dev` (use `/v1` as the OpenAI base URL). Machines may cold-start when `min_machines_running = 0`.

See [Deployment Guide](docs/deployment.md) for Railway, Render, and other options.

## Development

```bash
git clone https://github.com/EffNine/conductor.git
cd conductor

# Requires Go 1.21+, gcc, and CGO (do not set CGO_ENABLED=0)
make build
make test

export CONDUCTOR_API_KEY=test-key
export OPENAI_API_KEY=sk-test
make run
# binary: ./bin/conductor
```

Useful targets: `make lint`, `make docker-build` (uses context `.`; prefer `docker build -f deployments/Dockerfile -t effnine/conductor:latest .`).

## Documentation

- [Quick Start Guide](docs/quickstart.md)
- [Configuration Reference](docs/configuration.md)
- [Provider Setup](docs/providers.md)
- [API Reference](docs/api.md)
- [Architecture](docs/architecture.md)
- [Deployment Guide](docs/deployment.md)
- [Contributing](docs/contributing.md)

## License

MIT License — see [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! See [Contributing Guide](docs/contributing.md) for details.
