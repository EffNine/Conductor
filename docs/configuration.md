# Configuration Reference

Novexa Gateway uses environment variables first, then YAML, then defaults.

## Priority

1. Environment variables (`NOVEXA_*` or provider-specific keys)
2. YAML config file
3. Default values

## Environment Variables

### Core Settings

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `NOVEXA_API_KEY` | Gateway API key for client authentication | — | **Yes** |
| `NOVEXA_SERVER_PORT` | HTTP server port | `8080` | No |
| `NOVEXA_SERVER_HOST` | HTTP server host | `0.0.0.0` | No |
| `NOVEXA_SERVER_READ_TIMEOUT` | Request read timeout | `30s` | No |
| `NOVEXA_SERVER_WRITE_TIMEOUT` | Response write timeout | `120s` | No |
| `NOVEXA_SERVER_MAX_REQUEST_SIZE` | Maximum request body size | `10MB` | No |

### Provider API Keys

| Variable | Description |
|----------|-------------|
| `OPENAI_API_KEY` | OpenAI API key |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `GEMINI_API_KEY` | Google Gemini API key |
| `DEEPSEEK_API_KEY` | DeepSeek API key |
| `OPENROUTER_API_KEY` | OpenRouter API key |
| `GROQ_API_KEY` | Groq API key |

### Database Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `NOVEXA_DATABASE_DRIVER` | Database driver (`sqlite` or `postgres`) | `sqlite` |
| `NOVEXA_DATABASE_DSN` | Database connection string | `./data/novexa.db` |

### Logging Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `NOVEXA_LOGGING_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `NOVEXA_LOGGING_FORMAT` | Log format (`json` or `console`) | `json` |
| `NOVEXA_LOGGING_LOG_PROMPTS` | Log request prompts (security risk) | `false` |
| `NOVEXA_LOGGING_LOG_RESPONSES` | Log response bodies | `false` |

### Rate Limiting

| Variable | Description | Default |
|----------|-------------|---------|
| `NOVEXA_RATE_LIMIT_GLOBAL_REQUESTS_PER_MINUTE` | Global requests per minute | `1000` |
| `NOVEXA_RATE_LIMIT_PER_PROVIDER_REQUESTS_PER_MINUTE` | Per-provider requests per minute | `100` |

### Health Monitoring

| Variable | Description | Default |
|----------|-------------|---------|
| `NOVEXA_HEALTH_CHECK_INTERVAL` | Provider health check interval | `60s` |
| `NOVEXA_HEALTH_TIMEOUT` | Provider health check timeout | `10s` |
| `NOVEXA_HEALTH_UNHEALTHY_THRESHOLD` | Consecutive provider failures before unhealthy | `3` |
| `NOVEXA_HEALTH_MODELS_ENABLED` | Enable per-model reachability probes | `true` |
| `NOVEXA_HEALTH_MODELS_HIDE_UNREACHABLE` | Omit unreachable models from `/v1/models` | `true` |
| `NOVEXA_HEALTH_MODELS_CHECK_INTERVAL` | Interval between model probe passes | `24h` |
| `NOVEXA_HEALTH_MODELS_TIMEOUT` | Per-model probe timeout | `15s` |
| `NOVEXA_HEALTH_MODELS_CONCURRENCY` | Max parallel model probes | `3` |
| `NOVEXA_HEALTH_MODELS_UNHEALTHY_THRESHOLD` | Consecutive model failures before hide | `2` |
| `NOVEXA_HEALTH_MODELS_UNKNOWN_AS_REACHABLE` | Keep unprobed models visible | `true` |

## YAML Configuration File

```yaml
# Server configuration
server:
  host: "0.0.0.0"
  port: 8080
  read_timeout: 30s
  write_timeout: 120s
  max_request_size: 10MB
  cors:
    enabled: true
    origins: ["*"]
    methods: ["GET", "POST", "OPTIONS"]
    headers: ["Authorization", "Content-Type"]

# Gateway API key
api_key: "${NOVEXA_API_KEY}"

# Provider configuration
providers:
  openai:
    enabled: true
    api_key: "${OPENAI_API_KEY}"
    base_url: "https://api.openai.com/v1"
    timeout: 60s
    max_retries: 3

  anthropic:
    enabled: false
    api_key: "${ANTHROPIC_API_KEY}"
    base_url: "https://api.anthropic.com"
    timeout: 60s
    max_retries: 3

  gemini:
    enabled: false
    api_key: "${GEMINI_API_KEY}"
    base_url: "https://generativelanguage.googleapis.com/v1beta"
    timeout: 60s
    max_retries: 3

  deepseek:
    enabled: false
    api_key: "${DEEPSEEK_API_KEY}"
    base_url: "https://api.deepseek.com/v1"
    timeout: 60s
    max_retries: 3

  openrouter:
    enabled: false
    api_key: "${OPENROUTER_API_KEY}"
    base_url: "https://openrouter.ai/api/v1"
    timeout: 60s
    max_retries: 3

  groq:
    enabled: false
    api_key: "${GROQ_API_KEY}"
    base_url: "https://api.groq.com/openai/v1"
    timeout: 30s
    max_retries: 3

  ollama:
    enabled: false
    base_url: "http://localhost:11434"
    timeout: 120s
    max_retries: 1
    models:
      - "llama3.1:8b"

  lmstudio:
    enabled: false
    base_url: "http://localhost:1234/v1"
    timeout: 120s
    max_retries: 1
    models:
      - "loaded-model-name"

# Model routing
routes:
  "gpt-4o":
    provider: openai
  "gpt-4o-mini":
    provider: openai
  "claude-sonnet":
    provider: anthropic
    model_id: "claude-3-5-sonnet-20241022"

# Aliases (not advertised in /v1/models)
aliases:
  "fast": "gpt-4o-mini"
  "smart": "gpt-4o"
  "coding": "deepseek-chat"

# Fallback chains
fallbacks:
  "deepseek-chat":
    - provider: deepseek
    - provider: openrouter
      model_id: "deepseek/deepseek-chat"

# Cost estimation
cost:
  enabled: true
  currency: "USD"
  rates:
    - provider: openai
      model_id: "gpt-4o"
      unit: token
      unit_size: 1000
      input_usd: 0.0025
      output_usd: 0.010

# Retry configuration
retry:
  max_retries: 3
  initial_backoff: 100ms
  max_backoff: 5s
  backoff_multiplier: 2.0
  retryable_status_codes: [429, 500, 502, 503]

# Database configuration
database:
  driver: "sqlite"
  dsn: "./data/novexa.db"
  max_open_conns: 10
  max_idle_conns: 5

# Logging configuration
logging:
  level: "info"
  format: "json"
  log_prompts: false
  log_responses: false

# Rate limiting
rate_limit:
  enabled: true
  global:
    requests_per_minute: 1000
  per_provider:
    requests_per_minute: 100

# Health monitoring
health:
  check_interval: 60s
  timeout: 10s
  unhealthy_threshold: 3
  # Per-model probes hide endpoints that appear in /models but fail inference
  # (common on NVIDIA NIM free tier). Probes send max_tokens=1 chat requests.
  models:
    enabled: true
    hide_unreachable: true
    check_interval: 24h
    timeout: 15s
    concurrency: 3
    unhealthy_threshold: 2
    providers:
      - nvidia_nim
    unknown_as_reachable: true

# Usage tracking
usage:
  enabled: true
```

### Model reachability

NVIDIA NIM's `GET /v1/models` lists the full catalog, including retired and non-callable endpoints. There is no catalog flag for "free and online". Novexa optionally probes each configured provider's models with a minimal `POST /chat/completions` (`max_tokens: 1`) and:

- Caches online/offline status (also updated from live chat failures)
- Hides unreachable models from `GET /v1/models` when `health.models.hide_unreachable` is true
- Exposes status on `GET /api/models` and `GET /api/models/status`
- Use `GET /api/models?include_unreachable=true` to list hidden models with their status

| Field | Description | Default |
|-------|-------------|---------|
| `enabled` | Run background per-model probes | `true` |
| `hide_unreachable` | Omit unreachable models from `/v1/models` and default `/api/models` | `true` |
| `check_interval` | Time between full probe passes | `24h` |
| `timeout` | Timeout per individual model probe | `15s` |
| `concurrency` | Max parallel probes (keep low for NIM free-tier RPM) | `3` |
| `unhealthy_threshold` | Consecutive failures before a model is considered unreachable | `2` |
| `providers` | Provider names to probe; empty = all registered | `[nvidia_nim]` |
| `unknown_as_reachable` | Treat never-probed models as reachable (keeps catalog non-empty at startup) | `true` |

**Classification rules:**

| Outcome | Effect |
|---------|--------|
| HTTP 200 on probe / live chat | Mark reachable; reset failure count |
| 404, timeout, 502/503/504, model-not-found | Count toward `unhealthy_threshold` |
| 429 rate limit, 401/403 auth | Neutral — do not change reachability |

Disable with:

```yaml
health:
  models:
    enabled: false
```

Probe more than NIM (use carefully — probes consume quota):

```yaml
health:
  models:
    providers:
      - nvidia_nim
      - openrouter
```

## Minimal Configuration

For the simplest setup, only the gateway key and provider keys are required:

```bash
export NOVEXA_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key
```

The gateway will start on port 8080, enable OpenAI, and use SQLite with sensible defaults.

## Configuration Validation

Startup fails if:

- `NOVEXA_API_KEY` is not set
- Server port is invalid
- Logging level/format is invalid
- Database driver is unsupported

## Configuration Reload

`/api/config/reload` is currently a stub and returns a success message without reloading configuration. A restart is required for provider API key and structural config changes.
