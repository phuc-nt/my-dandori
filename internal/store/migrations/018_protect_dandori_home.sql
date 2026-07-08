-- Protecting dandori's own config/db from agent tampering: config is
-- re-read on every hook invocation and Bash is not sandboxed, so an agent
-- could otherwise disarm guardrails by editing ~/.dandori/config.yaml or
-- deleting ~/.dandori/dandori.db to trigger the store-open fail path.
-- Matches ~/.dandori/, $HOME/.dandori/, and the expanded absolute-home form.
INSERT INTO guardrail_rules (kind, pattern, description) VALUES
  ('block', '(~|\$HOME|/[Uu]sers/[^/]+|/home/[^/]+)/\.dandori(/|$|[\s;&|])', 'protecting dandori''s own config/db from agent tampering');
