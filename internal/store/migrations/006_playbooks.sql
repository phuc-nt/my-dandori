-- Playbooks: the LEARN flywheel — a good run packaged into a reusable
-- starting kit (prompt, files, model, cost norm) so the knowledge stays
-- when the person (or agent) moves on.
CREATE TABLE playbooks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    run_id     TEXT REFERENCES runs(id),
    agent_id   TEXT,
    task_key   TEXT,
    prompt     TEXT,
    model      TEXT,
    cost_usd   REAL,
    top_files  TEXT, -- JSON array of most-touched files
    notes      TEXT,
    created_at TEXT NOT NULL,
    created_by TEXT
);
