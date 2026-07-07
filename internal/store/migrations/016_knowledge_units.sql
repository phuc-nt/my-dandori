-- Knowledge Units envelope (v12): ONE pipeline table (state machine + review
-- + adoption + provenance) wrapping the existing distribution rails for
-- context/rule/playbook (ref-not-duplicate) and a new body-owning surface for
-- skill. kind/state validated in app code, NOT via CHECK (015 precedent:
-- modernc ALTER/CHECK edge cases untested on old DBs).
CREATE TABLE knowledge_units (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT NOT NULL,          -- context|rule|playbook|skill
    name          TEXT NOT NULL,          -- slug ^[a-z0-9][a-z0-9-]*$ ; path/compliance/match key
    title         TEXT NOT NULL,          -- free-text display
    state         TEXT NOT NULL DEFAULT 'detected',
    version_n     INTEGER NOT NULL DEFAULT 1,   -- monotonic per (kind,name)
    supersedes_id INTEGER REFERENCES knowledge_units(id),  -- this unit replaces an older one
    -- ref-not-duplicate: content lives in existing store for 3/4 kinds
    ref_kind      TEXT,                   -- context_version|guardrail_rule|playbook (NULL for skill)
    ref_id        INTEGER,                -- id in the corresponding store
    -- skill-only: body lives here (new surface), IMMUTABLE after approve
    body          TEXT,                   -- full SKILL.md content (skill kind)
    content_hash  TEXT,                   -- sha256(body) hex (skill kind) — pin for pull
    layer         TEXT,                   -- company|team|agent target on publish (context/skill)
    layer_target  TEXT,                   -- '*' | team_id | agent_id
    required      INTEGER NOT NULL DEFAULT 0,  -- mandate flag (admin-approved via P6)
    -- stats snapshot @ nominate (audit/gate only — suggest card recomputes live)
    n_present     INTEGER, n_absent INTEGER,
    done_present  REAL,    done_absent REAL,
    ci_present_lo INTEGER, ci_present_hi INTEGER,
    ci_absent_lo  INTEGER, ci_absent_hi  INTEGER,
    cost_present  REAL,    cost_absent  REAL,
    provenance_run_ids TEXT,             -- JSON array run IDs
    nominated_by  TEXT,                   -- 'dandori-observer' | operator_id (engineer-nominated)
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE INDEX idx_ku_state ON knowledge_units(state, kind);
CREATE INDEX idx_ku_kind_ref ON knowledge_units(kind, ref_kind, ref_id);
-- name UNIQUE per kind for the PUBLISHED head only (one live published slug
-- per kind). nominated/in_review are deliberately OUTSIDE this index: a v2
-- draft may exist and be reviewed while v1 stays published (F5 — supersede
-- happens at APPLIER time when knowledge-publish is approved, not at nominate
-- time). NominateUnit itself dedups drafts in app code (reject a second
-- nominate while one draft is already nominated/in_review for the same
-- kind+name) so this index only needs to guard the published head.
CREATE UNIQUE INDEX idx_ku_kind_name_live ON knowledge_units(kind, name)
    WHERE state IN ('published','adopted','measured');
-- Second partial unique index for the draft states (M1): NominateUnit's app-
-- code dedup (SELECT-then-INSERT above) has a TOCTOU race window between two
-- concurrent nominates for the same (kind,name) — this index is the actual
-- guarantee, closing that race at the DB layer. A racing second INSERT fails
-- here and NominateUnit maps the UNIQUE violation to the same friendly
-- "already pending review" error the pre-check produces.
CREATE UNIQUE INDEX idx_ku_kind_name_draft ON knowledge_units(kind, name)
    WHERE state IN ('nominated','in_review');

CREATE TABLE knowledge_transitions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    unit_id    INTEGER NOT NULL REFERENCES knowledge_units(id),
    from_state TEXT NOT NULL, to_state TEXT NOT NULL,
    actor      TEXT NOT NULL, note TEXT, at TEXT NOT NULL
);
CREATE INDEX idx_ku_trans ON knowledge_transitions(unit_id, at);

-- M3: skillreg.ApproveHash and the H1 lineage-aware rewrite both filter
-- audit_log on (action, subject) then ORDER BY id DESC — this is now called
-- from the SessionStart hot path (per mandated skill actually found stale),
-- so it needs an index rather than a full table scan on every agent turn.
CREATE INDEX idx_audit_log_action_subject ON audit_log(action, subject, id);

-- Rebuild adoptions (SQLite cannot drop NOT NULL): add unit_id + installed,
-- relax playbook_id. adoptions is CHILD-ONLY (no table REFERENCES it) so the
-- rebuild is safe with foreign_keys ON. PRAGMA foreign_keys is a NO-OP inside
-- a transaction (every migration runs in one) — deliberately not used here;
-- correctness comes from adoptions having no incoming FK, not from toggling
-- the pragma.
CREATE TABLE adoptions_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    playbook_id   INTEGER REFERENCES playbooks(id),      -- relaxed: NOT NULL removed
    unit_id       INTEGER REFERENCES knowledge_units(id),-- any kind
    installed     INTEGER NOT NULL DEFAULT 1,            -- skill installed(pull) vs suggest-only
    operator_id   TEXT,
    run_id        TEXT REFERENCES runs(id),
    adopted_at    TEXT NOT NULL,
    metric_before REAL, metric_after REAL, computed_at TEXT
);
INSERT INTO adoptions_new(id, playbook_id, operator_id, run_id, adopted_at, metric_before, metric_after, computed_at)
    SELECT id, playbook_id, operator_id, run_id, adopted_at, metric_before, metric_after, computed_at FROM adoptions;
DROP TABLE adoptions;
ALTER TABLE adoptions_new RENAME TO adoptions;
CREATE INDEX idx_adoptions_playbook ON adoptions(playbook_id, adopted_at);
CREATE INDEX idx_adoptions_unit ON adoptions(unit_id, adopted_at);
