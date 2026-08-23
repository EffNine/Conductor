# Changelog

All notable changes to Conductor are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are cut from
Conventional Commits.

## [Unreleased]

### Added

- **Dynamic category-preserving failover** (`routing.dynamic_fallback`, on by default):
  when the primary route and static fallbacks fail, capability-matched alternates
  are drawn from the full catalog (vision→vision, planning→reasoning+tools,
  long-context→fits). Requests only fail when no eligible model can serve them.
- **Bare model auto-resolution** (`routing.auto_resolve_bare_models`, on by default):
  with a single provider configured, bare IDs (`gpt-4o`) work with zero routing config.
- **Embeddings failover parity** with the chat path (retries, breaker gating, fallbacks).
- **Fallback observability**: `conductor_fallback_total{kind,provider}` metric and
  `fallback_engaged` structured logs.
- **Global inbound rate limiting** (`rate_limit.enabled`,
  `rate_limit.global.requests_per_minute`); `/health` exempt.
- Version reporting: `conductor_build_info{version}` and `GET /health` now carry the
  real build version (injected at link time).
- End-to-end smoke tests in CI booting the real binary against mock upstreams.

### Changed

- Repo hygiene: milestone reports moved to `docs/reports/`; process-named tests
  renamed to behavior-based names; `internal/v27` → `internal/integrationtest`.
- `handler.go` and `config.go` split into focused files (no API changes).

### Fixed

- Cloudflare adapter never extracted the account ID from base URLs and
  double-appended `/account/<id>`; both corrected with regression tests.
- User-facing docs aligned with runtime behavior: merged catalog is `GET /api/models`
  (`/v1/models` is virtual-only), env vars documented with their required
  `CONDUCTOR_` prefix, stale `gateway` binary name replaced, probe interval
  corrected to 2h.

### Removed

- Dead `rate_limit.per_provider` configuration (was never wired).
