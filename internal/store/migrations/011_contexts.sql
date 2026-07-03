-- Context Hub: layered org context docs (company/team/agent) with immutable
-- full-snapshot versions and an explicit head pointer. Additive — v4 DBs
-- upgrade cleanly. Full snapshots are correct at this scale (tens of docs,
-- KBs each); no delta storage.

CREATE TABLE contexts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    layer      TEXT NOT NULL CHECK (layer IN ('company','team','agent')),
    target_id  TEXT NOT NULL,            -- '*' for company; teams.id / agents.id otherwise
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(layer, target_id)
);

CREATE TABLE context_versions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    context_id  INTEGER NOT NULL REFERENCES contexts(id) ON DELETE CASCADE,
    version_n   INTEGER NOT NULL,        -- 1,2,3… monotonic per context
    content     TEXT NOT NULL,           -- full markdown snapshot
    author      TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    UNIQUE(context_id, version_n)
);
CREATE INDEX idx_ctx_versions ON context_versions(context_id, version_n DESC);

CREATE TABLE context_heads (
    context_id INTEGER PRIMARY KEY REFERENCES contexts(id) ON DELETE CASCADE,
    version_id INTEGER NOT NULL REFERENCES context_versions(id)
);
