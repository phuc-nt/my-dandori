-- v13 Kit & Mining: ONE migration for the whole sprint (P1 ships it, later
-- phases consume tables already present — see plan.md "Locked decisions").
-- Plain ADD COLUMN, no CHECK (015/016 precedent: modernc ALTER/CHECK edge
-- cases untested on old DBs).

-- origin badge follows a knowledge_units row end-to-end: human (default,
-- v12 behavior) | import-* | ai-draft | detector. Existing rows default
-- origin='human' — NO BACKFILL (v13 default 8.7): a pre-v13 row was either
-- human-nominated or detector-nominated indistinguishably in the old schema,
-- and 'human' is the safe/conservative label since it carries no special
-- "AI wrote this" implication downstream.
ALTER TABLE knowledge_units ADD COLUMN origin TEXT NOT NULL DEFAULT 'human';
ALTER TABLE knowledge_units ADD COLUMN origin_model TEXT;  -- ai-draft only

-- Mining dismiss (M2): reading-list-only per-run suppression for the
-- /knowledge/mining tab. NEVER written to the append-only audit chain, and
-- carries ZERO governance-suppression power — a dismissed run stays fully
-- visible in run detail, audit views, and every other surface. This table
-- exists purely to declutter one reading list.
CREATE TABLE mining_dismissals (
    run_id TEXT PRIMARY KEY,
    actor  TEXT,
    reason TEXT,
    at     TEXT
);

-- Kit per-file bodies (consumed by P4): a kind=kit knowledge_units row's
-- manifest lives in the unit's body; each bundled file's own content lives
-- here so review can show full per-file bodies and the applier/puller can
-- verify per-file hashes against the pinned manifest (Merkle-lite).
CREATE TABLE knowledge_kit_files (
    unit_id      INTEGER NOT NULL REFERENCES knowledge_units(id),
    path         TEXT NOT NULL,
    body         TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    size         INTEGER NOT NULL,
    PRIMARY KEY (unit_id, path)
);
