-- v10 identity + RBAC foundation. Additive only: existing operators rows
-- (machine principals like alice@dev-laptop, auto-created by ResolveOperator)
-- keep working with these columns NULL/default — nothing here breaks v9 local
-- mode. CHECK(role IN ...) intentionally omitted (M2): modernc/SQLite ALTER
-- TABLE ADD COLUMN with CHECK has untested edge cases on old DBs; role is
-- validated in application code (operator add only accepts admin|viewer).
ALTER TABLE operators ADD COLUMN username TEXT;
ALTER TABLE operators ADD COLUMN password_hash TEXT;
ALTER TABLE operators ADD COLUMN role TEXT NOT NULL DEFAULT 'viewer';
ALTER TABLE operators ADD COLUMN disabled_at TEXT;
CREATE UNIQUE INDEX idx_operators_username ON operators(username) WHERE username IS NOT NULL;

CREATE TABLE sessions (
    id               TEXT PRIMARY KEY,          -- base64(crypto/rand 32B), >=256-bit
    operator_id      TEXT NOT NULL REFERENCES operators(id),
    created_at       TEXT NOT NULL,
    last_activity_at TEXT NOT NULL,
    expires_at       TEXT NOT NULL              -- absolute timeout
);
CREATE INDEX idx_sessions_operator ON sessions(operator_id, expires_at);

-- Populated in P3 (per-operator ingest tokens). Schema landed here so P1/P3/P4
-- share one migration and avoid a migration-sequence collision.
CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,               -- SHA-256(full token) hex
    operator_id  TEXT NOT NULL REFERENCES operators(id),
    display_name TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    expires_at   TEXT,                           -- NULL = no expiry (v10 default)
    revoked_at   TEXT,
    UNIQUE(operator_id, display_name)
);
CREATE INDEX idx_api_tokens_operator ON api_tokens(operator_id, revoked_at);
