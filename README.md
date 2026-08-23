# Conductor

> One API key. One endpoint. Every model you pay for — routed, metered, and kept honest.

[![CI](https://github.com/EffNine/conductor/actions/workflows/ci.yaml/badge.svg)](https://github.com/EffNine/conductor/actions/workflows/ci.yaml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/EffNine/conductor?filename=go.mod)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Docker Pulls](https://img.shields.io/docker/pulls/effnine/conductor)](https://hub.docker.com/r/effnine/conductor)

---

Conductor is a self-hosted **OpenAI-compatible AI gateway** that merges multiple provider subscriptions into one endpoint. Point your coding tools at a single URL and see every model you own — with unified usage, cost, and health tracking.

For complex tasks, Conductor also runs a persistent **agent orchestration runtime**: it classifies intent, generates plans, routes across providers intelligently, and verifies results.

## Quick Start

```bash
docker run -d -p 8080:8080 \
  -e CONDUCTOR_API_KEY=my-key \
  -e OPENAI_API_KEY=sk-... \
  -v conductor-data:/app/data \
  effnine/conductor:latest
```

Test it:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer my-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/gpt-4o","messages":[{"role":"user","content":"Hello!"}]}'
```

> **Model IDs**: with a single provider configured, bare IDs (`gpt-4o`) and
> provider-prefixed IDs (`openai/gpt-4o`) both work with zero config; virtual
> categories (`auto`, `fast`, `coding`, …) always do. With multiple providers,
> bare IDs need a `routes:` entry (or use prefixed IDs). The full merged
> catalog lives at `GET /api/models`; `/v1/models` lists virtual categories for
> OpenAI-compatible clients. Auto-resolution of bare IDs can be disabled with
> `routing.auto_resolve_bare_models: false`.

Full guide → [docs/quickstart.md](docs/quickstart.md)

## Bring Your Own Keys

Conductor is built for individuals with several provider subscriptions who
don't want a middleman service. Every key stays on your machine and goes only
to its own provider — no per-token markup, no upstream seeing your traffic mix.

Every provider below auto-enables from its env var; list as many as you have:

```bash
docker run -d -p 8080:8080 \
  -e CONDUCTOR_API_KEY=my-key \
  -e OPENAI_API_KEY=sk-... \
  -e ANTHROPIC_API_KEY=sk-ant-... \
  -e GEMINI_API_KEY=... \
  -e DEEPSEEK_API_KEY=... \
  -e GROQ_API_KEY=gsk_... \
  -v conductor-data:/app/data \
  effnine/conductor:latest
```

All of them appear in `GET /api/models`, share one gateway key, feed one usage/cost
ledger, and back each other up through automatic failover.

## Features

| Feature | Description |
|---------|-------------|
| **Single Gateway Key** | One auth key for all clients (Continue, Aider, Open WebUI, Claude Code, custom apps) |
| **Merged Model Picker** | `GET /api/models` aggregates every provider; IDs are provider-prefixed (`openai/gpt-4o`, `nvidia_nim/meta/llama-3.1-8b-instruct`). `/v1/models` stays OpenAI-compatible with virtual categories (`auto`, `fast`, …) |
| **Model Reachability** | Background probes hide unreachable models; status exposed at `/api/models/status` |
| **Explicit Routing + Aliases** | Map bare IDs to providers and upstream slugs. Provider-prefixed IDs route without config |
| **Automatic Failover** | Primary fails → configured static chains → **dynamic alternates from the full catalog**, filtered to the request's category (vision→vision, planning→reasoning+tools, long-context→fits). A request only errors when no eligible model can serve it. Disable with `routing.dynamic_fallback.enabled: false` |
| **Auto Model Selection** | Send `"model": "auto"` — gateway picks the best model by health, cost, latency, and capability match |
| **Task Orchestration (V2.5)** | `POST /api/tasks` creates persistent tasks with intent classification, plan generation, agent loops, and verification |
| **Multi-Step Agents** | Bounded loops with tool calls (fs, shell, git), checkpoint/resume, retry/backoff |
| **Usage & Cost Tracking** | Per-request tokens, latency, and USD cost aggregated in SQLite |
| **Dashboard API** | Models, status, usage, costs, provider health, and logs — all behind one key |

## Supported Providers

| Provider | Chat | Embeddings | Streaming | Pricing |
|----------|:----:|:----------:|:---------:|:-------:|
| OpenAI | ✅ | ✅ | ✅ | ✅ |
| Anthropic | ✅ | ❌ | ✅ | ✅ |
| Gemini | ✅ | ✅ | ✅ | ✅ |
| DeepSeek | ✅ | ✅ | ✅ | ✅ |
| OpenRouter | ✅ | ✅ | ✅ | ✅ |
| Groq | ✅ | ✅ | ✅ | ✅ |
| OpenCode | ✅ | ✅ | ✅ | ✅ |
| NVIDIA NIM | ✅ | ✅ | ✅ | ✅ |
| Nous Portal | ✅ | ✅ | ✅ | — |
| Ollama | ✅ | ✅ | ✅ | — |
| LM Studio | ✅ | ✅ | ✅ | — |
| KiloCode | ✅ | ✅ | ✅ | ✅ |
| Mistral | ✅ | ✅ | ✅ | ✅ |
| Z.AI | ✅ | ✅ | ✅ | ✅ |
| Cerebras | ✅ | ✅ | ✅ | ✅ |
| Requesty | ✅ | ✅ | ✅ | ✅ |
| Cloudflare | ✅ | ❌ | ❌ | — |

## Architecture

```
Client → API Key Check → Validate → Route → Provider Adapter → Normalize → Response
                             ↓              ↓                              ↓
                        Alias/Routes   Health/Probes               Usage + Cost → SQLite

Task API Flow (V2.5):
  POST /api/tasks
    ↓
  INTENT → CAPABILITY → CANDIDATE → SELECTION → PLAN
    ↓
  AGENT LOOP (steps with tool calls)
    ↓
  VERIFY → COMPLETE
```

## Dashboard API

All endpoints authenticated with the gateway key (`GET /health` is public):

```bash
curl http://localhost:8080/api/models            # merged catalog
curl http://localhost:8080/api/models/status     # per-model reachability
curl http://localhost:8080/api/usage             # usage totals
curl http://localhost:8080/api/usage/costs       # cost breakdown
curl http://localhost:8080/api/health            # provider health
curl http://localhost:8080/api/metrics           # Prometheus metrics
```

See [docs/api.md](docs/api.md) for the full reference.

## Deploy

### Fly.io

```bash
export CONDUCTOR_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-...
./scripts/fly-deploy.sh
```

See [docs/deployment.md](docs/deployment.md) for Docker, Render, Railway, and other options.

## Security

- **All endpoints require the gateway key** (`Authorization: Bearer <key>`) except `GET /health`, which stays open for load balancer / Fly.io liveness checks. Scheme is case-insensitive; bare keys or other schemes (`Basic`, …) are rejected. The key is never logged, never echoed in responses, and is always redacted (`[REDACTED]`) in `GET /api/config`.
- **CORS defaults to `*`** — restrict origins in config for production
- **No built-in rate limiting** — place behind a reverse proxy (Caddy, Nginx) or CDN
- **SQLite database** stored at `./data/conductor.db` — restrict directory permissions
- See [Security Best Practices](docs/deployment.md#security-best-practices) for full guidance

## Development

```bash
git clone https://github.com/EffNine/conductor.git
cd conductor
make build
make test

export CONDUCTOR_API_KEY=test-key
export OPENAI_API_KEY=sk-test
make run
```

Requires Go 1.21+, `gcc`, and CGO enabled.

## Documentation

- [Quick Start Guide](docs/quickstart.md)
- [Configuration Reference](docs/configuration.md)
- [Provider Setup](docs/providers.md)
- [API Reference](docs/api.md)
- [Architecture](docs/architecture.md)
- [Deployment Guide](docs/deployment.md)
- [Contributing](docs/contributing.md)

## License

MIT — see [LICENSE](LICENSE).
