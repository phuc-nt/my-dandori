-- Adds optional Ed25519 co-signature columns to the audit hash chain. Both
-- are nullable so existing (pre-signing) rows stay valid and unsigned —
-- signing is enabled by setting DANDORI_AUDIT_SIGNING_KEY, not by this
-- migration. key_id lets a future key rotation identify which public key
-- verifies a given row.
ALTER TABLE audit_log ADD COLUMN signature BLOB;
ALTER TABLE audit_log ADD COLUMN key_id INTEGER;
