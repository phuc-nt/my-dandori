-- UB4: per-check quality-gate override. A failed gate_results row can be
-- overridden by a human with a mandatory justification — never blanket,
-- never silent. Columns stay NULL until overridden; override rows are
-- immutable (a re-override, if ever needed, would be a fresh row via a new
-- gate_results insert, never an update-in-place of an already-overridden row).
ALTER TABLE gate_results ADD COLUMN overridden_at TEXT;
ALTER TABLE gate_results ADD COLUMN overridden_by TEXT;
ALTER TABLE gate_results ADD COLUMN override_reason TEXT;
