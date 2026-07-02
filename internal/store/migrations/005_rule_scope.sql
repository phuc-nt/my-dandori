-- Guardrail rules can target one agent or project instead of the whole fleet
-- (a struggling agent gets tightened without punishing everyone).
ALTER TABLE guardrail_rules ADD COLUMN scope_type TEXT NOT NULL DEFAULT 'global';
ALTER TABLE guardrail_rules ADD COLUMN scope_id TEXT NOT NULL DEFAULT '';
