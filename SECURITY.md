# Security Policy

## Reporting a Vulnerability

Open a [GitHub security advisory](https://github.com/EffNine/conductor/security/advisories/new)
rather than a public issue. Include the affected version, a reproduction, and
impact. You can expect an initial response within 7 days.

## Security Model

Conductor is designed for **single-operator self-hosting**: one person (or one
trusted team) holds the gateway key and their own provider keys. It is not
multi-tenant infrastructure.

### Authentication

- Every endpoint except `GET /health` requires `Authorization: Bearer <gateway key>`
  (fail-closed 401; scheme is case-insensitive; bare keys rejected).
- The gateway key is never logged or echoed; `/api/config` redacts it.
- If `CONDUCTOR_API_KEY` is unset, a random key is generated to
  `data/conductor.api_key` (mode `0600`) — set the env var explicitly in production.

### Secrets Handling

- Provider keys live in your config file or environment on **your** machine;
  Conductor forwards them only to the corresponding upstream provider.
- Nothing is sent to any third-party aggregation service.
- Config responses via the dashboard API are redacted.

### Data

- Usage, cost, traces, and attempt records persist locally in SQLite at
  `./data/conductor.db`. Prompt/response bodies are not persisted by the gateway.
- Restrict filesystem permissions on `data/`; it holds your database and key file.

### Network

- CORS defaults to `*` — restrict `server.cors.origins` for browser-facing clients.
- No built-in TLS: terminate HTTPS with Caddy/Nginx/your platform.
- A global inbound rate limiter is available (`rate_limit.*`) as a runaway-client guard.

### Agent Tools

Conductor's task runtime includes fs/shell/git tools sandboxed to a configured
workspace root. Configure roots that do not contain sensitive material, and do
not expose the task API to untrusted callers.

## Supported Versions

Only the latest `main` (and Docker image built from it) receives security fixes.
