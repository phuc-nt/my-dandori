-- Central mode foundation: human operator identity, teams, and event
-- idempotency for spool replay. All additive — local single-machine mode
-- keeps working with these columns NULL.

CREATE TABLE operators (
    id         TEXT PRIMARY KEY,          -- principal, e.g. alice@dev-laptop
    display    TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE teams (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT UNIQUE NOT NULL,
    created_at TEXT NOT NULL
);

-- member_id references operators.id or agents.id depending on member_type.
-- SQLite cannot express a polymorphic FK; the relation is enforced in app code.
CREATE TABLE team_members (
    team_id     INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    member_type TEXT NOT NULL CHECK (member_type IN ('operator', 'agent')),
    member_id   TEXT NOT NULL,
    added_at    TEXT NOT NULL,
    PRIMARY KEY (team_id, member_type, member_id)
);

-- Plain TEXT, no ALTER-added FK (SQLite ALTER FKs are advisory only);
-- relation enforced in app code, index for the aggregate queries.
ALTER TABLE runs ADD COLUMN operator_id TEXT;
CREATE INDEX idx_runs_operator ON runs(operator_id, started_at);

-- Client-generated idempotency key: spool replay after a lost response must
-- not double-count events.
ALTER TABLE events ADD COLUMN ulid TEXT;
CREATE UNIQUE INDEX idx_events_ulid ON events(ulid) WHERE ulid IS NOT NULL;
