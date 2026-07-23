# AGENTS.md

## Cursor Cloud specific instructions

Conductor is a single Go/Fiber binary: an OpenAI-compatible AI gateway that routes
requests across upstream provider subscriptions, persists usage to SQLite, and exposes a
dashboard API. It is a headless HTTP service (no frontend); test it with `curl`.
Standard commands live in the `Makefile` and `README.md` (`make build|test|lint|run`).

### Non-obvious caveats

- **CGO is required.** The SQLite driver (`mattn/go-sqlite3`) needs `CGO_ENABLED=1` and a C
  compiler (`gcc`). Both are already set up. Do not build/test with `CGO_ENABLED=0` locally —
  the binary will compile but panic at runtime with "requires cgo to work". (Note: the
  production `deployments/Dockerfile` uses `CGO_ENABLED=0`, which differs from local dev.)
- **The `data/` directory must exist before running.** The default DB DSN is
  `./data/conductor.db` (relative to the working directory) and the app does NOT create the
  parent dir — it exits with `unable to open database file: no such file or directory`.
  Run `mkdir -p data` once, and always start the gateway from the repo root.
- **`CONDUCTOR_API_KEY` is the only hard requirement to boot.** It can be set via env var or the
  `api_key` field in `config.yaml`. Without it, startup fails config validation.
- **Providers auto-enable from env vars.** Setting `OPENAI_API_KEY` (etc.) auto-enables that
  provider even without a `config.yaml`. See `internal/config/config.go` `autoEnableProviders`.
  `OLLAMA_API_KEY` enables Ollama Cloud (`https://ollama.com/v1`); `OLLAMA_BASE_URL` overrides
  the host when Ollama is enabled (e.g. local Docker). Local Ollama can also be enabled via YAML alone.
- **Config file is optional and gitignored.** `config.yaml` (searched in `.`, `./config`,
  `/etc/conductor`) plus `data/` and `*.db` are all in `.gitignore`, so a local dev config never
  gets committed. Copy `config/config.example.yaml` to `config.yaml` to customize routes.
- **Lint findings are pre-existing.** `make lint` (golangci-lint) runs but currently reports
  several pre-existing issues (errcheck, gofmt, gosec, govet shadow, revive). The tool works;
  these are not caused by environment setup.
- **Route/alias keys containing a dot don't match.** Config is loaded via Viper, whose default
  `.` key delimiter mangles map keys that contain a dot (e.g. a `routes:` or `aliases:` key like
  `meta/llama-3.1-8b-instruct`). Such a route silently won't resolve. Use the provider-prefixed
  Model ID from `/v1/models` instead (e.g. `nvidia_nim/meta/llama-3.1-8b-instruct`), which the
  router strips and dispatches without needing a matching route entry.
- **Model reachability probes (esp. NVIDIA NIM).** `/models` catalogs can list free and
  unreachable endpoints with no availability flag. By default the gateway probes **all**
  registered providers on startup/redeploy (then every `12h`). Confirmed failures drop out of
  `/v1/models` as soon as they fail (list shrinks, never flashes empty). Unprobed models stay
  visible until the first pass finishes, then only models that **passed** remain
  (`unknown_as_reachable: false`) until the next probe cycle re-checks them. Status is
  **persisted to SQLite** so Fly cold starts keep the available-only list instead of flashing
  the full catalog again. Loopback providers (local `ollama` / `lmstudio`) are skipped during
  probes so remote deploys finish the pass. Status: `GET /api/models`, `GET /api/models/status`,
  `GET /api/models?include_unreachable=true`. Config under `health.models`
  (see `docs/configuration.md`). Disable with `health.models.enabled: false`.
  Limit scope with `health.models.providers: [nvidia_nim]`.
- **Curated models only (optional).** Default is **dynamic** catalog + probe hide. Set
  `catalog.curated_only: true` (or `CONDUCTOR_CATALOG_CURATED_ONLY=true`) to also apply static
  allowlists: any provider with a `models:` list (or `CONDUCTOR_PROVIDERS_<NAME>_MODELS` CSV)
  advertises only that allowlist; providers without one still use dynamic ListModels. When
  curated-only is on and NIM has no models list, a built-in short NIM allowlist is applied.
  Fly `fly.toml` leaves curated_only off. Legacy `NOVEXA_*` env vars are still accepted as
  aliases for `CONDUCTOR_*` after the rebrand.

### Local end-to-end testing without real provider keys

There are no real upstream credentials in this environment. To exercise the full pipeline
(auth → route → provider adapter → response normalize → SQLite usage/cost tracking), point a
provider `base_url` at a local OpenAI-compatible mock and add a matching route in
`config.yaml`, then drive it with `curl` against `http://127.0.0.1:8080` using the
`Authorization: Bearer <CONDUCTOR_API_KEY>` header. Key endpoints: `GET /health`,
`GET /v1/models`, `POST /v1/chat/completions`, `GET /api/models`, `GET /api/models/status`,
`GET /api/usage`, `GET /api/usage/costs`. To test auto-hide, have the mock return 404 for one
model's chat completions and confirm it disappears from `/v1/models` after a probe pass.
