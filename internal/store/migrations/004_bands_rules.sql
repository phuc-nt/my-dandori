-- Autonomy bands: the consequence layer between LEARN grades and GOVERN
-- gating. supervised = every edit needs approval · gated = default rules ·
-- trusted = gate rules skipped except critical ones.
CREATE TABLE agent_bands (
    agent_id   TEXT PRIMARY KEY REFERENCES agents(id),
    band       TEXT NOT NULL DEFAULT 'gated',
    updated_at TEXT,
    updated_by TEXT,
    reason     TEXT
);

-- critical=1 rules gate even trusted agents (deploy-class actions).
ALTER TABLE guardrail_rules ADD COLUMN critical INTEGER NOT NULL DEFAULT 0;
UPDATE guardrail_rules SET critical = 1
 WHERE kind = 'gate' AND (pattern LIKE '%deploy%' OR pattern LIKE '%terraform%' OR pattern LIKE '%kubectl%');
