# Configuration Reference

Conductor uses environment variables first, then YAML, then defaults.

## Priority

1. Environment variables (`CONDUCTOR_*` or provider-specific keys)
2. YAML config file
3. Default values

## Environment Variables

### Core Settings

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `CONDUCTOR_API_KEY` | Gateway API key for client authentication | — | **Yes** |
| `CONDUCTOR_SERVER_PORT` | HTTP server port | `8080` | No |
| `CONDUCTOR_SERVER_HOST` | HTTP server host | `0.0.0.0` | No |
| `CONDUCTOR_SERVER_READ_TIMEOUT` | Request read timeout | `30s` | No |
| `CONDUCTOR_SERVER_WRITE_TIMEOUT` | Response write timeout | `120s` | No |
| `CONDUCTOR_SERVER_MAX_REQUEST_SIZE` | Maximum request body size | `10MB` | No |

### Provider API Keys

| Variable | Description |
|----------|-------------|
| `OPENAI_API_KEY` | OpenAI API key |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `GEMINI_API_KEY` | Google Gemini API key |
| `DEEPSEEK_API_KEY` | DeepSeek API key |
| `OPENROUTER_API_KEY` | OpenRouter API key |
| `GROQ_API_KEY` | Groq API key |
| `OPENCODE_API_KEY` | OpenCode Zen API key |
| `NVIDIA_NIM_API_KEY` | NVIDIA NIM API key |
| `NOUS_PORTAL_API_KEY` | Nous Portal API key |
| `XAI_API_KEY` | xAI API key (auto-enables) |
| `AGNES_API_KEY` | Agnes AI API key (auto-enables) |
| `OLLAMA_API_KEY` | Ollama Cloud API key (auto-enables; default base `https://ollama.com/v1`) |
| `OLLAMA_BASE_URL` | Override Ollama OpenAI-compatible base URL when the provider is enabled (e.g. Docker host) |

### Database Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `CONDUCTOR_DATABASE_DRIVER` | Database driver (`sqlite` or `postgres`) | `sqlite` |
| `CONDUCTOR_DATABASE_DSN` | Database connection string | `./data/conductor.db` |

### Logging Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `CONDUCTOR_LOGGING_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `CONDUCTOR_LOGGING_FORMAT` | Log format (`json` or `console`) | `json` |
| `CONDUCTOR_LOGGING_LOG_PROMPTS` | Log request prompts (security risk) | `false` |
| `CONDUCTOR_LOGGING_LOG_RESPONSES` | Log response bodies | `false` |

### Rate Limiting

| Variable | Description | Default |
|----------|-------------|---------|
| `CONDUCTOR_RATE_LIMIT_GLOBAL_REQUESTS_PER_MINUTE` | Global requests per minute | `1000` |
| `CONDUCTOR_RATE_LIMIT_PER_PROVIDER_REQUESTS_PER_MINUTE` | Per-provider requests per minute | `100` |

### Catalog

| Variable | Description | Default |
|----------|-------------|---------|
| `CONDUCTOR_CATALOG_CURATED_ONLY` | Advertise only `providers.*.models` (skip dynamic ListModels) | `false` |

### Health Monitoring

| Variable | Description | Default |
|----------|-------------|---------|
| `CONDUCTOR_HEALTH_CHECK_INTERVAL` | Provider health check interval | `60s` |
| `CONDUCTOR_HEALTH_TIMEOUT` | Provider health check timeout | `10s` |
| `CONDUCTOR_HEALTH_UNHEALTHY_THRESHOLD` | Consecutive provider failures before unhealthy | `3` |
| `CONDUCTOR_HEALTH_MODELS_ENABLED` | Enable per-model reachability probes | `true` |
| `CONDUCTOR_HEALTH_MODELS_HIDE_UNREACHABLE` | Omit unreachable models from `/v1/models` | `true` |
| `CONDUCTOR_HEALTH_MODELS_CHECK_INTERVAL` | Interval between model probe passes | `12h` |
| `CONDUCTOR_HEALTH_MODELS_TIMEOUT` | Per-model probe timeout | `60s` |
| `CONDUCTOR_HEALTH_MODELS_CONCURRENCY` | Max parallel model probes | `3` |
| `CONDUCTOR_HEALTH_MODELS_UNHEALTHY_THRESHOLD` | Consecutive model failures before hide | `1` |
| `CONDUCTOR_HEALTH_MODELS_UNKNOWN_AS_REACHABLE` | Keep unprobed models visible after first pass | `false` |

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
api_key: "${CONDUCTOR_API_KEY}"

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
    api_key: "${OLLAMA_API_KEY}"
    base_url: "http://localhost:11434/v1"
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

# Catalog: curated_only advertises only providers.*.models (skip dynamic catalogs)
catalog:
  curated_only: false

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
  dsn: "./data/conductor.db"
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
  # Per-model probes: full pass on startup/redeploy, then every check_interval.
  # Empty providers list = all registered providers.
  models:
    enabled: true
    hide_unreachable: true
    check_interval: 12h
    timeout: 60s
    concurrency: 3
    unhealthy_threshold: 1
    providers: []
    unknown_as_reachable: false

# Usage tracking
usage:
  enabled: true
```

### Curated catalog

By default `/v1/models` merges each provider's dynamic `ListModels` result (falling back to `providers.*.models` when listing fails). To advertise only an allowlist — especially useful for NVIDIA NIM's large mixed catalog — set:

```yaml
catalog:
  curated_only: true

providers:
  nvidia_nim:
    enabled: true
    api_key: "${NVIDIA_NIM_API_KEY}"
    models:
      - "deepseek-ai/deepseek-v4-flash"
      - "meta/llama-3.1-8b-instruct"
  openai:
    enabled: true
    api_key: "${OPENAI_API_KEY}"
    models:
      - "gpt-4o"
      - "gpt-4o-mini"
```

| Field | Description | Default |
|-------|-------------|---------|
| `curated_only` | Advertise only `providers.*.models`; ignore dynamic ListModels for catalog and probes | `false` |

Providers with an empty `models` list contribute nothing while curated-only is on. Prefixed IDs in `/v1/models` still work for chat (e.g. `nvidia_nim/meta/llama-3.1-8b-instruct`). Curated-only does not block chat for Model IDs outside the list if the client already knows them and routing succeeds.

### Model reachability

NVIDIA NIM's `GET /v1/models` lists the full catalog, including retired and non-callable endpoints. There is no catalog flag for "free and online". Conductor optionally probes each configured provider's models with a minimal `POST /chat/completions` (`max_tokens: 1`) and:

- Runs a full probe pass on every startup/redeploy, then again every `check_interval`
- Caches online/offline status (also updated from live chat failures)
- Keeps confirmed failures hidden during the first pass (list shrinks; never flashes empty), then lists only models that passed (`unknown_as_reachable: false`)
- Persists probe results to SQLite so Fly.io cold starts keep the available-only list instead of flashing the full catalog
- Skips loopback `ollama` / `lmstudio` base URLs during probes so remote deploys finish the pass
- Exposes status on `GET /api/models` and `GET /api/models/status`
- Use `GET /api/models?include_unreachable=true` to list hidden models with their status
- Legacy `NOVEXA_*` environment variables are accepted as aliases for `CONDUCTOR_*`

| Field | Description | Default |
|-------|-------------|---------|
| `enabled` | Run background per-model probes | `true` |
| `hide_unreachable` | Omit unreachable / unpassed models from `/v1/models` and default `/api/models` | `true` |
| `check_interval` | Time between full probe passes (after the startup pass) | `12h` |
| `timeout` | Timeout per individual model probe | `60s` |
| `concurrency` | Max parallel probes (keep low for NIM free-tier RPM) | `3` |
| `unhealthy_threshold` | Consecutive definitive failures before a model is hidden | `1` |
| `providers` | Provider names to probe; empty = all registered | `[]` (all) |
| `unknown_as_reachable` | After first pass, only list probe-passed models | `false` |

**Classification rules:**

| Outcome | Effect |
|---------|--------|
| HTTP 200 on probe / live chat | Mark reachable; reset failure count |
| Definitive probe failure (404/410, model-not-found, other non-transient errors) | Count toward `unhealthy_threshold` (default 1 → hide from `/v1/models`) |
| 429 rate limit, 401/403 auth | Neutral — do not change reachability |
| Timeout, 502/503/504 | Inconclusive — do not hide (may be transient) |

Disable with:

```yaml
health:
  models:
    enabled: false
```

Probe a subset only (use carefully — probes consume quota):

```yaml
health:
  models:
    providers:
      - nvidia_nim
      - openrouter
```

### Auto model selection (NVIDIA NIM)

When `providers.nvidia_nim.auto.enabled: true`, clients can send `"model": "auto"` and the gateway will pick the best available NIM model at runtime. It first classifies the request text into a task type (`elite`, `coding`, `reasoning`, `vision`, `fast`, `default`), then uses a matching `task_profile` to restrict candidates and tune the scoring weights. Within the profile, models are scored by:

- **Reachability** — only models that passed the reachability probe are considered.
- **Historical cost** — average USD per token from `usage_records` over the configured `lookback`.
- **Latency** — latest probe latency from the model status cache.

Models with no usage history get a neutral cost score so they are not penalized.

```yaml
providers:
  nvidia_nim:
    enabled: true
    api_key: "${NVIDIA_NIM_API_KEY}"
    models:
      - "deepseek-ai/deepseek-v4-flash"
      - "meta/llama-3.1-8b-instruct"
    auto:
      enabled: true
      provider: "nvidia_nim"
      lookback: 24h
      weights:
        reachability: 10.0
        cost: 3.0
        latency: 2.0
      task_profiles:
        elite:
          models:
            - "mistralai/mistral-large-3-675b-instruct-2512"
            - "nvidia/nemotron-3-super-120b-a12b"
          weights:
            reachability: 10.0
            cost: 1.0
            latency: 2.0
```

| Field | Description | Default |
|-------|-------------|---------|
| `enabled` | Enable runtime auto selection for this provider | `false` |
| `provider` | Provider scope for auto selection | `nvidia_nim` |
| `lookback` | How far back to read usage history for cost scoring | `24h` |
| `weights.reachability` | Weight for reachability signal | `10.0` |
| `weights.cost` | Weight for historical cost signal | `3.0` |
| `weights.latency` | Weight for probe latency signal | `2.0` |
| `task_profiles` | Task-type-specific model allowlists and weight overrides | Built-in NIM tiers |

If `task_profiles` is omitted, the gateway uses built-in profiles derived from the NVIDIA NIM model comparison (`elite`, `coding`, `reasoning`, `vision`, `fast`, `default`). If a profile restricts candidates to a list, only those models are considered; otherwise the full advertised catalog is used.

Auto mode respects `catalog.curated_only`: only models advertised in `/v1/models` are candidates. Status is exposed on `GET /api/auto/status`. You can also configure an `aliases` entry such as `"nim-auto": "auto"` to expose a friendlier model name.

## Minimal Configuration

For the simplest setup, only the gateway key and provider keys are required:

```bash
export CONDUCTOR_API_KEY=your-secret-gateway-key
export OPENAI_API_KEY=sk-your-openai-key
```

The gateway will start on port 8080, enable OpenAI, and use SQLite with sensible defaults.

## Configuration Validation

Startup fails if:

- `CONDUCTOR_API_KEY` is not set
- Server port is invalid
- Logging level/format is invalid
- Database driver is unsupported

## Configuration Reload

`/api/config/reload` is currently a stub and returns a success message without reloading configuration. A restart is required for provider API key and structural config changes.
