package govern

import (
	"crypto/ed25519"
	"database/sql"
	"strconv"

	"github.com/phuc-nt/dandori/internal/store"
)

// Verify walks the whole audit_log chain and reports the first broken entry
// id (0 = intact) plus a reason distinguishing independent attack classes:
//
//   - "chain": the in-DB hash chain itself is broken — a row's stored hash
//     does not recompute, or its prev_hash does not match the previous row's
//     hash. Catches editing any single row in place.
//   - "signature": a signed row fails Ed25519 verification against its
//     key_id's public key, OR an unsigned row appears at/after the point
//     signing was known to have started (monotonic signing — once signing
//     starts it can never stop without detection), OR a signing key is
//     currently configured but the chain's tail is unsigned with no valid
//     checkpoint proving otherwise (key-required mode). Catches "forge with
//     a different key", "rebuild the whole table with signature=NULL", and
//     "rebuild + delete the in-DB first_signed_id marker" — the hash chain
//     alone is blind to all three because a full rebuild can produce an
//     internally-consistent hash chain from scratch.
//   - "checkpoint": a checkpoint file exists but its own signature does not
//     verify against the configured key — a forged or corrupted checkpoint.
//     Treated as tampering, never silently downgraded to "no checkpoint".
//   - "truncated": the chain's current tip is behind a validly-signed
//     checkpoint on disk. Catches deleting tail rows — the remaining rows
//     stay hash- and signature-consistent, so only an external reference
//     (the checkpoint, anchored outside the DB) can catch this.
//
// reason == "" means fully intact (id is then 0).
func Verify(st *store.Store) (brokenID int64, reason string, err error) {
	// Read settings and derive keys BEFORE opening the row cursor below:
	// store.Store's write pool is capped at one connection
	// (SetMaxOpenConns(1)), so a second query issued while `rows` is still
	// open would block forever waiting for a connection the open cursor is
	// holding — exactly the deadlock this ordering avoids.
	pubByKeyID := loadVerifyKeys()
	dbFirstSignedID := firstSignedIDValue(st)

	dir, _ := resolveCheckpointDirs()
	cp, cpOK, cpErr := LatestCheckpoint(dir)
	if cpErr != nil {
		return 0, "", cpErr
	}
	cpValid := false
	if cpOK {
		pub, hasPub := pubByKeyID[cp.KeyID]
		if !hasPub || !cp.VerifySignature(pub) {
			// A checkpoint file is present but does not verify: either
			// forged, corrupted, or signed by a key we no longer/never
			// trusted. This is worse than "no checkpoint" — silently
			// falling back to unanchored chain-only verification would
			// let an attacker defeat the checkpoint by simply replacing it
			// with garbage, so an invalid checkpoint is tampering, full stop.
			return cp.TipID, "checkpoint", nil
		}
		cpValid = true
	}

	// The monotonic-signing floor is the higher of the in-DB marker and
	// whatever the latest VALID checkpoint attests to. This is what stops
	// "delete the in-DB first_signed_id row" from erasing the fact signing
	// was enabled: the last checkpoint taken while signing was on already
	// recorded first_signed_id inside its own signed payload, independent of
	// the database.
	firstSignedID := dbFirstSignedID
	if cpValid && cp.FirstSignedID > firstSignedID {
		firstSignedID = cp.FirstSignedID
	}

	rows, err := st.DB.Query(`SELECT id, ts, actor, action, COALESCE(subject,''), COALESCE(detail,''),
		prev_hash, hash, signature, key_id FROM audit_log ORDER BY id`)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()

	prev := "genesis"
	var lastID int64
	var lastHash string
	var lastRowSigned bool
	for rows.Next() {
		var id int64
		var ts, actor, action, subject, detail, prevHash, hash string
		var sig []byte
		var keyID sql.NullInt64
		if err := rows.Scan(&id, &ts, &actor, &action, &subject, &detail, &prevHash, &hash, &sig, &keyID); err != nil {
			return 0, "", err
		}

		if prevHash != prev || canonicalHash(prevHash, ts, actor, action, subject, detail) != hash {
			return id, "chain", nil
		}

		lastRowSigned = len(sig) > 0
		if lastRowSigned {
			kid := int(keyID.Int64)
			if !verifyRow(pubByKeyID, kid, hash, sig) {
				return id, "signature", nil
			}
		} else if firstSignedID > 0 && id > firstSignedID {
			// Unsigned row after signing was already turned on: either a
			// rebuild that dropped signatures, or a downgrade. Both are the
			// attack monotonic signing exists to catch.
			return id, "signature", nil
		}

		prev = hash
		lastID = id
		lastHash = hash
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}

	// Key-required mode: a signing key is configured RIGHT NOW, meaning
	// whoever runs verify expects signed rows, but the chain's tail is
	// unsigned and nothing (neither the in-DB marker nor a valid checkpoint)
	// proves signing was ever properly torn down. Silently accepting this
	// as "intact" would let a from-scratch rebuild with no signatures at all
	// pass, in the window before the first checkpoint exists (fewer than
	// checkpointEveryN rows, before any export ran).
	if len(pubByKeyID) > 0 && lastID > 0 && !lastRowSigned && firstSignedID == 0 {
		return lastID, "signature", nil
	}

	if brokenID, reason := verifyAgainstCheckpoint(lastID, lastHash, cp, cpValid); reason != "" {
		return brokenID, reason, nil
	}
	return 0, "", nil
}

// verifyAgainstCheckpoint compares the chain's current tip against a
// checkpoint whose signature has ALREADY been verified by the caller
// (cpValid — see Verify). A checkpoint with a GREATER tip id than what
// currently exists, or the SAME id but a different tip_hash, means rows
// were deleted or replaced after that checkpoint was taken — the remaining
// chain can be perfectly hash- and signature-consistent and still fail this
// check. That is exactly why an external checkpoint is needed: deleting
// tail rows is invisible to any check that only looks at the rows still
// present in the database.
func verifyAgainstCheckpoint(lastID int64, lastHash string, cp Checkpoint, cpValid bool) (brokenID int64, reason string) {
	if !cpValid {
		return 0, "" // no checkpoint, or its signature already failed above
	}
	if lastID < cp.TipID {
		return cp.TipID, "truncated"
	}
	if lastID == cp.TipID && lastHash != cp.TipHash {
		return cp.TipID, "truncated"
	}
	return 0, ""
}

// loadVerifyKeys builds the key_id → public key map Verify checks signatures
// against. MVP: a single configured signing key (DefaultKeyID); the map
// shape lets a future key rotation add historical keys without another
// Verify signature change.
func loadVerifyKeys() map[int]ed25519.PublicKey {
	priv, ok := loadSigningKey()
	if !ok {
		return nil
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil
	}
	return map[int]ed25519.PublicKey{DefaultKeyID: pub}
}

// firstSignedIDValue reads the durable first-signed-row marker (0 = signing
// was never turned on for this chain).
func firstSignedIDValue(st *store.Store) int64 {
	v := st.Setting(firstSignedIDSettingKey)
	if v == "" {
		return 0
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// firstSignedIDFromTx is firstSignedIDValue's transaction-scoped twin: it
// must query through the caller's *sql.Tx rather than st.Setting() (which
// would issue its own query on st.DB) — store.Store's write pool is capped at
// one connection (SetMaxOpenConns(1)), so a second query while this
// transaction still holds that connection would deadlock. Safe to call for
// the periodic (non-first-signed) checkpoint branch in AppendTx, since the
// marker row is always committed by an earlier transaction by then.
func firstSignedIDFromTx(tx *sql.Tx) int64 {
	var v string
	_ = tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, firstSignedIDSettingKey).Scan(&v)
	if v == "" {
		return 0
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return id
}
