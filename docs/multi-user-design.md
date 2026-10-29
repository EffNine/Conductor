# Multi-user Conductor — design proposal

- **Status:** Accepted; slices 1–3 implemented 2026-10-30, slice 4 pending
  (quotas, per-user rate limit, model allowlist)
- **Scope:** authentication, attribution, authorization and quotas for more than one
  caller on a single Conductor instance.
- **Non-goal:** multi-tenant isolation (separate databases, processes or trust
  boundaries). Conductor stays a single-operator gateway; "multi-user" means the
  operator's people and agents, not untrusted customers.

## 1. What exists today

| Concern | Current state |
|---|---|
| Identity | one static gateway key (admin-equivalent), or a scoped `service_token` read from the EffNine control-plane DB |
| Scopes | `conductor:read` / `conductor:execute` / `conductor:admin` enforced with `RequireScope` |
| Attribution | none — no user/tenant column on `usage_records`, `request_logs`, `tasks`, `routing_traces`, `request_attempts` |
| Rate limiting | one global in-memory counter for the whole process |
| Quotas / spend caps | none |
| Admin surface | none (configuration is files/env) |

The token store is read-only against `service_token` (`service`, `scopes`, `token_hash`,
`expires_at`, `revoked_at`). It has no user identity, and Conductor cannot create or revoke
tokens there.

## 2. Requirements

**Must**
- Each person/agent authenticates with its own credential.
- Every request, task, trace and usage row is attributable to a user — from the
  authenticated principal, never from a client header.
- The operator can create, list and revoke credentials without restarting the process.
- Per-user request rate and spend/token budgets, enforced before work starts.
- The existing single key keeps working (mapped to a `default` admin user).
- No prompts or responses are stored (unchanged invariant).

**Should**
- Per-user model/provider allowlist (e.g. local-only for some users).
- Key rotation with overlap; last-used timestamps.
- Audit trail for key and quota changes.
- Admin can read all users' usage; a member sees only their own.

**Could**
- Federate identity from EffNine authority or an OIDC provider.
- Per-user provider credentials (BYOK).
- Team/org grouping above users.

**Won't (this design)**
- Untrusted multi-tenant isolation, per-user databases, billing/payment collection.

## 3. Identity options

| Option | Pros | Cons | Verdict |
|---|---|---|---|
| **A. Conductor-owned API keys** (`users` + `api_keys` in the gateway DB) | self-contained, offline, reuses the existing hash/scope/revocation pattern; no coupling | another credential store to operate; key distribution is manual | **chosen for the first slice** |
| **B. EffNine authority tokens** (extend `service_token` with user identity) | one identity source for the estate; central revocation | Conductor is read-only there; requires an EffNine schema/API change and a deployment dependency; the control plane currently models *services*, not people | later, behind the same `Authenticator` seam |
| **C. OIDC / JWT** | standard, SSO, no key distribution | new trust configuration, token validation surface, no offline use | defer |
| **D. Do nothing** | zero work | no attribution, no per-user limits, one leaked key compromises everyone | rejected |

The chosen shape is an `Authenticator` interface with two implementations from day one:
`StaticKey` (today's key → default admin) and `LocalKeys` (users/keys). An EffNine or OIDC
authenticator can be added later without touching handlers.

## 4. Data model (migration `0002_multi_user`)

```sql
CREATE TABLE users (
  id           TEXT PRIMARY KEY,
  label        TEXT NOT NULL,
  role         TEXT NOT NULL CHECK (role IN ('admin','operator','member','viewer')),
  disabled_at  DATETIME,
  created_at   DATETIME NOT NULL,
  -- quotas; NULL = inherit the gateway default
  max_requests_per_minute INTEGER,
  max_tokens_per_month    INTEGER,
  max_spend_usd_per_month REAL,
  model_allowlist         TEXT      -- comma-separated; NULL = all models
);

CREATE TABLE api_keys (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id),
  token_hash   TEXT NOT NULL UNIQUE,   -- SHA-256, never the raw key
  prefix       TEXT NOT NULL,          -- first 8 chars for display
  scopes       TEXT NOT NULL,          -- JSON array
  created_at   DATETIME NOT NULL,
  last_used_at DATETIME,
  expires_at   DATETIME,
  revoked_at   DATETIME
);
CREATE INDEX api_keys_user_idx ON api_keys(user_id);

ALTER TABLE usage_records     ADD COLUMN user_id TEXT;
ALTER TABLE request_logs      ADD COLUMN user_id TEXT;
ALTER TABLE tasks             ADD COLUMN user_id TEXT;
ALTER TABLE routing_traces    ADD COLUMN user_id TEXT;
ALTER TABLE request_attempts  ADD COLUMN user_id TEXT;

CREATE INDEX usage_records_user_time_idx ON usage_records(user_id, created_at);
CREATE INDEX tasks_user_idx ON tasks(user_id);
```

`0002` also inserts the `default` admin user. Every pre-existing row is backfilled to
`default` (one `UPDATE ... WHERE user_id IS NULL` per table), so the single-key deployment
keeps its history under a real principal rather than `NULL`.

The embedded baseline (`0001`) stays untouched; `0002` is the first real versioned change and
demonstrates the migration path.

## 5. Principal and enforcement

```go
type Principal struct {
    UserID   string
    KeyID    string
    Scopes   []string
    Role     string
    Limits   Limits
}
```

`Auth` resolves the bearer token to a `Principal` and stores it in `c.Locals("principal")`.
The static key resolves to `default`/admin. `RequireScope` keeps working; a new helper
`CurrentPrincipal(c)` is the only way handlers read identity.

| Surface | Enforcement |
|---|---|
| `POST /v1/chat/completions`, `/v1/embeddings` | quota check before dispatch (`execute` scope) |
| `POST /api/tasks` + retry/resume/cancel | quota check; task row stamped `user_id`; only the owner or an admin may act |
| `GET /api/usage`, `/api/usage/costs`, `/api/logs` | filter `user_id = principal` unless admin/operator |
| `GET /api/routing/traces`, `/api/tasks` | same filter |
| Model selection | if `model_allowlist` is set and the requested model is not in it → 403 `model_not_allowed` |
| Rate limit | per principal; the global counter remains as a backstop |
| Admin API | `conductor:admin` only |

**Quotas** are computed from `usage_records` (tokens/spend over the calendar month) and a
per-minute counter for requests. The check runs before dispatch; on accounting failure the
gateway fails **closed** for `execute` and **open** for read, so a broken meter cannot let
spend through but also cannot black out the dashboard.

**Admin API (new, all `conductor:admin`):**

```
POST   /api/users                     create (label, role, quotas)
GET    /api/users                     list with usage summary
PATCH  /api/users/:id                 role/quota/disable
POST   /api/users/:id/keys            create a key (raw shown once)
DELETE /api/api-keys/:id              revoke
GET    /api/api-keys?user_id=         list prefixes/last-used only
```

Every key/quota mutation writes an audit event (reuse `request_logs` with a dedicated
`path`, or a small `admin_audit` table — decision deferred to implementation).

## 6. Compatibility and rollout

1. **Slice 1 (no behaviour change):** `0002`, `Principal`, `LocalKeys` + `StaticKey`,
   `default` user backfill. Single-key still works; nothing is filtered yet.
2. **Slice 2:** stamp `user_id` on write paths; filter reads for non-admin principals.
3. **Slice 3:** admin API + key lifecycle.
4. **Slice 4:** per-user rate/token/spend quotas and model allowlists.
5. **Optional:** EffNine or OIDC authenticator behind the same interface.

Config gains `auth.mode: single | multi` (default `single`). `multi` refuses to start with no
enabled user, mirroring the fail-closed posture of the rest of the gateway. Rolling back to
`single` never requires a schema rollback: the columns stay, the static key keeps working.

## 7. Risks

| Risk | Mitigation |
|---|---|
| Spoofed attribution | `user_id` only from the authenticated principal; never from a request header |
| Quota bypass via concurrent requests | check-and-reserve in one SQL statement per user, or a per-user in-memory limiter plus a reconciled monthly total |
| Admin overreach | admins see usage metadata, never prompts (not stored); admin actions are audited |
| SQLite write contention | quotas are read-mostly; the existing single-writer model already serialises usage writes |
| Key leakage | only hashes stored; raw key shown once; prefix for identification; revocation immediate |
| Migration on a live DB | additive nullable columns + backfill; baseline untouched; `0002` is reversible in dev only (down drops the columns/tables) |
| EffNine coupling (option B) | not taken now; identity seam keeps it optional |

## 8. Open decisions

1. **Identity source:** local keys (A) first, or EffNine authority (B) as the source of truth?
2. **Trust level:** trusted team (attribution + budgets) or untrusted users (needs per-user
   isolation, not just filters)?
3. **Provider keys:** shared operator keys with quotas, or per-user BYOK?
4. **Quota windows:** requests/minute, tokens/month, spend/month — are these the right axes,
   and is a monthly reset acceptable?

Answers to (1)–(3) determine whether slice 1 is a few hundred lines or a subsystem.

## 9. Implementation status (2026-10-30)

| Slice | State |
|---|---|
| 1 — schema, principal, authenticator, default user | **done** (`0002_multi_user`, `auth.KeyStore`, `AuthenticatePrincipal`) |
| 2 — attribution and filtered reads | **done for usage, logs and tasks**; `routing_traces.user_id` and `request_attempts.user_id` exist but are not populated yet (the router/executor do not carry the principal) |
| 3 — admin API | **done** (`/api/users`, `/api/users/:id`, `/api/users/:id/keys`, `/api/api-keys`) |
| 4 — quotas, per-user rate limit, model allowlist | **not implemented** |

Operational notes:

- `auth.mode` is not needed: local keys are additive. The static key maps to `default`
  (admin); the token store maps to `svc:<fingerprint>` (member).
- Create the first non-default user with `POST /api/users` using the static key, then issue
  that user a key with `POST /api/users/{id}/keys`.
- The raw key is returned once and never stored; only its SHA-256 hash is persisted.
