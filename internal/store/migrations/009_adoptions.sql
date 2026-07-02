-- Flywheel adoption tracking: who used a playbook and whether their metrics
-- improved. EXPLICIT signal only (a run created "from" a playbook) — never
-- inferred. Rows are inserted only for playbooks/operators/runs that already
-- exist, so the FKs never fail on seeded databases.
CREATE TABLE adoptions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    playbook_id   INTEGER NOT NULL REFERENCES playbooks(id),
    operator_id   TEXT,
    run_id        TEXT REFERENCES runs(id),
    adopted_at    TEXT NOT NULL,
    metric_before REAL,          -- adopter's done-rate at adoption time
    metric_after  REAL,          -- recomputed after enough subsequent runs
    computed_at   TEXT
);
CREATE INDEX idx_adoptions_playbook ON adoptions(playbook_id, adopted_at);
