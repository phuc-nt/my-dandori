-- Default guardrail rules. kind=block → deny outright; kind=gate → require
-- human approval. Patterns are Go regexes matched against Bash commands and
-- file paths touched by Write/Edit tools.
INSERT INTO guardrail_rules (kind, pattern, description) VALUES
  ('block', 'rm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r)[a-zA-Z]*\s+(/|~)', 'recursive force delete at root or home'),
  ('block', '(^|[\s;&|])(cat|less|cp|mv|rm|vim?|nano)\s+[^\s]*\.env(\.[a-z]+)?([\s;&|]|$)', 'reading or moving .env secret files'),
  ('block', 'DROP\s+TABLE|DELETE\s+FROM\s+\S*migrations', 'destructive schema/migration SQL'),
  ('block', 'git\s+push\s+.*--force', 'force push'),
  ('gate',  'git\s+push', 'pushing code needs approval'),
  ('gate',  'gh\s+pr\s+merge', 'merging a PR needs approval'),
  ('gate',  '(^|[\s;&|])(deploy|kubectl\s+apply|terraform\s+apply)', 'deploy-like commands need approval');
