-- Server-derived dedup guard for central guardrail-decision audit rows.
-- Keyed on a content hash of (run_id, action, detail) rather than the
-- client-minted ULID: a client that precomputes a ULID and POSTs a benign
-- event first, then POSTs the real deny under the SAME ULID, must not have
-- the real deny silently dropped by the events.ulid ON CONFLICT DO NOTHING
-- path. content_hash is the dedup key that actually matters for audit
-- integrity; ulid is kept only for diagnostics (which wire record triggered
-- this append).
CREATE TABLE audited_events (
    content_hash TEXT PRIMARY KEY,
    ulid         TEXT,
    audit_id     INTEGER NOT NULL,
    created_at   TEXT NOT NULL
);
