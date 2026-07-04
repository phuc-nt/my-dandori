-- UG3: named, filtered run views. filters_json stores the raw querystring
-- (e.g. "agent=x&status=running") so applying a view is a redirect to
-- /runs?<filters_json> re-parsed through the existing runFilters whitelist —
-- no per-column modeling needed (KISS).
CREATE TABLE saved_views (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    page         TEXT NOT NULL,
    filters_json TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_saved_views_page ON saved_views(page, created_at);
