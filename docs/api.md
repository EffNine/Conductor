# API Reference

Conductor exposes an OpenAI-compatible API plus dashboard endpoints for monitoring.

## Authentication

All endpoints except `GET /health` require authentication via the `Authorization` header:

```
Authorization: Bearer <your-api-key>
```

The scheme is matched case-insensitively (`Bearer` / `bearer`). A bare API key without the scheme, any other scheme (`Basic`, `Key`, …), an empty credential, or an extra token is rejected with `401`.

- Missing credentials → `401` with `error.code = "missing_api_key"`
- Malformed or invalid credentials → `401` with `error.code = "invalid_api_key"`
- All `401` responses include a `WWW-Authenticate: Bearer` challenge header
- Conductor never logs the `Authorization` header or the API key, and the key is never echoed in any response, trace, or metric. `GET /api/config` always reports the gateway key (and all provider keys) as `[REDACTED]`.

The API key is set via the `CONDUCTOR_API_KEY` environment variable (or `api_key` in config). If unset, Conductor generates one on first boot and persists it to `data/conductor.api_key`. Use `conductor gen-key` / `make gen-key` to print a new key.

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
- `model` (string): Model ID, alias, or a virtual capability model for automatic selection
- `messages` (array): Array of message objects

**Virtual Capability Models** (recommended):
Instead of choosing among dozens of raw upstream model IDs, clients should use one of Conductor's stable virtual models. Each represents a capability category; Conductor internally selects the best concrete provider/model.

| Virtual Model | Purpose |
|---------------|---------|
| `frontier` | Best overall / strongest generally available model |
| `coding` | Software engineering, code generation, debugging, repository work |
| `reasoning` | Deep reasoning, analysis, difficult problem solving |
| `agentic` | Tool use, multi-step execution, autonomous workflows |
| `planning` | Task decomposition, architecture planning, strategy |
| `long_horizon` | Large context and long-running tasks |
| `fast` | Low latency / responsive workloads |
| `light` | Lightweight / economical workloads |
| `vision` | Multimodal / image-capable workloads |
| `auto` | Generic automatic selection / backward-compatible fallback |

When a virtual model is sent (e.g. `model: "coding"`), Conductor selects the best concrete provider/model from all registered providers based on the category's capability requirements and the configured routing weights. The selection considers health, latency, cost, and capability match. The upstream provider **never** receives the virtual model ID — the gateway resolves it to a concrete provider and model ID before dispatching.

The `mode` field (optional) can be used to further influence selection within a virtual model category:
- `coding` — prefers tool-calling and reasoning capable models
- `reasoning` — prefers reasoning-capable models
- `vision` — requires vision/image capability when request contains image content
- `fast` — strongly prefers low-latency healthy models
- `planning` — requires reasoning + tool-calling capabilities
- `agentic` — requires reasoning + tool-calling + sufficient context capacity
- `long_horizon` — requires sufficient context capacity
- `auto` (default) — uses the classifier to infer the task type

If `mode` is omitted, the classifier infers the task type from the message content (for `auto` model) or the virtual model's implicit category is used.

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
- `chat_template_kwargs` (object): Provider-specific chat-template options (forwarded when set). For NVIDIA NIM **reasoning** families (DeepSeek V3/V4/R1, GLM, Kimi, Qwen3 thinking/coder, Nemotron super/ultra/3, MiniMax M3, Inkling, Phi-reasoning, Magistral), the gateway injects the correct per-model `chat_template_kwargs` when omitted so OpenCode and similar clients still get a streamed reply instead of empty `content` / hangs. Set `reasoning_effort: "none"` to disable thinking. See [Providers — NIM reasoning models](providers.md#nim-reasoning-models-chat_template_kwargs). OpenAI `developer` roles are remapped to `system` for NIM.

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

Returns exactly 10 virtual capability models. Raw provider model IDs are not exposed through this endpoint; the public model catalog is a model abstraction layer that presents only curated capability models. Raw provider catalog powering virtual resolution, health filtering, circuit breaker filtering, capability matching, latency scoring, cost scoring, and deterministic selection remains internal. With `catalog.curated_only`, only the Static Model List under each provider is advertised in addition to the 10 virtual models (see [Configuration — Curated catalog](configuration.md#curated-catalog)). When model reachability probing is enabled (`health.models`), unreachable raw models are omitted from internal catalog use but are still accessible via `GET /api/models?include_unreachable=true`.

#### Response

```json
{
  "object": "list",
  "data": [
    {
      "id": "frontier",
      "object": "model",
      "created": 1677652288,
      "owned_by": "conductor",
      "name": "Frontier"
    },
    {
      "id": "coding",
      "object": "model",
      "created": 1677652288,
      "owned_by": "conductor",
      "name": "Coding"
    },
    {
      "id": "reasoning",
      "object": "model",
      "created": 1677652288,
      "owned_by": "conductor",
      "name": "Reasoning"
    },
    {
      "id": "agentic",
      "object": "model",
      "created": 1677652288,
      "owned_by": "conductor",
      "name": "Agentic"
    },
    {
      "id": "planning",
      "object": "model",
      "created": 1677652288,
      "owned_by": "conductor",
      "name": "Planning"
    },
    {
      "id": "long_horizon",
      "object": "model",
      "created": 1677652288,
      "owned_by": "conductor",
      "name": "Long Horizon"
    },
    {
      "id": "fast",
      "object": "model",
      "created": 1677652288,
      "owned_by": "conductor",
      "name": "Fast"
    },
    {
      "id": "light",
      "object": "model",
      "created": 1677652288,
      "owned_by": "conductor",
      "name": "Light"
    },
    {
      "id": "vision",
      "object": "model",
      "created": 1677652288,
      "owned_by": "conductor",
      "name": "Vision"
    },
    {
      "id": "auto",
      "object": "model",
      "created": 1677652288,
      "owned_by": "conductor",
      "name": "Auto"
    }
  ]
}
```

**Notes**:
- The response contains exactly **10 virtual capability models**, all with `owned_by: "conductor"`. These are the recommended models for clients to use for capability-based routing.
- Raw provider model IDs are **not** exposed through `/v1/models`. The public model catalog is a model abstraction layer that presents only curated capability models.
- Raw provider catalog remains internal, powering virtual model resolution, health filtering, circuit breaker filtering, capability matching, latency scoring, cost scoring, and deterministic selection.
- Virtual models have `owned_by: "conductor"` and a human-readable `name` to indicate they're gateway-managed.
- Raw provider models can still be accessed internally through the catalog and via `GET /api/models` / `GET /api/models?include_unreachable=true` for administration/debugging.
- With `catalog.curated_only: true`, only `providers.*.models` entries appear in the internal catalog (plus all 10 virtual models).
- Raw provider models are not reachable through the OpenAI-compatible `/v1/models` contract; use the dashboard API `GET /api/models` for full catalog access.

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
- `model` (string): Concrete embedding model ID to use. Virtual capability models (`frontier`, `coding`, `reasoning`, `agentic`, `planning`, `long_horizon`, `fast`, `light`, `vision`, `auto`) are **not** supported for embeddings — you must specify a concrete embedding model (e.g. `text-embedding-3-small`, `openai/text-embedding-3-small`).
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

### Virtual Model Selection

Conductor exposes **10 virtual capability models** as the primary client-facing model catalog. These are always available and do not require any provider-specific configuration. When a client sends a virtual model (e.g. `model: "coding"`), Conductor resolves it to a concrete provider and model using the merged catalog and the configured routing weights.

| Virtual Model | Purpose | Key Characteristics |
|---------------|---------|---------------------|
| `frontier` | Best overall / strongest generally available model | Balanced weights, high capability preference |
| `coding` | Software engineering, code generation, debugging | Strong tool-calling & reasoning preference |
| `reasoning` | Deep reasoning, analysis, difficult problem solving | Highest capability weight, reasoning bonus |
| `agentic` | Tool use, multi-step execution, autonomous workflows | Requires reasoning + tool-calling + context |
| `planning` | Task decomposition, architecture planning, strategy | Requires reasoning + tool-calling |
| `long_horizon` | Large context and long-running tasks | Requires sufficient context capacity |
| `fast` | Low latency / responsive workloads | Latency-dominated, health-protected |
| `light` | Lightweight / economical workloads | Cost-dominated, reasonable capability |
| `vision` | Multimodal / image-capable workloads | Vision capability hard requirement |
| `auto` | Generic automatic selection / backward-compatible fallback | Balanced, uses classifier when no mode |

The optional `mode` field can further influence selection within a virtual model category:
- `coding` — prefers tool-calling and reasoning capable models
- `reasoning` — prefers reasoning-capable models
- `vision` — requires vision capability when image content is present (hard filter)
- `fast` — strongly prefers low-latency healthy models
- `planning` — requires reasoning + tool-calling; prefers execution reliability
- `agentic` — requires reasoning + tool-calling + sufficient context; strongest execution reliability
- `long_horizon` — requires sufficient context capacity; prefers sustained reliability
- `auto` (default) — uses the classifier to infer task type from message content

If `mode` is omitted, the classifier infers the task type (for `auto` model) or the virtual model's implicit category guides selection.

**The upstream provider NEVER receives a virtual model ID** — the gateway always resolves it to a concrete provider and model ID before dispatching.

The legacy NVIDIA NIM `auto` configuration (`providers.nvidia_nim.auto`) is preserved for backward compatibility. When enabled and the request is routed to NVIDIA NIM, the NIM-specific auto selector takes precedence. Otherwise, the catalog-backed virtual resolver is used.

**Dashboard Endpoint**: `GET /api/auto/status` reports whether the catalog-backed virtual resolver is available.

```json
{
  "enabled": true,
  "note": "virtual model selection from all registered providers using health, cost, latency, and capability scoring"
}
```

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

### Routing Trace List

**Endpoint**: `GET /api/routing/traces`

Returns a paginated, filterable list of persisted routing decision traces. Most recent first.
This endpoint is **read-only observability** — it never participates in routing, provider selection, runtime state updates, or cache behavior.

#### Authentication

Protected by the gateway API key (same as all other dashboard endpoints).

#### Query Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `mode` | string | Canonical resolved mode. Uses `ParseMode` normalization (case-insensitive). Supported values: `auto`, `coding`, `reasoning`, `vision`, `fast`, `planning`, `agentic`, `long_horizon`. Invalid values return HTTP 400. |
| `provider` | string | Exact match on `selected_provider`. |
| `model` | string | Exact match on `selected_model`. |
| `requested_model` | string | Exact match on the original virtual/model ID before resolution (e.g. `"coding"`, `"frontier"`, `"auto"`). |
| `runtime_hash` | string | Exact match on the deterministic hash of the scoring-relevant runtime snapshot. |
| `outcome` | string | Exact match on outcome. One of: `selected`, `rejected`, `failed`. |
| `from` | string | Inclusive lower bound on decision timestamp. RFC3339 format (e.g. `2026-08-19T00:00:00Z`). |
| `to` | string | Inclusive upper bound on decision timestamp. RFC3339 format. Must not be before `from`. |
| `limit` | integer | Maximum rows to return. Default: `50`. Range: `1`–`200`. Values outside this range return HTTP 400. |
| `offset` | integer | Number of rows to skip. Default: `0`. Must be non-negative. Invalid values return HTTP 400. |

Invalid or unrecognised parameters return HTTP 400 with the offending parameter named:

```json
{
  "error": {
    "message": "invalid mode \"foo\": supported values are auto, coding, reasoning, vision, fast, planning, agentic, long_horizon",
    "type": "invalid_request_error",
    "param": "mode",
    "code": "invalid_request"
  }
}
```

#### Response

```json
{
  "data": [
    {
      "decision_id": "d7f8a9b0-1234-5678-9abc-def012345678",
      "timestamp": "2026-08-20T14:30:00Z",
      "schema_version": 2,
      "requested_mode": "coding",
      "resolved_mode": "coding",
      "mode_source": "explicit",
      "task_type": "code_generation",
      "selected_provider": "openai",
      "selected_model": "gpt-4o",
      "requested_model": "coding",
      "runtime_hash": "a3f2c1...",
      "selected_score": 0.87,
      "candidate_count": 5,
      "outcome": "selected",
      "created_at": "2026-08-20T14:30:01Z"
    }
  ],
  "pagination": {
    "limit": 50,
    "offset": 0,
    "count": 1
  }
}
```

#### Response Fields

| Field | Type | Description |
|-------|------|-------------|
| `decision_id` | string | Unique identifier for this routing decision. |
| `timestamp` | string | When the routing decision was made (RFC3339 UTC). |
| `schema_version` | int64 | Trace schema version (currently `2`). |
| `requested_mode` | string | The raw mode string from the request (before normalization). |
| `resolved_mode` | string | The canonical mode after `ParseMode` resolution. |
| `mode_source` | string | How the mode was determined: `explicit` (client-supplied), `classifier` (inferred), or `virtual` (implicit from virtual model category). |
| `task_type` | string | Classifed task type from the request classifier (e.g. `code_generation`, `reasoning`, `chat`). |
| `selected_provider` | string | Provider name chosen for this decision (e.g. `openai`, `anthropic`). |
| `selected_model` | string | Concrete provider model ID dispatched to upstream (e.g. `gpt-4o`). |
| `requested_model` | string | Original virtual or concrete model ID from the request before resolution. |
| `runtime_hash` | string | Deterministic hash of the scoring-relevant runtime snapshot at decision time. |
| `selected_score` | float64 | Total score of the winning candidate. |
| `candidate_count` | int | Number of candidates scored in this decision. |
| `outcome` | string | `selected` (winner chosen), `rejected` (decision completed, no winner), or `failed` (pipeline stage failed before selection). |
| `created_at` | string | Row insertion time in the trace store (RFC3339 UTC). |

Results are ordered by `timestamp DESC, decision_id DESC`.

When the trace store is not available (routing disabled), returns HTTP 503:

```json
{
  "error": {
    "message": "Routing trace store not available",
    "type": "server_error",
    "code": "trace_store_unavailable"
  }
}
```

---

### Routing Trace Detail

**Endpoint**: `GET /api/routing/traces/:id`

Returns the full canonical `DecisionTrace` for a single routing decision. Unknown IDs return HTTP 404.

#### Authentication

Protected by the gateway API key.

#### Path Parameter

| Parameter | Description |
|-----------|-------------|
| `id` | The `decision_id` value from a list response or another trace reference. |

#### Response (HTTP 404 for unknown IDs)

```json
{
  "error": {
    "message": "Routing trace 'd7f8a9b0-1234-5678-9abc-def012345678' not found",
    "type": "invalid_request_error",
    "code": "trace_not_found"
  }
}
```

#### Response Fields (full DecisionTrace)

The full trace contains every field from the P3.14 canonical contract:

| Field | Type | Description |
|-------|------|-------------|
| `decision_id` | string | Unique routing decision identifier. |
| `trace_schema_ver` | int64 | Trace schema version (currently `2`). |
| `timestamp` | string | Decision timestamp (RFC3339 UTC). |
| `requested_mode` | string | Raw mode string from the request. |
| `requested_model` | string | Original model ID before resolution. |
| `resolved_mode` | string | Canonical resolved mode. |
| `mode_source` | string | Mode source: `explicit`, `classifier`, or `virtual`. |
| `mode_description` | string | Static prose summary of the mode's routing policy. |
| `mode_traits` | array[string] | Machine-readable tags describing the mode's policy. |
| `intent` | object\|null | Request intent: `{ "task_type": "...", "confidence": 0.95 }`. |
| `capability_requirements` | object\|null | Resolved capability requirements for this decision. |
| `context_requirement` | int | Estimated token budget the mode enforced. |
| `effective_weights` | object | Normalized routing weights used for this decision (`health`, `latency`, `cost`, `capability`). |
| `mode_bonuses` | object | Per-mode capability bonuses applied (`tool_calling`, `reasoning`, `structured`, `context_capacity`). |
| `runtime_hash` | string | Deterministic hash of the scoring-relevant runtime snapshot. |
| `candidate_scores` | array[object] | Per-candidate score breakdown (see below). |
| `winner` | object\|null | The selected route (`provider_name`, `provider_model_id`, `model_id`). |
| `rejection_reasons` | array[object] | Why each non-winning candidate was rejected. |
| `stage_results` | array[object] | Pipeline stage execution results (`name`, `duration_ms`, `status`, `metadata`, `output_ref`). |
| `events` | array[object] | Timeline events (`type`, `timestamp`, `payload`). |

**CandidateScore fields:**

| Field | Type | Description |
|-------|------|-------------|
| `provider` | string | Provider name. |
| `provider_model_id` | string | Upstream model ID for this candidate. |
| `total_score` | float64 | Final weighted score. |
| `health_score` | float64 | Health factor contribution. |
| `latency_score` | float64 | Latency factor contribution. |
| `cost_score` | float64 | Cost factor contribution. |
| `capability_score` | float64 | Capability match factor contribution. |
| `mode_bonus` | float64 | Additive bonus for mode-relevant capability. |
| `context_bonus` | float64 | Additive bonus for context capacity match. |
| `telemetry_pref` | float64 | Execution telemetry preference contribution. |
| `selected` | bool | True for the winning candidate. |
| `rejected` | bool | True for candidates filtered out before scoring. |
| `rejection_reason` | string | Why this candidate was rejected, if applicable. |

**RejectionReason fields:**

| Field | Type | Description |
|-------|------|-------------|
| `provider` | string | Provider name. |
| `reason` | string | Human-readable rejection explanation. |

**StageResult fields:**

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Stage name (e.g. `classifier`, `scoring`, `selection`). |
| `duration_ms` | int64 | Stage duration in milliseconds. |
| `status` | string | `pending`, `running`, `completed`, `failed`, or `skipped`. |
| `metadata` | object\|null | Stage-specific metadata. |
| `output_ref` | string\|null | Opaque handle to the stage's output payload. |

#### Security Contract

The trace API **never exposes**:
- API keys or provider credentials
- Request prompts, messages, or raw request bodies
- Response tokens or content
- Authorization headers or cookies
- Any secrets

The original request is intentionally **not embedded** in traces. Traces contain only routing metadata, scoring breakdowns, and pipeline stage outcomes.

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

## Failure Analytics

Read-only observability over persisted chat execution attempts (P4.5).
Rows are written asynchronously by attempt persistence (see
`execution.attempts.enabled`) and bounded by
`execution.attempts.retention`. Both endpoints require the gateway API key.

### Failure List

**Endpoint**: `GET /api/failures`

Query parameters:

| Parameter | Type | Description |
|---|---|---|
| `class` | string | Exact match on failure class (`rate_limited`, `timeout`, `auth_failed`, `capacity`, `upstream_error`, `network_error`, `invalid_request`, `unknown`) |
| `provider` | string | Exact match on provider name |
| `model` | string | Exact match on virtual model |
| `window` | duration | Only rows newer than this (e.g. `1h`, `24h`); default unbounded, capped at 30d |
| `limit` | int | 1..200, default 50 |
| `offset` | int | Default 0 |

Response: `{ "total": <matching count>, "limit": …, "offset": …,
"attempts": [ { "created_at", "request_id", "correlation_id",
"virtual_model", "mode", "provider", "provider_model_id",
"candidate_index", "attempt_index", "failure_class", "outcome"
(success\|failed\|skipped), "skip_reason", "http_status", "latency_ms",
"retry_wait_ms", "retry_after_honored" }, … ] }` — newest first.

### Failure Summary

**Endpoint**: `GET /api/failures/summary`

Query parameters:

| Parameter | Type | Description |
|---|---|---|
| `window` | duration | Aggregation window; default `24h`, capped at 30d |
| `bucket` | duration | Time-bucket width; default window/12, min 1m |

Response: `{ "window_seconds", "bucket_seconds", "total_failures",
"by_provider": {…}, "by_class": {…}, "buckets": [ { "bucket_start",
"count" }, … ] }`

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

### Basic Chat Completion (Concrete Model)

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

### Virtual Capability Models (Recommended)

```bash
# Frontier - best overall model
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "frontier",
    "messages": [{"role": "user", "content": "Solve this complex problem"}]
  }'

# Coding - software engineering tasks
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "coding",
    "messages": [{"role": "user", "content": "Write a Go HTTP server"}]
  }'

# Reasoning - deep analysis
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "reasoning",
    "messages": [{"role": "user", "content": "Analyze the trade-offs of microservices vs monolith"}]
  }'

# Agentic - autonomous workflows
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "agentic",
    "messages": [{"role": "user", "content": "Build a complete REST API with tests"}],
    "tools": [...]
  }'

# Fast - low latency
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "fast",
    "messages": [{"role": "user", "content": "Quick answer: what is 2+2?"}]
  }'

# Vision - image understanding
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "vision",
    "messages": [{
      "role": "user",
      "content": [
        {"type": "text", "text": "Describe this image"},
        {"type": "image_url", "image_url": {"url": "https://example.com/image.png"}}
      ]
    }]
  }'

# Auto - generic automatic selection
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "auto",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'

# Auto with explicit mode hint
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "auto",
    "mode": "reasoning",
    "messages": [{"role": "user", "content": "Explain quantum computing"}]
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

### Dashboard: Streaming

```bash
curl http://localhost:8080/api/streams \
  -H "Authorization: Bearer your-api-key"
```

Returns live streaming pipeline statistics:

```json
{
  "active_streams": 0,
  "streams_started": 12,
  "streams_completed": 11,
  "streams_cancelled": 1,
  "streams_timeout": 0,
  "streams_errors": 1,
  "chunks_total": 340,
  "bytes_total": 12288,
  "average_duration_ms": 1842.5,
  "average_chunks": 28.3,
  "average_bytes": 1024.0,
  "providers": {
    "openai": {
      "streams_started": 12,
      "streams_completed": 11,
      "streams_cancelled": 1,
      "streams_timeout": 0,
      "streams_errors": 1,
      "chunks_total": 340,
      "bytes_total": 12288,
      "average_duration_ms": 1842.5,
      "average_chunks": 28.3,
      "average_bytes": 1024.0
    }
  }
}
```
