CREATE TABLE agents (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    runtime    TEXT NOT NULL DEFAULT 'claude-code',
    created_at TEXT NOT NULL
);

CREATE TABLE runs (
    id                 TEXT PRIMARY KEY,
    session_id         TEXT UNIQUE,
    agent_id           TEXT REFERENCES agents(id),
    project            TEXT,
    task_key           TEXT,
    cwd                TEXT,
    transcript_path    TEXT,
    model              TEXT,
    status             TEXT NOT NULL DEFAULT 'running',
    started_at         TEXT,
    ended_at           TEXT,
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd           REAL NOT NULL DEFAULT 0,
    source             TEXT NOT NULL DEFAULT 'hook'
);
CREATE INDEX idx_runs_agent ON runs(agent_id, started_at);
CREATE INDEX idx_runs_project ON runs(project, started_at);

CREATE TABLE events (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id    TEXT REFERENCES runs(id),
    ts        TEXT NOT NULL,
    kind      TEXT NOT NULL,
    tool_name TEXT,
    ok        INTEGER,
    payload   TEXT
);
CREATE INDEX idx_events_run ON events(run_id, ts);
CREATE INDEX idx_events_kind ON events(kind, ts);

CREATE TABLE work_items (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    source     TEXT NOT NULL,
    key        TEXT NOT NULL,
    title      TEXT,
    status     TEXT,
    assignee   TEXT,
    is_agent   INTEGER NOT NULL DEFAULT 0,
    project    TEXT,
    updated_at TEXT,
    payload    TEXT,
    UNIQUE (source, key)
);
CREATE INDEX idx_work_items_updated ON work_items(source, updated_at);

CREATE TABLE budgets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    scope_type TEXT NOT NULL,
    scope_id   TEXT NOT NULL DEFAULT '',
    period     TEXT NOT NULL DEFAULT 'month',
    limit_usd  REAL NOT NULL,
    UNIQUE (scope_type, scope_id)
);

CREATE TABLE approvals (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id        TEXT,
    event_id      INTEGER,
    action        TEXT NOT NULL,
    reason        TEXT,
    status        TEXT NOT NULL DEFAULT 'pending',
    requested_at  TEXT NOT NULL,
    decided_at    TEXT,
    decided_by    TEXT,
    decision_note TEXT,
    channel       TEXT NOT NULL DEFAULT 'web',
    slack_ts      TEXT,
    consumed_at   TEXT
);
CREATE INDEX idx_approvals_status ON approvals(status, requested_at);

CREATE TABLE guardrail_rules (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT NOT NULL,
    pattern     TEXT NOT NULL,
    description TEXT,
    enabled     INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE flags (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     TEXT REFERENCES runs(id),
    reason     TEXT,
    status     TEXT NOT NULL DEFAULT 'open',
    jira_key   TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE audit_log (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    ts        TEXT NOT NULL,
    actor     TEXT NOT NULL,
    action    TEXT NOT NULL,
    subject   TEXT,
    detail    TEXT,
    prev_hash TEXT NOT NULL,
    hash      TEXT NOT NULL
);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT
);

CREATE TABLE gate_results (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     TEXT,
    check_name TEXT NOT NULL,
    ok         INTEGER NOT NULL,
    output     TEXT,
    ts         TEXT NOT NULL
);

CREATE TABLE notifications (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    kind    TEXT NOT NULL,
    dedup   TEXT NOT NULL UNIQUE,
    sent_at TEXT NOT NULL,
    detail  TEXT
);
