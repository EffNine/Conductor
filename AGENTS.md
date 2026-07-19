# AGENTS.md

## Cursor Cloud specific instructions

Novexa Gateway is a single Go/Fiber binary: an OpenAI-compatible AI gateway that routes
requests across upstream provider subscriptions, persists usage to SQLite, and exposes a
dashboard API. It is a headless HTTP service (no frontend); test it with `curl`.
Standard commands live in the `Makefile` and `README.md` (`make build|test|lint|run`).

### Non-obvious caveats

- **CGO is required.** The SQLite driver (`mattn/go-sqlite3`) needs `CGO_ENABLED=1` and a C
  compiler (`gcc`). Both are already set up. Do not build/test with `CGO_ENABLED=0` locally —
  the binary will compile but panic at runtime with "requires cgo to work". (Note: the
  production `deployments/Dockerfile` uses `CGO_ENABLED=0`, which differs from local dev.)
- **The `data/` directory must exist before running.** The default DB DSN is
  `./data/novexa.db` (relative to the working directory) and the app does NOT create the
  parent dir — it exits with `unable to open database file: no such file or directory`.
  Run `mkdir -p data` once, and always start the gateway from the repo root.
- **`NOVEXA_API_KEY` is the only hard requirement to boot.** It can be set via env var or the
  `api_key` field in `config.yaml`. Without it, startup fails config validation.
- **Providers auto-enable from env vars.** Setting `OPENAI_API_KEY` (etc.) auto-enables that
  provider even without a `config.yaml`. See `internal/config/config.go` `autoEnableProviders`.
- **Config file is optional and gitignored.** `config.yaml` (searched in `.`, `./config`,
  `/etc/novexa`) plus `data/` and `*.db` are all in `.gitignore`, so a local dev config never
  gets committed. Copy `config/config.example.yaml` to `config.yaml` to customize routes.
- **Lint findings are pre-existing.** `make lint` (golangci-lint) runs but currently reports
  several pre-existing issues (errcheck, gofmt, gosec, govet shadow, revive). The tool works;
  these are not caused by environment setup.

### Local end-to-end testing without real provider keys

There are no real upstream credentials in this environment. To exercise the full pipeline
(auth → route → provider adapter → response normalize → SQLite usage/cost tracking), point a
provider `base_url` at a local OpenAI-compatible mock and add a matching route in
`config.yaml`, then drive it with `curl` against `http://127.0.0.1:8080` using the
`Authorization: Bearer <NOVEXA_API_KEY>` header. Key endpoints: `GET /health`,
`GET /v1/models`, `POST /v1/chat/completions`, `GET /api/usage`, `GET /api/usage/costs`.
