# Deployment Guide

Novexa Gateway can be deployed locally, on free cloud platforms, or on any infrastructure that supports Docker.

## Local Deployment

### Docker (Recommended)

```bash
docker run -d \
  --name novexa-gateway \
  -p 8080:8080 \
  -e NOVEXA_API_KEY=your-secret-key \
  -e OPENAI_API_KEY=sk-your-openai-key \
  -v novexa-data:/app/data \
  novexa/gateway:latest
```

### Docker Compose

```yaml
version: '3.8'

services:
  gateway:
    image: novexa/gateway:latest
    ports:
      - "8080:8080"
    environment:
      - NOVEXA_API_KEY=your-secret-key
      - OPENAI_API_KEY=sk-your-openai-key
    volumes:
      - novexa-data:/app/data
    restart: unless-stopped

volumes:
  novexa-data:
```

### Build from Source

```bash
git clone https://github.com/novexa/gateway.git
cd gateway
make build
./bin/gateway
```

---

## Free Cloud Deployment

### Railway

Railway offers a generous free tier with Docker support.

#### 1. Create `railway.toml`

```toml
[build]
builder = "DOCKERFILE"
dockerfilePath = "deployments/Dockerfile"

[deploy]
startCommand = "./gateway"
healthcheckPath = "/health"
healthcheckTimeout = 100
restartPolicyType = "ON_FAILURE"
restartPolicyMaxRetries = 10
```

#### 2. Deploy

```bash
# Install Railway CLI
npm install -g @railway/cli

# Login
railway login

# Create project
railway init

# Set environment variables
railway variables set NOVEXA_API_KEY=your-secret-key
railway variables set OPENAI_API_KEY=sk-your-openai-key

# Deploy
railway up
```

#### 3. Get URL

```bash
railway domain
```

Your gateway will be available at `https://your-app.railway.app`

#### Notes

- Railway offers $5 free credit per month
- Persistent volumes available for SQLite data
- Automatic HTTPS
- Custom domains supported

---

### Fly.io (recommended free deploy)

Fly.io offers a free shared VM tier with persistent volumes and HTTPS.

Canonical config: [`fly.toml`](../fly.toml) at the repo root (build context must be the repo root so `deployments/Dockerfile` can copy Go sources).

#### One-shot deploy

```bash
# Install Fly CLI (once)
curl -L https://fly.io/install.sh | sh
fly auth login

# Deploy (creates app + SQLite volume + secrets + release)
export NOVEXA_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key
./scripts/fly-deploy.sh
# or: make fly-deploy
```

If `novexa-gateway` is taken: `APP_NAME=novexa-gateway-you ./scripts/fly-deploy.sh`

#### Manual steps

```bash
fly apps create novexa-gateway
fly volumes create novexa_data --size 1 --region iad
fly secrets set NOVEXA_API_KEY=your-secret-key OPENAI_API_KEY=sk-your-openai-key
fly deploy
fly status
```

Your gateway will be at `https://<app-name>.fly.dev` (use `/v1` as the OpenAI base URL).

#### Notes

- Free shared-cpu-1x / 256MB VM; 1GB volume for SQLite
- Automatic HTTPS; machines auto-stop when idle and wake on request
- Health check: `GET /health`
- Set any implemented provider key: `OPENAI_API_KEY`, `OPENCODE_API_KEY`, `NVIDIA_NIM_API_KEY`, `NOUS_PORTAL_API_KEY`, or `OLLAMA_API_KEY` (Ollama Cloud)

---

### Render

Render offers free web services with automatic deploys.

#### 1. Create `render.yaml`

```yaml
services:
  - type: web
    name: novexa-gateway
    runtime: docker
    dockerfilePath: ./deployments/Dockerfile
    envVars:
      - key: NOVEXA_API_KEY
        sync: false
      - key: OPENAI_API_KEY
        sync: false
      - key: DATABASE_DSN
        value: ./data/novexa.db
    disk:
      name: novexa-data
      mountPath: /app/data
      sizeGB: 1
```

#### 2. Deploy

1. Push your code to GitHub
2. Go to [render.com](https://render.com)
3. Click "New +" → "Web Service"
4. Connect your GitHub repository
5. Render will auto-detect the `render.yaml`
6. Add environment variables in the dashboard
7. Click "Create Web Service"

#### 3. Get URL

Your gateway will be available at `https://novexa-gateway.onrender.com`

#### Notes

- Render offers 750 hours free per month
- Persistent disk for SQLite data
- Automatic HTTPS
- Auto-deploys from GitHub
- Service spins down after 15 minutes of inactivity (cold start on next request)

---

## Production Deployment

### With Caddy Reverse Proxy

For production deployments, use Caddy as a reverse proxy for automatic HTTPS.

#### 1. Create `Caddyfile`

```
your-domain.com {
    reverse_proxy localhost:8080
    
    # Security headers
    header {
        Strict-Transport-Security "max-age=31536000;"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
    }
    
    # Logging
    log {
        output file /var/log/caddy/access.log
    }
}
```

#### 2. Docker Compose with Caddy

```yaml
version: '3.8'

services:
  caddy:
    image: caddy:2-alpine
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddy-data:/data
      - caddy-config:/config
    depends_on:
      - gateway

  gateway:
    image: novexa/gateway:latest
    environment:
      - NOVEXA_API_KEY=your-secret-key
      - OPENAI_API_KEY=sk-your-openai-key
    volumes:
      - novexa-data:/app/data
    restart: unless-stopped

volumes:
  caddy-data:
  caddy-config:
  novexa-data:
```

### Kubernetes

For Kubernetes deployments, see the `deployments/k8s/` directory (coming soon).

### Systemd Service

For bare metal deployments, create a systemd service:

```ini
[Unit]
Description=Novexa Gateway
After=network.target

[Service]
Type=simple
User=novexa
WorkingDirectory=/opt/novexa-gateway
ExecStart=/opt/novexa-gateway/bin/gateway
Restart=always
RestartSec=10
Environment=NOVEXA_API_KEY=your-secret-key
Environment=OPENAI_API_KEY=sk-your-openai-key

[Install]
WantedBy=multi-user.target
```

---

## Environment Variables for Cloud Deployment

### Required

```bash
NOVEXA_API_KEY=your-secret-gateway-key
OPENAI_API_KEY=sk-your-openai-key
```

### Optional

```bash
# Server
SERVER_PORT=8080
SERVER_HOST=0.0.0.0

# Additional providers
ANTHROPIC_API_KEY=sk-ant-your-key
GEMINI_API_KEY=your-gemini-key
DEEPSEEK_API_KEY=your-deepseek-key

# Database
DATABASE_DRIVER=sqlite
DATABASE_DSN=./data/novexa.db

# Logging
LOGGING_LEVEL=info
LOGGING_FORMAT=json
```

---

## Persistent Storage

### SQLite Data Location

By default, SQLite database is stored at `./data/novexa.db`

### Docker Volume

```bash
docker run -v novexa-data:/app/data novexa/gateway:latest
```

### Cloud Platform Volumes

- **Railway**: Create a volume and mount to `/app/data`
- **Fly.io**: `fly volumes create novexa_data --size 1`
- **Render**: Add a disk in `render.yaml`

### Backup

```bash
# Backup SQLite database
docker exec novexa-gateway sqlite3 /app/data/novexa.db ".backup '/app/data/backup.db'"
docker cp novexa-gateway:/app/data/backup.db ./backup.db
```

---

## Monitoring

### Health Checks

All cloud platforms support health checks. Configure:

- **Path**: `/health`
- **Timeout**: 10 seconds
- **Interval**: 30 seconds

### Logs

```bash
# Docker
docker logs novexa-gateway

# Railway
railway logs

# Fly.io
fly logs

# Render
View in dashboard
```

### Metrics

Access usage metrics via API:

```bash
curl https://your-gateway.com/api/usage \
  -H "Authorization: Bearer your-key"
```

---

## Security Best Practices

### API Keys

- Use strong, random API keys
- Never commit keys to version control
- Use environment variables or secret managers
- Rotate keys periodically

### Network

- Use HTTPS in production (Caddy handles this automatically)
- Restrict CORS origins if needed
- Use firewall rules to limit access

### Rate Limiting

Configure rate limits to prevent abuse:

```yaml
rate_limit:
  enabled: true
  global:
    requests_per_minute: 1000
  per_provider:
    requests_per_minute: 100
```

### Updates

Keep the gateway updated:

```bash
docker pull novexa/gateway:latest
docker-compose up -d
```

---

## Troubleshooting

### Cold Starts (Render)

Render free tier services spin down after inactivity. First request after idle takes 30-60 seconds.

**Solution**: Use a cron job to ping the health endpoint every 5 minutes.

### Volume Permissions

If SQLite can't write to the volume:

```bash
docker run -u $(id -u):$(id -g) -v novexa-data:/app/data novexa/gateway:latest
```

### Out of Memory

If the gateway crashes with OOM:

- Increase memory limit in cloud platform settings
- Reduce `max_request_size` in config
- Disable unused providers

### Database Locked

SQLite can only handle one writer at a time. If you see "database is locked":

- Enable WAL mode (enabled by default)
- Reduce concurrent write operations
- Consider PostgreSQL for high concurrency
