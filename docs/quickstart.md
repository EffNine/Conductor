# Quick Start Guide

Get Conductor running in 5 minutes.

## Prerequisites

- Docker installed
- At least one AI provider API key (any provider from the table in the [README](../README.md#supported-providers))

## Option 1: Docker (Recommended)

### 1. Create a `.env` file

```bash
cat > .env << EOF
CONDUCTOR_API_KEY=your-secret-gateway-key
OPENAI_API_KEY=sk-your-openai-key
EOF
```

### 2. Run the gateway

```bash
docker run -d \
  --name conductor \
  -p 8080:8080 \
  --env-file .env \
  -v conductor-data:/app/data \
  effnine/conductor:latest
```

### 3. Test it

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-secret-gateway-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "openai/gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'

> **Which model IDs work without config?** Provider-prefixed IDs
> (`openai/gpt-4o`) and virtual categories (`auto`, `fast`, `coding`, …) work
> immediately. Bare IDs like `gpt-4o` need a `routes:` entry — see
> [Configuration](configuration.md).
```

## Option 2: Docker Compose

```yaml
services:
  gateway:
    image: effnine/conductor:latest
    ports:
      - "8080:8080"
    environment:
      - CONDUCTOR_API_KEY=your-secret-gateway-key
      - OPENAI_API_KEY=sk-your-openai-key
    volumes:
      - conductor-data:/app/data
    restart: unless-stopped

volumes:
  conductor-data:
```

```bash
docker compose up -d
```

## Option 3: Build from Source

```bash
# Clone
git clone https://github.com/EffNine/conductor.git
cd conductor

# Build
make build

# Run
export CONDUCTOR_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key
./bin/conductor
```

## Configuration

### Minimal Configuration

A provider key is enough to boot locally. The gateway API key is optional:

```bash
# Optional — auto-generated on first boot if unset (saved to data/conductor.api_key)
# export CONDUCTOR_API_KEY=your-secret-gateway-key
# Or: make gen-key
export OPENAI_API_KEY=sk-your-openai-key
```

In production, set `CONDUCTOR_API_KEY` explicitly (env or platform secret). Do not rely on `data/conductor.api_key` on ephemeral disks.

### Custom Configuration

Create a `config.yaml`:

```yaml
api_key: "${CONDUCTOR_API_KEY}"

providers:
  openai:
    api_key: "${OPENAI_API_KEY}"

routes:
  "gpt-4o":
    provider: openai
  "fast":
    provider: openai
    model_id: "gpt-4o-mini"

aliases:
  "smart": "gpt-4o"
```

Run with config file mounted:

```bash
docker run -d \
  -p 8080:8080 \
  --env-file .env \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v conductor-data:/app/data \
  effnine/conductor:latest
```

## Using with Clients

### VS Code (Continue)

```json
{
  "apiBase": "http://localhost:8080/v1",
  "apiKey": "your-secret-gateway-key",
  "model": "gpt-4o"
}
```

### Claude Code

Claude Code speaks the Anthropic wire format, while Conductor exposes the OpenAI-compatible format. Use Claude Code with a provider that accepts Anthropic format directly, or use any OpenAI-compatible client with Conductor.

### Open WebUI

- API Base URL: `http://localhost:8080/v1`
- API Key: `your-secret-gateway-key`

### cURL

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-secret-gateway-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

## Checking Status

### Health Check

```bash
curl http://localhost:8080/health
```

### Provider Health

```bash
curl http://localhost:8080/api/health \
  -H "Authorization: Bearer your-secret-gateway-key"
```

### Merged Model Catalog

```bash
curl http://localhost:8080/api/models \
  -H "Authorization: Bearer your-secret-gateway-key"
```

### Model Online Status

When providers are enabled, the gateway probes models and can hide unreachable ones from the merged catalog (`/api/models`). Conductor runs a full probe pass on every startup/redeploy, then every 2 hours by default (all registered providers):

```bash
# Probe cache
curl http://localhost:8080/api/models/status \
  -H "Authorization: Bearer your-secret-gateway-key"

# Include models hidden from /v1/models
curl "http://localhost:8080/api/models?include_unreachable=true" \
  -H "Authorization: Bearer your-secret-gateway-key"
```

See [Configuration — Model reachability](configuration.md#model-reachability) and [Providers — Model Reachability](providers.md#model-reachability-nvidia-nim).

### Usage Statistics

```bash
curl http://localhost:8080/api/usage \
  -H "Authorization: Bearer your-secret-gateway-key"
```

### Recent Logs

```bash
curl http://localhost:8080/api/logs \
  -H "Authorization: Bearer your-secret-gateway-key"
```

## Next Steps

- [Configuration Reference](configuration.md)
- [Provider Setup](providers.md)
- [Deployment Guide](deployment.md)
- [API Reference](api.md)

## Troubleshooting

### Gateway won't start

```bash
docker logs conductor
```

Common issues:
- Unable to write `data/` (API key auto-generation and SQLite both need it — run `mkdir -p data` from the repo root)
- Port 8080 already in use (change with `CONDUCTOR_SERVER_PORT`)
- Invalid provider API key

### Provider returns errors

Check provider health:

```bash
curl http://localhost:8080/api/health \
  -H "Authorization: Bearer your-secret-gateway-key"
```

Most providers have full adapters (see the support matrix in the README). Cloudflare is partial; check `/api/health` to see whether your provider is responding.

### Model not in the catalog

- The merged catalog is at `GET /api/models`; `/v1/models` lists virtual categories (`auto`, `fast`, …) for OpenAI-compatible clients
- Configure a `routes` entry for bare Model IDs, or use provider-prefixed IDs (`openai/gpt-4o`) which need no config
- For providers without a catalog endpoint, configure a static `models` list
- For NVIDIA NIM: the model may have failed online-status probes — check `/api/models/status` or `/api/models?include_unreachable=true`
- Recurring probes run every 2 hours by default (plus a full pass on each startup/redeploy); live request failures can still update model status immediately

### Streaming not working

Test with curl:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-secret-gateway-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }' \
  --no-buffer
```
