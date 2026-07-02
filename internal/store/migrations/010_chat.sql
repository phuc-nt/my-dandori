-- CEO chatbot: sessions, messages, and a tool-call audit log. Single-user
-- MVP — sessions are keyed by principal + day. Messages and tool args are
-- stored redacted.
CREATE TABLE chat_sessions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    principal   TEXT NOT NULL,
    day         TEXT NOT NULL,                -- YYYY-MM-DD, budget window
    tokens_used INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    UNIQUE (principal, day)
);

CREATE TABLE chat_messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES chat_sessions(id),
    role       TEXT NOT NULL,                 -- user | assistant
    content    TEXT NOT NULL,                 -- redacted
    created_at TEXT NOT NULL
);
CREATE INDEX idx_chat_messages_session ON chat_messages(session_id, id);

CREATE TABLE tool_audits (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  INTEGER NOT NULL REFERENCES chat_sessions(id),
    tool        TEXT NOT NULL,
    args        TEXT NOT NULL,                -- redacted JSON
    result      TEXT NOT NULL,                -- redacted, truncated
    approval_id INTEGER,                      -- set when an action tool created a request
    ts          TEXT NOT NULL
);
CREATE INDEX idx_tool_audits_session ON tool_audits(session_id, ts);
