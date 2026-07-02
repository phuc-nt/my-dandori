-- Multi-runtime capture + per-run git delta (attribution & revert detection).
ALTER TABLE runs ADD COLUMN runtime TEXT NOT NULL DEFAULT 'claude-code';
ALTER TABLE runs ADD COLUMN lines_added INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN lines_deleted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN head_before TEXT;
ALTER TABLE runs ADD COLUMN head_after TEXT;
