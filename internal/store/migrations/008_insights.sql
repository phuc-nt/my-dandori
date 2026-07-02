-- Master Observer insights: fleet-level executive signals, distinct from
-- run-level flags. approval_id is nullable and set AFTER the approval row
-- exists — an insight never references a missing approval.
CREATE TABLE insights (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    type        TEXT NOT NULL,               -- budget_overshoot_trend | agent_underused | ...
    subject     TEXT NOT NULL,               -- agent/operator/team/project id
    summary     TEXT NOT NULL,               -- plain-language (VI), redacted
    evidence    TEXT NOT NULL DEFAULT '{}',  -- JSON: metric values, source ids, structured action params
    class       TEXT NOT NULL,               -- auto | approval
    surface     TEXT NOT NULL DEFAULT 'ceo', -- ceo | operator
    status      TEXT NOT NULL DEFAULT 'open',-- open | surfaced | resolved | dismissed
    approval_id INTEGER,
    created_at  TEXT NOT NULL,
    resolved_at TEXT
);
CREATE INDEX idx_insights_open ON insights(type, subject) WHERE status IN ('open', 'surfaced');
CREATE INDEX idx_insights_surface ON insights(surface, status, created_at);
