-- 0001 baseline: the schema AutoMigrate produced before versioning.
-- Down drops every table this release owns (newest first); it is destructive
-- by design and only used by `migrate down` in development.
DROP TABLE IF EXISTS shadow_observations;
DROP TABLE IF EXISTS task_tool_calls;
DROP TABLE IF EXISTS task_events;
DROP TABLE IF EXISTS task_steps;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS request_attempts;
DROP TABLE IF EXISTS routing_traces;
DROP TABLE IF EXISTS model_probe_meta;
DROP TABLE IF EXISTS model_status_records;
DROP TABLE IF EXISTS cost_rates;
DROP TABLE IF EXISTS provider_health_records;
DROP TABLE IF EXISTS request_logs;
DROP TABLE IF EXISTS usage_records;
