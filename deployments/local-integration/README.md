# Conductor local integration (OpenCode + CodeBro)

Dry-run stack for pointing OpenCode at Conductor without real provider keys:

```
OpenCode (host) ──HTTP──▶ Conductor :8080 ──HTTP──▶ mock-upstream :9119
        ▲
        └── CodeBro MCP tools attached to the host session
```

## 1. Start the stack

```bash
cd ~/projects/active/conductor
go build -o bin/conductor ./cmd/conductor

# terminal 1: deterministic upstream
go run deployments/local-integration/mock-upstream.go -addr 127.0.0.1:9119

# terminal 2: conductor (runs from this dir so config + data/ are isolated)
cp deployments/local-integration/config.example.yaml deployments/local-integration/config.yaml
(cd deployments/local-integration && ../../bin/conductor)
```

Gateway key for this instance: `sk-conductor-local`

Two gotchas discovered during bring-up:
1. **Port**: host :8080 is typically taken by the OpenSandbox edge, so this
   stack uses **18080** everywhere.
2. **Key precedence**: `CONDUCTOR_API_KEY` in your shell overrides the yaml
   `api_key` (viper AutomaticEnv). Launch with `env -u CONDUCTOR_API_KEY`
   to keep this instance isolated on `sk-conductor-local`.

Note: provider env keys present in your shell (e.g. `AGNES_API_KEY`) are
auto-enabled by Conductor and will be health-probed even when unused by
routes. Harmless, but visible under `/api/models/status`.

Routing traces require `routing.enabled: true` (set here); the decision
pipeline then persists a DecisionTrace per request.

## 2. Smoke test

```bash
curl -s http://127.0.0.1:18080/health

curl -s http://127.0.0.1:18080/v1/models \
  -H "Authorization: Bearer sk-conductor-local" | head -c 400

curl -s http://127.0.0.1:18080/v1/chat/completions \
  -H "Authorization: Bearer sk-conductor-local" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"ping"}]}'

# streaming
curl -N http://127.0.0.1:18080/v1/chat/completions \
  -H "Authorization: Bearer sk-conductor-local" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"ping"}]}'
```

Observability while testing:

```bash
curl -s "http://127.0.0.1:18080/api/failures/summary?window=24h" -H "Authorization: Bearer sk-conductor-local"
curl -s "http://127.0.0.1:18080/api/routing/traces?limit=5" -H "Authorization: Bearer sk-conductor-local"
curl -s "http://127.0.0.1:18080/api/models/status" -H "Authorization: Bearer sk-conductor-local"
```

## 3. Point OpenCode at Conductor

Merge into your `opencode.json` (project or `~/.config/opencode/opencode.json`).
Do NOT remove existing providers — add alongside them:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "conductor": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Conductor (local)",
      "options": {
        "baseURL": "http://127.0.0.1:18080/v1",
        "apiKey": "sk-conductor-local"
      },
      "models": {
        "gpt-4o": { "name": "Conductor · gpt-4o (mock chain)" }
      }
    }
  }
}
```

Then restart OpenCode and pick `Conductor · gpt-4o` from `/models`.
Every request now flows OpenCode → Conductor (auth, routing, budgets,
retries, breakers, attempt persistence, traces) → mock upstream.

## 4. Going live later

Replace the mock upstream with a real subscription by editing this config:
set `providers.openai.base_url` back to `https://api.openai.com/v1` (or point
a different provider block at another subscription) and use the real API key.
No OpenCode-side change is needed beyond keeping the same model IDs.

CodeBro MCP needs no changes — it attaches to the host session, not to
Conductor.
