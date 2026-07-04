-- Console-launched runs (v6 Commander): track the OS process so the console
-- can stream, kill (by process group), reconcile orphans, and link retries.
-- All additive; hook/wrap runs simply leave these NULL.
ALTER TABLE runs ADD COLUMN pid INTEGER;
ALTER TABLE runs ADD COLUMN launched_by TEXT;
ALTER TABLE runs ADD COLUMN exit_code INTEGER;
ALTER TABLE runs ADD COLUMN retry_of TEXT;   -- lineage: the run this was retried from
