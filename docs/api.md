# API Reference

Conductor exposes an OpenAI-compatible API plus dashboard endpoints for monitoring.

## Authentication

All endpoints except `GET /health` require authentication via the `Authorization` header:

```
Authorization: Bearer <your-api-key>
```

The API key is set via the `CONDUCTOR_API_KEY` environment variable.

---

## OpenAI-Compatible Endpoints

### Chat Completions

**Endpoint**: `POST /v1/chat/completions`

Creates a model response for the given chat conversation.

#### Request Body

```json
{
  "model": "gpt-4o",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello!"}
  ],
  "temperature": 0.7,
  "max_tokens": 1024,
  "stream": false
}
```

**Required Fields**:
- `model` (string): Model ID, alias, or `auto` when auto model selection is enabled
- `messages` (array): Array of message objects

When `model: "auto"` is sent and `providers.nvidia_nim.auto.enabled` is `true`, the gateway selects the best available NVIDIA NIM model at runtime using reachability, historical cost, and probe latency scores. The upstream request is rewritten to use the chosen model ID.

**Optional Fields**:
- `temperature` (number): Sampling temperature (0-2). Default: 1.0
- `max_tokens` (number): Maximum tokens to generate. Default: provider default
- `stream` (boolean): Enable streaming. Default: false
- `stream_options` (object): Streaming options. The gateway sets `include_usage: true` automatically on stream requests so clients receive token usage in the final chunk.
- `top_p` (number): Nucleus sampling parameter. Default: 1.0
- `frequency_penalty` (number): Frequency penalty (-2 to 2). Default: 0
- `presence_penalty` (number): Presence penalty (-2 to 2). Default: 0
- `stop` (string|array): Stop sequences. Default: null
- `reasoning` (object): Reasoning controls for models that support thinking tokens. Forwarded to the upstream provider when present.
  - `effort` (string): `max` | `xhigh` | `high` | `medium` | `low` | `minimal` | `none`
  - `max_tokens` (number): Reasoning token budget (Anthropic-style)
  - `exclude` (boolean): Omit reasoning text from the response
  - `enabled` (boolean): Enable reasoning with provider defaults
  - `summary` (string): `auto` | `concise` | `detailed`
- `reasoning_effort` (string): OpenAI-style shorthand for `reasoning.effort` (`high`, `medium`, `low`, …)
- `include_reasoning` (boolean): Legacy OpenRouter flag to include reasoning in the response
- `chat_template_kwargs` (object): Provider-specific chat-template options (forwarded when set). For NVIDIA NIM **DeepSeek V4** models (`deepseek-ai/deepseek-v4-flash`, `deepseek-ai/deepseek-v4-pro`), the gateway injects `{ "thinking": true, "reasoning_effort": "high" }` when omitted so OpenCode and other clients that strip unknown fields still get a streamed reply instead of an empty `content` / hang. Set `reasoning_effort: "none"` (or `chat_template_kwargs.thinking: false`) to disable thinking. OpenAI `developer` roles are remapped to `system` for NIM.

When the upstream model returns reasoning (`message.reasoning` or `message.reasoning_content`) with empty `content`, the gateway copies reasoning into `content` so chat apps still show a reply. `usage.completion_tokens_details.reasoning_tokens` is preserved when the provider reports it.

Streaming responses omit empty `delta.role` / `delta.content` and drop zero-value `data: {}` frames. That keeps OpenCode (and similar custom OpenAI clients) from rejecting the stream or wiping `model`/`content` when aggregating.

#### Non-Streaming Response

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1677652288,
  "model": "gpt-4o",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello! How can I help you today?"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 12,
    "completion_tokens": 9,
    "total_tokens": 21
  }
}
```

#### Streaming Response

When `stream: true`, returns Server-Sent Events (SSE):

```
data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk","created":1677652288,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk","created":1677652288,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk","created":1677652288,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

---

### List Models

**Endpoint**: `GET /v1/models`

Lists available models from configured providers. With `catalog.curated_only`, only the Static Model List under each provider is advertised (see [Configuration — Curated catalog](configuration.md#curated-catalog)). When model reachability probing is enabled (`health.models`), models that fail consecutive probes are omitted (see [Model Reachability](#model-reachability)).

#### Response

```json
{
  "object": "list",
  "data": [
    {
      "id": "openai/gpt-4o",
      "object": "model",
      "created": 1677652288,
      "owned_by": "openai",
      "name": "gpt-4o"
    },
    {
      "id": "nvidia_nim/meta/llama-3.1-8b-instruct",
      "object": "model",
      "created": 1677652288,
      "owned_by": "meta",
      "name": "meta/llama-3.1-8b-instruct"
    }
  ]
}
```

**Notes**:
- Every Model ID is provider-prefixed (e.g. `nvidia_nim/meta/llama-3.1-8b-instruct`) so clients can send the listed ID directly to `/v1/chat/completions`.
- `name` is a shorter display label (the upstream Provider Model ID without the gateway provider prefix), e.g. `meta/llama-3.1-8b-instruct`. Pickers that support `name` can show it; chat requests must still use `id`.
- `owned_by` reflects the provider or the upstream owner when reported.
- Aliases are never listed.
- With `catalog.curated_only: true`, only `providers.*.models` entries appear.
- Unreachable models (when probing + `hide_unreachable` are on) are not listed; use `GET /api/models?include_unreachable=true` to inspect them.

---

### Embeddings

**Endpoint**: `POST /v1/embeddings`

Creates an embedding vector representing the input text.

#### Request Body

```json
{
  "model": "text-embedding-3-small",
  "input": "The food was delicious"
}
```

**Required Fields**:
- `model` (string): Model ID to use
- `input` (string|array): Input text to embed

#### Response

```json
{
  "object": "list",
  "data": [
    {
      "object": "embedding",
      "embedding": [0.0023, -0.009, 0.015],
      "index": 0
    }
  ],
  "model": "text-embedding-3-small",
  "usage": {
    "prompt_tokens": 6,
    "total_tokens": 6
  }
}
```

---

## Dashboard Endpoints

All dashboard endpoints require the gateway API key.

### Merged Model Catalog

**Endpoint**: `GET /api/models`

Returns the merged model catalog from all configured providers, including reachability fields when probing is enabled.

#### Query Parameters

| Parameter | Description |
|-----------|-------------|
| `include_unreachable` | If `true` or `1`, return the full catalog including models hidden from `/v1/models` |

#### Response

```json
{
  "models": [
    {
      "model_id": "openai/gpt-4o",
      "name": "gpt-4o",
      "provider": "openai",
      "provider_model_id": "gpt-4o",
      "owned_by": "openai",
      "reachable": true,
      "latency_ms": 312,
      "checked_at": "2026-07-20T10:30:00Z"
    },
    {
      "model_id": "nvidia_nim/meta/llama-3.1-8b-instruct",
      "name": "meta/llama-3.1-8b-instruct",
      "provider": "nvidia_nim",
      "provider_model_id": "meta/llama-3.1-8b-instruct",
      "owned_by": "meta",
      "reachable": false,
      "latency_ms": 0,
      "last_error": "model not found",
      "checked_at": "2026-07-20T10:30:05Z"
    }
  ]
}
```

Reachability fields (`reachable`, `state`, `latency_ms`, `last_error`, `checked_at`, `error_rate`, `next_probe`) are present when the model status store is active. Unprobed models report `reachable` according to `health.models.unknown_as_reachable` (default `true`) and omit latency/error until the first probe. Full catalog stays visible until the first probe pass finishes; then recovering/unhealthy models are omitted while healthy, degraded, and (when configured) unknown models remain.

---

### Model Online Status

**Endpoint**: `GET /api/models/status`

Returns detailed per-model health state from probes and live traffic (including recovering/unhealthy models hidden from `/v1/models`).

#### Response

```json
{
  "timestamp": "2026-07-24T12:00:00Z",
  "models": [
    {
      "id": "openai/gpt-4o",
      "provider": "openai",
      "provider_model_id": "gpt-4o",
      "state": "healthy",
      "reachable": true,
      "last_probe": "2026-07-24T11:55:00Z",
      "probe_error": null,
      "error_rate": 0.02,
      "error_rate_window": "5m0s",
      "latency_ms": 245,
      "consecutive_failures": 0
    },
    {
      "id": "anthropic/claude-sonnet",
      "provider": "anthropic",
      "provider_model_id": "claude-sonnet",
      "state": "recovering",
      "reachable": false,
      "last_probe": "2026-07-24T11:58:00Z",
      "next_probe": "2026-07-24T12:03:30Z",
      "probe_error": "connection timeout",
      "error_rate": 0,
      "latency_ms": 0,
      "consecutive_failures": 2,
      "backoff_multiplier": 3.5,
      "retry_countdown_ms": 210000
    }
  ]
}
```

---

### Force Model Probe

**Endpoint**: `POST /api/models/force-probe`

Immediately re-probes one model, resetting its backoff schedule. Requires the gateway API key.

#### Request

Query: `?model_id=openai/gpt-4o`  
or JSON body: `{"model_id":"openai/gpt-4o"}`

#### Response

```json
{
  "model_id": "openai/gpt-4o",
  "previous_state": "recovering",
  "new_state": "healthy",
  "latency_ms": 156,
  "error": null
}
```

---

### Auto Mode Status

**Endpoint**: `GET /api/auto/status`

Reports whether runtime automatic model selection is enabled and the provider it targets.

#### Response

```json
{
  "enabled": true,
  "provider": "nvidia_nim",
  "note": "auto mode selects from NVIDIA NIM catalog using task, health, cost, and latency"
}
```

When `enabled` is `true`, clients can send `"model": "auto"` to `POST /v1/chat/completions` (and `POST /v1/embeddings`). The gateway classifies the request text into a task type (`elite`, `coding`, `reasoning`, `vision`, `fast`, `default`), picks the matching `task_profile` (or the built-in NIM defaults), and then scores candidate models by reachability, historical cost, and probe latency to choose the upstream model at request time.

---

### Model Reachability

NVIDIA NIM (and similar catalogs) often list models that are not currently callable — retired free endpoints, capacity-limited models, or non-chat entries. There is no reliable “available” flag on `GET /models`.

Conductor optionally probes registered providers with a minimal chat completion and:

1. Runs a full pass on every startup/redeploy, then again every `check_interval` (default `2h`)
2. Retries failures on exponential backoff so recovery does not wait for the next full pass
3. Batches probe results for atomic catalog snapshots
4. Caches health state (also updated from live chat successes/failures and error-rate tracking)
5. Hides recovering/unhealthy models from `GET /v1/models` when `health.models.hide_unreachable` is true
6. Exposes status on `GET /api/models`, `GET /api/models/status`, and `POST /api/models/force-probe`

**Defaults** (see [Configuration](configuration.md#model-reachability)):

| Setting | Default |
|---------|---------|
| Enabled | `true` |
| Providers probed | all registered (`providers: []`) |
| Hide unreachable from `/v1/models` | `true` |
| Check interval | `2h` (plus startup/redeploy pass) |
| Unhealthy threshold | `1` consecutive failure |
| Unprobed models visible after first pass | `true` (err toward availability) |
| Backoff | enabled (`30s` initial, `12h` cap, `3.5×`) |
| Live error tracking | enabled (`5m` window, `15%` degraded) |

Rate limits (`429`) and auth errors (`401`/`403`) do **not** mark a model offline.

---

### Health Check

**Endpoint**: `GET /health`

Simple health check (no authentication required).

#### Response

```json
{
  "status": "ok"
}
```

---

### Provider Health

**Endpoint**: `GET /api/health`

Returns live health status of all registered providers.

#### Response

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

---

### List Providers

**Endpoint**: `GET /api/providers`

Lists all registered providers.

#### Response

```json
{
  "providers": [
    {
      "name": "openai",
      "enabled": true
    }
  ]
}
```

---

### Usage Statistics

**Endpoint**: `GET /api/usage`

Returns total and per-provider/per-model usage with estimated cost.

#### Query Parameters

- `limit` (number): Maximum number of recent usage records to aggregate. Default: `1000`

#### Response

```json
{
  "total": {
    "requests": 150,
    "prompt_tokens": 30000,
    "completion_tokens": 15000,
    "total_tokens": 45000,
    "duration_ms": 180000,
    "input_chars": 0,
    "output_chars": 0,
    "cost_usd": 0.45
  },
  "by_model": {
    "gpt-4o": {
      "requests": 80,
      "prompt_tokens": 20000,
      "completion_tokens": 10000,
      "total_tokens": 30000,
      "duration_ms": 96000,
      "cost_usd": 0.30
    }
  },
  "by_provider": {
    "openai": {
      "requests": 80,
      "prompt_tokens": 20000,
      "completion_tokens": 10000,
      "total_tokens": 30000,
      "duration_ms": 96000,
      "cost_usd": 0.30
    }
  }
}
```

**Note**: `cost_usd` is omitted when no cost source is available.

---

### Cost Breakdown

**Endpoint**: `GET /api/usage/costs`

Returns detailed cost breakdown.

#### Response

```json
{
  "message": "Cost tracking endpoint - coming soon"
}
```

**Note**: This endpoint is currently a stub.

---

### Request Logs

**Endpoint**: `GET /api/logs`

Returns recent request logs.

#### Query Parameters

- `limit` (number): Number of logs to return. Default: `100`, Max: `1000`

#### Response

```json
{
  "logs": [
    {
      "id": "log-abc123",
      "request_id": "req-abc123",
      "method": "POST",
      "path": "/v1/chat/completions",
      "status_code": 200,
      "client_ip": "127.0.0.1",
      "user_agent": "curl/8.0",
      "provider": "openai",
      "model": "gpt-4o",
      "latency_ms": 1200,
      "created_at": "2026-07-19T10:30:00Z"
    }
  ]
}
```

---

### Current Configuration

**Endpoint**: `GET /api/config`

Returns current configuration (secrets redacted).

#### Response

```json
{
  "message": "Config endpoint - coming soon"
}
```

**Note**: This endpoint is currently a stub.

---

### Reload Configuration

**Endpoint**: `PUT /api/config/reload`

#### Response

```json
{
  "status": "ok",
  "message": "Configuration reloaded successfully"
}
```

**Note**: This endpoint is currently a stub and does not reload configuration. A restart is required.

---

## Error Responses

All errors follow the OpenAI error format:

```json
{
  "error": {
    "message": "Model 'invalid-model' not found",
    "type": "invalid_request_error",
    "param": "model",
    "code": "model_not_found"
  }
}
```

### Error Types

| HTTP Status | Type | Description |
|-------------|------|-------------|
| 400 | `invalid_request_error` | Invalid request format or parameters |
| 401 | `authentication_error` | Invalid or missing API key |
| 429 | `rate_limit_error` | Rate limit exceeded |
| 500 | `server_error` | Internal server error |
| 502 | `provider_error` | Provider returned an error |
| 503 | `service_unavailable` | Service temporarily unavailable |

---

## Rate Limiting

Rate limits are enforced per API key:

- **Default**: 1000 requests per minute (global)
- **Per Provider**: 100 requests per minute

When rate limited, returns:

```json
{
  "error": {
    "message": "Rate limit exceeded",
    "type": "rate_limit_error",
    "code": "rate_limit_exceeded"
  }
}
```

HTTP Status: `429 Too Many Requests`

---

## CORS

CORS is enabled by default for all origins. Configure in `config.yaml`:

```yaml
server:
  cors:
    enabled: true
    origins: ["*"]
    methods: ["GET", "POST", "OPTIONS"]
    headers: ["Authorization", "Content-Type"]
```

---

## Examples

### Basic Chat Completion

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'
```

### Streaming Chat Completion

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ],
    "stream": true
  }' \
  --no-buffer
```

### Embeddings

```bash
curl http://localhost:8080/v1/embeddings \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-3-small",
    "input": "Hello world"
  }'
```

### Dashboard: Provider Health

```bash
curl http://localhost:8080/api/health \
  -H "Authorization: Bearer your-api-key"
```

### Dashboard: Model Catalog + Reachability

```bash
curl http://localhost:8080/api/models \
  -H "Authorization: Bearer your-api-key"

# Include models hidden from /v1/models
curl "http://localhost:8080/api/models?include_unreachable=true" \
  -H "Authorization: Bearer your-api-key"

# Probe cache only
curl http://localhost:8080/api/models/status \
  -H "Authorization: Bearer your-api-key"
```

### Dashboard: Usage

```bash
curl http://localhost:8080/api/usage \
  -H "Authorization: Bearer your-api-key"
```

### Dashboard: Logs

```bash
curl "http://localhost:8080/api/logs?limit=50" \
  -H "Authorization: Bearer your-api-key"
```
