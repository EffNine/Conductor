-- 0002 down: removes identity and attribution. Destructive; development only.
DROP INDEX IF EXISTS tasks_user_idx;
DROP INDEX IF EXISTS request_logs_user_idx;
DROP INDEX IF EXISTS usage_records_user_time_idx;

ALTER TABLE request_attempts DROP COLUMN user_id;
ALTER TABLE routing_traces DROP COLUMN user_id;
ALTER TABLE tasks DROP COLUMN user_id;
ALTER TABLE request_logs DROP COLUMN user_id;
ALTER TABLE usage_records DROP COLUMN user_id;

DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
