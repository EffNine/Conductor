# ARCHITECTURE_LOCK.md — EffNine Infrastructure v1.0

**EffNine Infrastructure v1.0**
**Status: FROZEN**
**Date: 2026-08-30**
**Classification: READY WITH DOCUMENTED RISKS**

> This file is the canonical freeze marker. The architecture described here is the reference implementation.
> Do not redesign, replace working components, or "modernize" without an explicit future change request.

---

## 1. Frozen Baseline

The current EffNine infrastructure has completed:

- security hardening
- single-admin authentication
- service-token architecture
- MyContext integration
- Conductor integration
- hosting/source separation
- real Rust MyContext runtime
- production MyContext encryption
- Cloudflare tunnel
- Caddy reverse proxy
- public end-to-end verification
- backup/recovery verification
- runtime smoke testing

Accepted release baseline: **READY WITH DOCUMENTED RISKS** per `FINAL_RELEASE_ASSESSMENT_2026-08-30.md` and `FINAL_RUNTIME_PROMOTION_REPORT_2026-08-30.md` (2026-08-30).

Baseline containers (verified `docker ps` 2026-08-30):

```
effnine-control-plane  Up (healthy)  3000/tcp  effnine/control-plane:latest
effnine-mycontext      Up (healthy)  8787/tcp  effnine/mycontext:latest  mc serve --host 0.0.0.0:8787 --production  encryption:on
effnine-conductor      Up (healthy)  8080/tcp  effnine/conductor:latest
effnine-caddy          Up            80:80 only (443 disabled on dev PC due to tailscaled, canonical 443:443 for VPS)  caddy:2-alpine
effnine-cloudflared    Up            outbound-only  cloudflare/cloudflared:latest  tunnel 9cbad8ad-6a3c-4be6-aa85-f3b5dbc5e211 quic 4 conns kul01/sin07
```

Tests executed 2026-08-30: `domain/control-plane go test 4 packages PASS`, `active/mycontext cargo test --lib 152 PASS`, `active/conductor go test 44 packages PASS`, `caddy validate Valid`, `docker compose config Valid` (host), `docker compose config` canonical via `.env.example` validates with TUNNEL_TOKEN set.

---

## 2. Canonical Architecture

### Topology

```
                         INTERNET
                             │
                         Cloudflare
                      (TLS 1.3, HSTS 63072000)
                             │
                    cloudflared Tunnel (outbound-only, quic)
                    tunnel: 9cbad8ad-6a3c-4be6-aa85-f3b5dbc5e211
                    credentials: /home/afnan/projects/effnine-host/cloudflared/credentials/9cbad8ad-6a3c-4be6-aa85-f3b5dbc5e211.json (0600)
                    config: /home/afnan/projects/effnine-host/cloudflared/config.yml → http://caddy:80
                             │
                           Caddy  (caddy:2-alpine)
                           :80 (dev PC, Tunnel) / :443 (canonical VPS)
                           auto_https off for Tunnel, HSTS, Host 400, /internal/* 404, Cookie delete
                             │
                    Docker bridge network  effnine  172.20.0.0/16
                             │
          ┌──────────────────┼──────────────────┐
          │                  │                  │
          ▼                  ▼                  ▼
    EffNine Control       MyContext          Conductor
       Plane               :8787              :8080
       :3000            mc serve            Go/Fiber
       Go/Fiber        --production        TokenStore
          │            encryption:on          │
          │                  │                  │
          └───────────── credential lifecycle ──┘
                    service_token (effnine.db)
```

Public hostnames (Cloudflare-proxied):

```
effnine.dev           → control-plane:3000  (via Caddy)   | currently A via Pages for effnine.dev, CNAME via Tunnel for mcp/conductor
mcp.effnine.dev       → mycontext:8787
conductor.effnine.dev → conductor:8080
```

Trust zones: Internet (untrusted) → Cloudflare (semi-trusted) → cloudflared (trusted outbound only) → Caddy (trusted, Host/CORS/HSTS) → effnine bridge 172.20.0.0/16 (trusted, private) → apps (private, no host ports).

Management plane: control-plane creates tokens, owns `effnine.db`. Runtime plane: Worker/Daemon/Gateway verify per-request against `service_token` without needing control-plane HTTP when file mount present (file `effnine.db:ro` survives control-plane downtime; D1 survives for Worker).

---

## 3. Security Invariants (must hold in every deployment)

**I1:** Exactly one admin account (`admin_account.singleton=1 UNIQUE CHECK` + triggers + `bootstrap BEGIN IMMEDIATE` race protection) — verified `SELECT COUNT(*)=1`.

**I2:** No public registration (`app.All("/api/*/register" → 404)`) — verified.

**I3:** `mct_` cannot authenticate to Conductor → 401 `wrong_service` (tested `TestTokenIsolationBetweenServices` + live `mct_ → conductor 401`).

**I4:** `cond_` cannot authenticate to MyContext → 401 (tested + live `cond_ → mycontext 401`).

**I5:** Raw service tokens are not persisted (`token_hash = SHA256(raw)` hex, `token_prefix` 8, `token_fingerprint` 16 hex, raw shown once in 201 with warning, never in `GET` or `token_hash`) — tested `TestTokenHashStoredNotRaw`, live DB `token_hash` hex only.

**I6:** MyContext control-plane DB mount is read-only (`effnine-data:/app/data:ro` or host `./data:/app/data:ro` with `mode=ro,_query_only=true,_journal_mode=WAL,_busy_timeout=5000`) — verified `docker inspect Mounts ro`, `TokenStore DSN mode=ro`.

**I7:** Provider API keys never reach clients (`config.Redacted()`, `audit.RedactMeta`, `security/redact.rs` → `[REDACTED]`, never in `GET /api/config`, never in logs) — tested `TestAuth_NoKeyLeakageInResponse`.

**I8:** `/internal/*` is not publicly reachable (Caddy `@internal path /internal/* → 404` before `reverse_proxy`, control-plane also requires `X-Internal-Key`) — verified `curl https://mcp.effnine.dev/internal/verify → 404` via `1.1 Caddy`, `curl https://conductor.effnine.dev/internal/verify → 404`.

**I9:** Caddy does not persist Cookie headers (`log filter request>headers>Cookie delete` ×3 per Caddyfile) — verified `docker logs caddy JSON` has `Authorization: REDACTED` but no `Cookie` field.

**I10:** Source and runtime directories remain separate (`/home/afnan/projects/` = source/development, `/home/afnan/projects/effnine-host/` = hosting/runtime) — verified `ls effnine-host` has no `domain/`, `active/`, `src/`.

**I11:** Runtime secrets are not in Git (`.env` 0600 host-only, `.mycontext_key` 0600 host-only, `cloudflared/credentials` 0600 host-only, `*.db` gitignored, `git ls-files` shows none) — verified `git ls-files` no `.env/.db/.key`.

**I12:** SSRF protections remain enforced (`ValidateProviderURL` blocks localhost, loopback, RFC1918, link-local, multicast, CGNAT, TEST-NET, metadata 169.254.169.254, ULA fc00::/7, hex/decimal tricks `0x7f.0.0.1`, `2130706433`, scheme https, no userinfo, DialContext private-IP check, CheckRedirect blocks public→private) — verified `TestValidateProviderURL_BlocksPrivate` 5 tests PASS, conductor live.

**I13:** Runtime services do not unnecessarily depend on control plane being available (Daemon file mount `verify_control_plane_file` survives `docker stop control-plane` → 200; Worker D1 direct lookup; Conductor TokenStore file mount; HTTP fallback only when file missing, 2s timeout fail-closed, no cache) — verified `docker stop control-plane` → `mycontext tools/list` still 200 via file.

**I14:** Production MyContext encryption remains enabled (`--production` requires `MYCONTEXT_ENCRYPTION_KEY_FILE`/`MYCONTEXT_ENCRYPTION_KEY` 32 bytes, `repo.is_encrypted()=true`, `meta/encryption.json {"enabled":true,"algorithm":"xchacha20poly1305"}`, `meta/keycheck enc:v1:...`, `MYCENC1||nonce||ciphertext` objects, key 0600 never in image/logs/API, `Cargo: CryptoConfig::from_env` perms `mode &0o077==0`) — verified `mc status` with key 200, without key `bail is encrypted`, container `encryption:true`.

**I15:** Docker application ports are not unnecessarily public (only `caddy 80:80` (dev PC, canonical 80+443) published, apps `expose` only, no `privileged`, no `host` networking, no docker socket) — verified `docker compose config | grep ports` only Caddy.

---

## 4. Deployment Invariants

### Canonical Paths

```
SOURCE:                 /home/afnan/projects/
  domain/control-plane  Go control plane (Fiber, SQLite, Argon2id, CSRF)
  domain/mcp-worker     Cloudflare Worker mcp.effnine.dev (D1, Host/CORS, SHA256 verify)
  domain/conductor-worker Cloudflare Worker conductor.effnine.dev (proxy to Fly, Host/CORS)
  domain/shared         control_plane_schema.sql (singleton trigger, service_token, audit_log)
  domain/effnine        Static site effnine.dev (public/admin, Cloudflare Pages, git EffNine/effnine)
  active/mycontext      Rust daemon + Worker (Cargo, 152 lib tests, XChaCha20 encryption)
  active/conductor      Go gateway (44 packages, SSRF, TokenStore, scope)
  infrastructure/       Canonical deployment templates (docker-compose.yml, Caddyfile, .env.example, SECURITY_HARDENING.md)
  scripts/              deploy-host.sh (validate → build → sync-config → up → health_check)

HOST:                   /home/afnan/projects/effnine-host/
  data/effnine.db       SQLite control-plane DB (0700 dir, 0600 file, WAL, singleton, service_token, audit_log)
  data/conductor.db     Conductor state (0600)
  data/mycontext/       MyContext repo (0700 dir, repo.db 0600, meta/encryption.json, meta/keycheck enc:v1:..., objects/)
  .env                  Runtime secrets (0600, EFFNINE_SESSION_SECRET, EFFNINE_INTERNAL_API_KEY, CLOUDFLARE_TUNNEL_TOKEN, provider keys)
  .mycontext_key        MyContext master key (0600, 64 hex, XChaCha20, mounted ro to /run/secrets/mycontext_key)
  cloudflared/          Tunnel runtime (0700 dir, config.yml 0600, credentials/*.json 0600, tunnel 9cbad8ad-...)
  Caddyfile             Runtime Caddy (host copy, auto_https off for Tunnel, Cookie delete ×3, /internal 404)
  docker-compose.yml    Runtime compose (host bind mounts ./data, ./data/mycontext, ./.mycontext_key:ro, caddy 80:80 only)
  backups/              0700, mycontext-plaintext-2026-08-30-preprod.tar.gz + encrypted backups, effnine.db VACUUM INTO dumps
  logs/                 (optional host logs)

RUNTIME DATABASE:       /home/afnan/projects/effnine-host/data/effnine.db
RUNTIME SECRETS:        /home/afnan/projects/effnine-host/.env (0600)
CLOUDFLARED:            /home/afnan/projects/effnine-host/cloudflared/
  config.yml → ingress mcp/conductor/effnine → http://caddy:80
  credentials/9cbad8ad-6a3c-4be6-aa85-f3b5dbc5e211.json
```

Source vs Host invariant: source is Git-tracked templates/docs/code; host is runtime state. Do not merge, do not commit host data, do not init git in host, do not place `live database / runtime secrets / Cloudflare credentials / production backups` in source tree. `effnine-host/.gitignore` + root `.gitignore effnine-host/` enforce.

### Docker Topology (locked)

```
Internet → Cloudflare (TLS 1.3) → cloudflared (outbound quic, no ports) → Caddy (:80 dev, :443 canonical, auto_https off, Host 400, /internal 404) → effnine bridge 172.20.0.0/16 → control-plane:3000 / mycontext:8787 (production encrypted) / conductor:8080
```

Only intentionally public entry is Caddy. No app ports published. No `privileged`, no `network_mode: host`, no docker socket mount.

### Caddy (public reverse proxy)

- TLS/proxy boundary, host routing (`effnine.dev`, `mcp.effnine.dev`, `conductor.effnine.dev` + `www`), security headers (`HSTS 63072000; includeSubDomains; preload`, `nosniff`, `DENY`, `no-referrer`, `Permissions-Policy`, `Cross-Origin-Opener-Policy`, `-Server`), internal path blocking, request forwarding with `header_up Host, X-Forwarded-Host, X-Forwarded-Proto, X-Request-ID, X-Forwarded-For, X-Real-IP`, access logging with `Cookie delete`.

### Cloudflare Tunnel

- Outbound-only (`tunnel --no-autoupdate --config /etc/cloudflared/config.yml run`, user `0:0` to read 0600 files). No inbound firewall rule. DNS: `cloudflared tunnel route dns 9cbad8ad → mcp.effnine.dev, conductor.effnine.dev → 104.21.x (proxied)`, `effnine.dev` currently A via Pages (not via Tunnel, see Risks).

### Development-PC Limitations (accepted)

- Single-PC SPOF, Tailscale occupying 443 (so dev PC `caddy ports: 80:80` only, canonical `443:443` for VPS). Portable compose architecture.

---

## 5. Authentication Model (locked)

Admin:

```
Argon2id  ($argon2id$v=19$m=65536,t=3,p=2$salt$hash, salt 16B, hash 32B, base64.RawStdEncoding)
opaque sessions  mcs_<43> (32 random bytes base64url, SHA256 hash stored, csrf_secret per-session uuid)
HttpOnly Secure SameSite=Strict  Set-Cookie: effnine_session=mcs_...; Max-Age=604800; Path=/; SameSite=Strict
CSRF double-submit  X-CSRF-Token: HMAC-SHA256(csrf_secret, sessionToken)  constant-time hmac.Equal, SameSite=Strict
session expiry  7d absolute, 12h idle sliding (last_seen_at refreshed, idle_expires_at updated)
session invalidation  logout single, revoke-all requires password re-auth, password change revokes all, bootstrap --reset-password revokes all
Rate limit  5/min per IP (login), 300/min general, 300/min mycontext, 600/min conductor, Fiber TrustedProxies 10/172/192/127
Host validation  Caddy @notAllowed 400 + Go HostAllowlist (only Host header, never X-Forwarded-Host)
```

Do not replace Argon2id with SHA-256/MD5/plaintext/reversible encryption without security review. Do not replace opaque sessions with long-lived bearer casually.

---

## 6. Service Token Model (locked)

```
MyContext:  mct_<43>  (32 random bytes base64url, tokens.GenerateRaw("mycontext"))
Conductor:  cond_<43> (32 random bytes base64url, tokens.GenerateRaw("conductor"))
```

Properties: `opaque`, `random (crypto/rand)`, `revocable (revoked_at)`, `expirable (expires_at RFC3339)`, `service-scoped (CHECK service IN ('mycontext','conductor'))`, `hash-only at rest (token_hash=hex(SHA256(raw)) UNIQUE)`, `prefix 8 for UI`, `fingerprint 16 hex for audit`, `scopes JSON`.

Raw tokens shown once in `POST /api/admin/tokens 201 {token: raw, warning: store once}` held in browser memory auto-hide 60s, not in localStorage/sessionStorage/IndexedDB/URL, never in `GET` list/detail or `token_hash`. After creation raw cannot be recovered — recover via `rotate/reissue`, not plaintext recovery. Do not store plaintext for convenience.

Service isolation: `mct_ → MyContext` only, `cond_ → Conductor` only, server-side `WHERE token_hash=? AND service=?` + `service.String != req.Service → 401 wrong_service`. Client-controlled service identity never overrides server expectation.

Authentication ≠ Authorization: `valid?` (hash, revoked, expired, service) vs `allowed?` (scope). Scopes:

```
MyContext:   mycontext:read (context_tree/search/read/history/request_permission)
             mycontext:write (context_write/propose)
             mycontext:admin (implies all)  Worker requiredScopeForTool + daemon required_mycontext_scope_for_tool

Conductor:   conductor:read (GET /v1/models, /api/models, /api/usage, /api/config)
             conductor:execute (POST /v1/chat/completions, /v1/embeddings)
             conductor:admin (POST /api/models/reset-status, PUT /api/config/reload)  admin implies all via RequireScope
             Wrong scope → 403 insufficient_scope (not 401)
```

Internal verify: `POST /internal/verify {token, service, scopes?}` with `X-Internal-Key: EFFNINE_INTERNAL_API_KEY` (32B base64, env, 0600) or `Authorization: Bearer`, over `effnine` bridge only, Caddy blocks public `404`, requires service match, scope subset, revocation/expiration, fail-closed 503 if key not configured, 2s timeout for daemon HTTP fallback, per-request no cache, audit `service.token.used` with fingerprint only.

Logging security: never persist `passwords`, `mcs_`, `mct_`, `cond_`, `Authorization`, `Cookie`, `provider API keys`, `internal API keys`. Caddy `Cookie delete` ×3, audit `RedactMeta` + `scrubRawSecrets` (regex `mct_/cond_/mcs_/sk-` → `[REDACTED]`), Worker/daemon `redact()` replaces bearer with `[REDACTED]`.

---

## 7. MyContext Architecture (locked)

```
Worker (Edge, Cloudflare D1)  +  Rust daemon (tiny_http, 0.0.0.0:8787 --production)
```

Authentication strategy: control-plane service-token authority → MyContext verification (D1 direct SELECT `WHERE token_hash=SHA256(raw)`, daemon file mount `EFFNINE_DB_PATH=/app/data/effnine.db:ro` preferred → SELECT same, fallback HTTP `POST http://control-plane:3000/internal/verify` with `X-Internal-Key`), plus legacy `auth_tokens`/`MCP_API_KEY` compatibility documented as transitional only. Do not create permanently independent MyContext auth authority or third credential database. Legacy cleanup tracked as deferred #1/#2.

Database access: control-plane SQLite `effnine-host/data/effnine.db` is single authoritative. MyContext accesses read-only (`:ro`, `mode=ro&_query_only=true`). Do not make writable to simplify.

Encryption: production `XChaCha20-Poly1305`, `MYCENC1||nonce||ciphertext`, deterministic nonce `SHA256(mycontext-nonce-v1:object:key:id)`, key host-only `.mycontext_key` 0600 at `/run/secrets/mycontext_key:ro`, `ENC_STR_PREFIX enc:v1:<base64 nonce+ciphertext>` for meta, `meta/encryption.json`, `meta/keycheck enc:v1:...`, `is_encrypted` fail-closed if `Mode::Production && crypto=None`. Key never committed/embedded/API/log/frontend. Dev exceptions clearly separated.

---

## 8. Conductor Security Model (locked)

Authenticated/authorized AI gateway preserving: `service authentication (TokenStore.Verify SHA256, conductor service, revoked/expired)`, `scope authorization (RequireScope 403)`, `provider credential isolation (env only, Redacted(), never to client)`, `SSRF protection (ValidateProviderURL + DialContext private-IP + CheckRedirect)`, `request limits (BodyLimit 256 KiB control-plane, 16 MiB mycontext, 10 MiB conductor-worker)`, `security headers (HSTS etc.)`, `CORS allowlist (https://effnine.dev, Vary: Origin, never *)`. Provider keys via env → `SSRFProtectedTransport` → vendor HTTPS only.

---

## 9. SSRF Protection (locked)

Do not weaken. Protected: `localhost`, `loopback`, `private IPv4 (10/8, 172.16/12, 192.168/16)`, `private IPv6`, `link-local (169.254/16, fe80::/10)`, `multicast`, `CGNAT 100.64/10`, `TEST-NET`, `metadata 169.254.169.254`, `ULA fc00::/7`, `numeric IP tricks (hex 0x7f, octal, decimal 2130706433)`, `unsafe DNS resolutions (DialContext re-resolution)`, `public→private redirects`. `allowPrivate` only for `ollama`/`lmstudio` or explicit admin `127.0.0.1` baseURL. Any modification requires new security analysis + tests + approval.

---

## 10. Deferred Items (explicitly not done)

```
1. MyContext legacy auth_tokens cleanup (daemon still has auth_tokens for stdio trust-on-local, migration via control-plane done, local table retained for offline)
2. MCP_API_KEY fallback removal after client migration (Worker still checks env MCP_API_KEY as fallback, track until all mct_ rotated)
3. Finer Conductor scopes (currently 3 coarse: read/execute/admin, finer per-endpoint deferred)
4. Provider key storage beyond protected .env (keys in .env 0600 only, no Vault/KMS yet)
5. Single-PC → VPS migration (compose portable, host copy + DNS change, not yet executed)
6. Long-lived Cloudflare tunnel token lifecycle (token in .env, rotation manual, not yet automated)
7. In-memory rate limiter (current is in-memory token bucket, no Redis/distributed limiter)
```

Do not silently mark completed. Do not treat as redesign reasons unless new evidence changes risk classification.

---

## 11. Change Control

From this point forward, any change affecting these areas requires explicit architecture/security review:

```
authentication, authorization, token format, token storage, session model,
single-admin invariant, service isolation, SSRF, internal verification,
Caddy security, Cloudflare ingress, Docker exposure, secret handling,
database topology, runtime/source separation
```

Small bug fixes preserving architecture may proceed normally. Architectural changes require deliberate approval: provide `current design, proposed design, reason, security impact, operational impact, migration cost, rollback plan` and wait.

Future feature classification: `FEATURE / BUG FIX / SECURITY FIX / OPERATIONAL CHANGE / ARCHITECTURE CHANGE` — only last reopens frozen architecture. Do not reinterpret architecture change as normal feature because it fits in few files. Before changing infrastructure, compare against `CANONICAL_AUTH_MODEL.md`, `CANONICAL_NETWORK_MODEL.md`, `CANONICAL_DATA_FLOW.md`, `OPERATIONS_RUNBOOK_EFFNINE.md`, `FINAL_RUNTIME_PROMOTION_REPORT_2026-08-30.md`. If code and docs disagree: STOP, IDENTIFY DRIFT, smallest deliberate correction.

---

## 12. Operational Invariants

- Source/Git: `source`, `Dockerfiles`, `deployment templates`, `Caddy configuration`, `schemas`, `tests`, `documentation`, `scripts`, `.env.example` are Git-controlled (templates only, never host secrets).
- Host-only: `.env` (0600), `database` (0700/0600), `Cloudflare credentials` (0700/0600), `runtime state`, `backups` (0700), `runtime logs` are host-only, never committed, `effnine-host/.gitignore` safety net + root `.gitignore effnine-host/`.
- Host directory is not second source tree: no `effnine-host/domain/`, `active/`, `src/`.
- Production must fail closed when missing critical secrets (`session secret`, `internal API key`, `encryption key`, `provider credentials` — no insecure auto defaults).
- Backup: `SQLite → encrypted backup (gpg AES256) → remote/offline` (`sqlite3 .dump | gpg`, `tar + gpg` for mycontext, `VACUUM INTO` for effnine.db). Raw tokens never backed up; credential recovery = rotate/reissue.
- Security decision rule: minimize trust/secret exposure/blast radius, fail closed, preserve isolation, minimize infrastructure, ease audit.
- Operational rule: prefer simple/observable/recoverable/documented/repeatable over clever/distributed/automated-heavy/dependency-heavy. Keep small.

---

## 13. Release Baseline & Risks

Current implementation: `READY WITH DOCUMENTED RISKS` (evidence in FINAL_RELEASE_ASSESSMENT_2026-08-30.md § overall + FINAL_RUNTIME_PROMOTION_REPORT §18-24, tests green, real public path via `https://mcp.effnine.dev` + `https://conductor.effnine.dev` via Cloudflare → cloudflared quic → Caddy :80 → apps, revocation via file mount immediate, no regression).

Do not automatically reclassify after unrelated changes. New evidence required.

**LOW-accepted (intentional):**

- L1 — MyContext repo file perms: host `data/mycontext/repo.db` now `0600` after promotion fix (was `0644` pre-promotion, corrected). Content encrypted (`MYCENC1`), repo dir `0700` limits.
- L2 — `effnine.dev` public not via Tunnel/Caddy: `effnine.dev` DNS `A` via Cloudflare Pages (marketing static), not via Tunnel Caddy. So public `effnine.dev/health` returns HTML not `{"service":"effnine-control-plane"}`, `effnine.dev/internal/verify` public returns HTML not `404`. `mcp.effnine.dev` / `conductor.effnine.dev` via Tunnel correctly `404` for `/internal/*`. Documented intentional (marketing vs API split), admin API reachable via internal Docker `caddy-test:80` host `effnine.dev` → 200, and via Tunnel if DNS later `CNAME` to Tunnel.

**INFORMATIONAL/INTENTIONAL:**

- I1 `MYCONTEXT_ALLOW_NON_LOOPBACK=1` opt-in for Docker `0.0.0.0:8787` (warn logged, bare-metal still `127.0.0.1`).
- I2 `auto_https off` for Tunnel (Cloudflare TLS, VPS re-enable with Origin cert).
- I3 `Caddyfile http://` site duplicates prevent `308` redirect loop behind Tunnel.
- I4 `cloudflared user: "0:0"` root to read `0600` host files (VPS alternative: chown to 65532).
- I5 `EncryptionMarker key_source` placeholder always `environment` (not security relevant).
- I6 Coarse scopes, in-memory limiter, WAL, tunnel token rotation, single-PC SPOF, provider keys in `.env` — accepted for private single-owner deployment.

No CRITICAL/HIGH/MEDIUM open per final verification; all prior H/M closed and re-verified.

---

## 14. Test Baseline

Part of frozen baseline. Before modifying security-sensitive code: `run existing tests`. After: `run again`. Never weaken/remove security tests to fix regression.

Executed at freeze:

```
control-plane:  go vet ./... 0, go test ./... -count=1 ok 3.1s (24 tests: TestConcurrentAdminCreateIsSingleton, TestPasswordChangeRevokesAllSessions, TestTokenIsolation, TestCSRF, TestHostHeaderValidation etc.)
mycontext:      cargo check Finished, cargo test --lib 152 passed 0 failed (redact, crypto, repo, storage)
conductor:      go vet ./... 0, go test ./... -count=1 ok 13.6s (44 packages, security SSRF 5 tests, middleware scope 5 tests)
docker:         docker compose config valid (host: 5 Up, infra via .env.example with TUNNEL_TOKEN validates Caddy)
caddy:          caddy validate --config /etc/caddy/Caddyfile Valid (3× Cookie delete, auto_https off)
public smoke:   curl via real DNS/public path: mcp health 200 via Caddy/Tunnel, conductor health 200, mct_ public 200, cond_ public 200, revoke 401, internal 404, headers HSTS etc., logs no Cookie, no raw tokens
```

---

## 15. Git Checkpoint

Tag: `effnine-infra-v1.0.0` (2026-08-30) applied to each relevant Git repository with clean status per `git status`, `tests`, `build`, `deployment configuration validation`.

Repositories checked:

- `active/mycontext` (EffNine/mycontext) — Rust daemon, Dockerfile, production encryption, daemon verify
- `active/conductor` (EffNine/Conductor) — Go gateway, TokenStore, SSRF, scopes
- `domain/effnine` (EffNine/effnine) — static site effnine.dev

Additional source not in separate Git but part of frozen baseline (captured via this lock document + host backup):

- `domain/control-plane` (Go, ensure single .db, not separate git)
- `domain/mcp-worker`, `domain/conductor-worker` (Cloudflare Workers, D1, Host/CORS)
- `domain/shared/control_plane_schema.sql`
- `infrastructure/docker-compose.yml`, `Caddyfile`, `.env.example`, `SECURITY_HARDENING.md`
- `domain/CANONICAL_*.md`, `OPERATIONS_RUNBOOK_EFFNINE.md`, `FINAL_RUNTIME_PROMOTION_REPORT_2026-08-30.md`

Before tagging: `git status` must be clean, `tests` pass, `build` validates, `deployment configuration` validates. Do not tag dirty/broken state. Host `effnine-host` is never a git repo (intentional, .gitignore safety net).

Freeze references: `CANONICAL_AUTH_MODEL.md`, `CANONICAL_NETWORK_MODEL.md`, `CANONICAL_DATA_FLOW.md`, `OPERATIONS_RUNBOOK_EFFNINE.md`, `FINAL_RUNTIME_PROMOTION_REPORT_2026-08-30.md` are part of baseline. If code/docs disagree: STOP and deliberate smallest correction.

---

## 16. Deployment Checklist (before declaring lock complete)

```
✓ architecture documentation exists (domain/CANONICAL_*.md, OPERATIONS_RUNBOOK_EFFNINE.md, FINAL_RUNTIME_PROMOTION_REPORT_2026-08-30.md, FINAL_RELEASE_ASSESSMENT_2026-08-30.md)
✓ canonical auth model exists (CANONICAL_AUTH_MODEL.md 206 lines)
✓ canonical network model exists (CANONICAL_NETWORK_MODEL.md 155 lines)
✓ canonical data-flow model exists (CANONICAL_DATA_FLOW.md 120 lines)
✓ operations runbook exists (OPERATIONS_RUNBOOK_EFFNINE.md 271 lines)
✓ runtime promotion report exists (FINAL_RUNTIME_PROMOTION_REPORT_2026-08-30.md 341 lines)
✓ hosting directory is separate (/home/afnan/projects/effnine-host not git, no domain/active/src copy, .gitignore safety net)
✓ runtime secrets are host-only (.env 0600, .mycontext_key 0600, cloudflared 0600, not in git ls-files)
✓ database is host-only (effnine.db 0600 under effnine-host/data, not in git, WAL 0600)
✓ Cloudflare credentials are host-only (cloudflared 0700/0600 host-only, not in git)
✓ security tests pass (control-plane 24, mycontext 152, conductor 44, caddy validate, compose config)
✓ integration tests pass (public end-to-end mcp/conductor via real DNS, Caddy→apps, revoke, scope, internal 404, headers)
✓ deployment configuration validates (host compose valid, canonical compose via .env.example with TUNNEL_TOKEN, Caddy valid, only Caddy ports)
✓ current runtime remains healthy (5 Up healthy, cloudflared quic 4 conns, control-plane/mycontext/conductor healthy, Caddy/Tunnel ok, no raw secrets in logs)
```

Do not modify unrelated application code during lock process.

---

## 17. Lock Marker

This file **IS** the lock marker.

If any invariant cannot be verified, final status must be `LOCK FAILED` not `LOCKED`.

---

*End of ARCHITECTURE_LOCK.md — treat as frozen. Future work: PRESERVE → EXTEND → TEST → DOCUMENT, not REWRITE → REPLACE → REDESIGN.*
