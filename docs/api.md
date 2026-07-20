# API Reference

Novexa Gateway exposes an OpenAI-compatible API plus dashboard endpoints for monitoring.

## Authentication

All endpoints except `GET /health` require authentication via the `Authorization` header:

```
Authorization: Bearer <your-api-key>
```

The API key is set via the `NOVEXA_API_KEY` environment variable.

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
- `model` (string): Model ID or alias to use
- `messages` (array): Array of message objects

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

When the upstream model returns reasoning (`message.reasoning` or `message.reasoning_content`) with empty `content`, the gateway copies reasoning into `content` so chat apps still show a reply. `usage.completion_tokens_details.reasoning_tokens` is preserved when the provider reports it.

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

Lists all available models from configured providers.

#### Response

```json
{
  "object": "list",
  "data": [
    {
      "id": "gpt-4o",
      "object": "model",
      "created": 1677652288,
      "owned_by": "openai"
    },
    {
      "id": "openai/llama3-8b",
      "object": "model",
      "created": 1677652288,
      "owned_by": "openai"
    }
  ]
}
```

**Note**: `owned_by` reflects the provider or the upstream owner when reported. Duplicate base model IDs across providers are qualified with a provider prefix.

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

Returns the merged model catalog from all configured providers.

#### Response

```json
{
  "models": [
    {
      "provider": "openai",
      "model_id": "gpt-4o",
      "provider_model_id": "gpt-4o",
      "owned_by": "openai"
    },
    {
      "provider": "groq",
      "model_id": "groq/llama3-8b",
      "provider_model_id": "llama3-8b",
      "owned_by": "meta"
    }
  ]
}
```

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
