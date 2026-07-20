# Quick Start Guide

Get Novexa Gateway running in 5 minutes.

## Prerequisites

- Docker installed
- At least one AI provider API key (OpenAI is the only fully implemented adapter)

## Option 1: Docker (Recommended)

### 1. Create a `.env` file

```bash
cat > .env << EOF
NOVEXA_API_KEY=your-secret-gateway-key
OPENAI_API_KEY=sk-your-openai-key
EOF
```

### 2. Run the gateway

```bash
docker run -d \
  --name novexa-gateway \
  -p 8080:8080 \
  --env-file .env \
  -v novexa-data:/app/data \
  ghcr.io/effnine/novexa-gateway:latest
```

### 3. Test it

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-secret-gateway-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

## Option 2: Docker Compose

```yaml
services:
  gateway:
    image: ghcr.io/effnine/novexa-gateway:latest
    ports:
      - "8080:8080"
    environment:
      - NOVEXA_API_KEY=your-secret-gateway-key
      - OPENAI_API_KEY=sk-your-openai-key
    volumes:
      - novexa-data:/app/data
    restart: unless-stopped

volumes:
  novexa-data:
```

```bash
docker compose up -d
```

## Option 3: Build from Source

```bash
# Clone
git clone https://github.com/EffNine/novexa-gateway.git
cd novexa-gateway

# Build
make build

# Run
export NOVEXA_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key
./bin/gateway
```

## Configuration

### Minimal Configuration

Only the gateway key and provider keys are required:

```bash
export NOVEXA_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key
```

### Custom Configuration

Create a `config.yaml`:

```yaml
api_key: "${NOVEXA_API_KEY}"

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
  -v novexa-data:/app/data \
  ghcr.io/effnine/novexa-gateway:latest
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

Claude Code uses the Anthropic API. Until the Anthropic adapter is implemented, point Claude Code at a different provider endpoint that supports OpenAI format, or use the gateway through an OpenAI-compatible client.

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

When providers are enabled, the gateway probes models and can hide unreachable ones from `/v1/models`. Novexa runs a full probe pass on every startup/redeploy, then every 12 hours by default (all registered providers):

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
docker logs novexa-gateway
```

Common issues:
- Missing `NOVEXA_API_KEY`
- Port 8080 already in use (change with `NOVEXA_SERVER_PORT`)
- Invalid provider API key

### Provider returns errors

Check provider health:

```bash
curl http://localhost:8080/api/health \
  -H "Authorization: Bearer your-secret-gateway-key"
```

Only the OpenAI adapter is fully implemented. Other providers are stubs and will return errors for chat/embeddings requests.

### Model not in `/v1/models`

- Configure a `routes` entry for the Model ID
- For stub providers, configure a static `models` list
- Aliases do not appear in `/v1/models`
- For NVIDIA NIM: the model may have failed online-status probes — check `/api/models/status` or `/api/models?include_unreachable=true`
- Recurring probes run every 12 hours by default (plus a full pass on each startup/redeploy); live request failures can still update model status immediately

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
