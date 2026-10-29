-- 0002 multi-user: identities, keys and per-row attribution.
-- Additive: every column is nullable, the default user absorbs existing rows,
-- and the single static key keeps working as the default admin.

CREATE TABLE users (
    id                      TEXT PRIMARY KEY,
    label                   TEXT NOT NULL,
    role                    TEXT NOT NULL CHECK (role IN ('admin','operator','member','viewer')),
    disabled_at             DATETIME,
    created_at              DATETIME NOT NULL,
    max_requests_per_minute INTEGER,
    max_tokens_per_month    INTEGER,
    max_spend_usd_per_month REAL,
    model_allowlist         TEXT
);

CREATE TABLE api_keys (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id),
    token_hash   TEXT NOT NULL UNIQUE,
    prefix       TEXT NOT NULL,
    scopes       TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    last_used_at DATETIME,
    expires_at   DATETIME,
    revoked_at   DATETIME
);
CREATE INDEX api_keys_user_idx ON api_keys(user_id);

ALTER TABLE usage_records    ADD COLUMN user_id TEXT;
ALTER TABLE request_logs     ADD COLUMN user_id TEXT;
ALTER TABLE tasks            ADD COLUMN user_id TEXT;
ALTER TABLE routing_traces   ADD COLUMN user_id TEXT;
ALTER TABLE request_attempts ADD COLUMN user_id TEXT;

CREATE INDEX usage_records_user_time_idx ON usage_records(user_id, created_at);
CREATE INDEX request_logs_user_idx ON request_logs(user_id);
CREATE INDEX tasks_user_idx ON tasks(user_id);

-- The existing operator becomes a real principal; all pre-existing rows belong
-- to it, so nothing is anonymous and no query changes shape.
INSERT INTO users (id, label, role, created_at)
VALUES ('default', 'Default operator', 'admin', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

UPDATE usage_records    SET user_id = 'default' WHERE user_id IS NULL;
UPDATE request_logs     SET user_id = 'default' WHERE user_id IS NULL;
UPDATE tasks            SET user_id = 'default' WHERE user_id IS NULL;
UPDATE routing_traces   SET user_id = 'default' WHERE user_id IS NULL;
UPDATE request_attempts SET user_id = 'default' WHERE user_id IS NULL;
