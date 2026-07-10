# Audit checkpoints

This directory holds signed checkpoints of the tamper-evident audit chain
(`audit_log` in the SQLite database). Each file is named `<tip_id>.json`
and contains:

```json
{
  "tip_id": 100,
  "tip_hash": "<hex sha256>",
  "ts": "<RFC3339 UTC>",
  "first_signed_id": 1,
  "key_id": 1,
  "signature": "<base64 ed25519 signature over tip_id+tip_hash+ts+first_signed_id>"
}
```

`tip_id` is the id of the row the checkpoint was taken at (not a row count —
ids can have gaps). `first_signed_id` is the id of the first audit row that
was ever signed, embedded in the signed payload itself so it survives even if
the in-database `audit_first_signed_id` setting is deleted.

## Why this directory exists

The per-row hash chain in `audit_log` proves a row wasn't edited in place,
and the per-row Ed25519 signature proves a row wasn't forged with a
different key. Neither one proves the chain wasn't **rebuilt from scratch**
or **truncated** (tail rows deleted) — both of those operations happen
entirely inside the database the operator already controls, so nothing
inside the database can catch them by itself.

A checkpoint's protection comes from two independent things, in this order
of actual strength:

1. **Its own signature is verified before it is trusted for anything.**
   `dandori audit verify` loads the configured public key and calls the
   checkpoint's `VerifySignature` — a checkpoint file that doesn't verify
   (forged, corrupted, signed by an unrecognized key) is rejected outright as
   tampering (reason `checkpoint`), never silently treated as "no checkpoint
   present". This is what actually raises the bar from "can write a file in
   this directory" to "must possess the Ed25519 private key".
2. **Best-effort git commit** of every checkpoint written here, plus the
   optional `DANDORI_AUDIT_CHECKPOINT_OFFSITE` copy. This is convenience
   automation, not the security boundary — on a single box where the signing
   key and this git repo both live, an attacker with key + box access can
   forge a new checkpoint, re-sign it correctly, and commit over the old one.
   The actual defense against a fully-compromised box is a copy of the
   checkpoint living somewhere that attacker cannot reach: a remote git
   `push` (not just a local commit), or the offsite path pointed at storage
   under separate control.

`dandori audit verify` compares the current chain's tip id and tip hash
against the latest *valid* checkpoint in this directory. A chain shorter
than, or diverging from, the latest checkpoint fails verification with
reason `truncated`.

## Key-required mode

If a signing key is configured right now but the chain's tail is unsigned
and no valid checkpoint proves signing was ever properly torn down, `dandori
audit verify` fails with reason `signature` rather than silently reporting
intact. This closes the window before the first checkpoint would otherwise
exist: a checkpoint is now written immediately the moment signing is first
enabled (at the first signed row), not only after 100 rows or at export
time, so there is no unanchored gap for a strip-signatures-and-delete-marker
rebuild to hide in.

## Operational notes

- Checkpoints are only written when `DANDORI_AUDIT_SIGNING_KEY` is set — an
  unsigned installation has nothing to sign a checkpoint with.
- Written immediately when signing is first enabled (the first signed row),
  automatically every 100 audit rows after that, and on every compliance
  export.
- `DANDORI_AUDIT_CHECKPOINT_DIR` overrides this directory's path.
- `DANDORI_AUDIT_CHECKPOINT_OFFSITE` additionally copies every checkpoint to
  another path (e.g. a separately-backed-up mount) for operators who want an
  anchor outside this git repo too.
- Do not delete old checkpoint files — `LatestCheckpoint` picks the
  highest `tip_id`, but keeping history lets an auditor see the
  chain's growth over time and makes any gap in the sequence visible.
