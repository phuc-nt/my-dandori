package ingest

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"log"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/store"
)

// applyGuardrailAuditTx creates the server-side co-signed audit row for a
// central guardrail decision, inside the caller's batch tx (never a second
// Begin — see applyBatch's doc comment on why that would deadlock). Three
// anti-attack properties enforced here, in order:
//
//  1. Action whitelist (I): rec.Action must be one of govern.AuditActionSet.
//     A client cannot get an arbitrary string into the audit action column —
//     an unrecognized action is logged and the record is skipped, never
//     written verbatim.
//  2. Run-owner check (I): the run's operator_id (set by ensureRunTx from
//     the AUTHENTICATED principal, never the request body) must match this
//     request's operatorID. A client authenticated as operator B spooling a
//     decision for a run owned by operator A is rejected — logged, batch
//     continues (one bad record must not fail the whole batch).
//  3. Server-derived dedup (K): idempotency is NOT keyed on rec.ULID (client-
//     minted, so a client could precompute a ULID, POST a benign event under
//     it first, then POST the real deny under the SAME ULID — the events
//     table's ON CONFLICT(ulid) DO NOTHING would silently absorb the second
//     one). Instead this hashes (run_id, action, detail) and checks
//     audited_events: a real deny always gets its own hash and is never
//     dropped just because some other event reused its ULID. A ULID that
//     arrives twice with a DIFFERENT content hash is not a duplicate at all
//     — it is logged as a suspicious ULID reuse and audited anyway.
func applyGuardrailAuditTx(tx *sql.Tx, audit *govern.Audit, operatorID string, rec *Record) error {
	action := rec.Action
	if !govern.AuditActionSet[action] {
		log.Printf("ingest: guardrail record run=%s tool=%s carried unrecognized action %q — not audited",
			rec.SessionID, rec.Tool, action)
		return nil
	}

	var runOperator sql.NullString
	if err := tx.QueryRow(`SELECT operator_id FROM runs WHERE id = ?`, rec.SessionID).Scan(&runOperator); err != nil {
		return err
	}
	// A NULL/empty owner is claim-on-first-touch by design: ensureRunTx already
	// filled it with this operator in the same batch, so the check reads self==self
	// and passes. What this rejects is fabricating a decision against a run another
	// operator already owns.
	if runOperator.Valid && runOperator.String != "" && runOperator.String != operatorID {
		log.Printf("ingest: guardrail record run=%s claims operator=%s but run is owned by %s — rejected (possible fabrication)",
			rec.SessionID, operatorID, runOperator.String)
		return nil
	}

	detail := canonicalGuardrailDetail(rec)
	contentHash := guardrailContentHash(rec.SessionID, action, detail)

	var existingULID sql.NullString
	err := tx.QueryRow(`SELECT ulid FROM audited_events WHERE content_hash = ?`, contentHash).Scan(&existingULID)
	if err == nil {
		return nil // true duplicate (identical run+action+detail) — already audited
	}
	if err != sql.ErrNoRows {
		return err
	}
	if dup, err := ulidAlreadyAudited(tx, rec.ULID); err != nil {
		return err
	} else if dup {
		// Same ULID as a previously-audited record but a DIFFERENT content
		// hash: either a genuine reuse bug or a suppression attempt (POST a
		// benign event under a precomputed ULID, then a real deny under the
		// same ULID). Either way the invariant is: audit it, never drop it —
		// this is exactly what closes the suppression attack (K).
		log.Printf("ingest: run=%s ulid=%s reused with a different guardrail payload — auditing anyway (possible ULID collision/tamper)",
			rec.SessionID, rec.ULID)
	}

	id, err := audit.AppendTx(tx, action, rec.SessionID, detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO audited_events(content_hash, ulid, audit_id, created_at) VALUES(?, ?, ?, ?)`,
		contentHash, rec.ULID, id, store.Now())
	return err
}

// ulidAlreadyAudited reports whether this ULID already produced an audited
// guardrail decision under a DIFFERENT content hash — the signal that this
// ULID is being reused, not that this exact decision is a replay.
func ulidAlreadyAudited(tx *sql.Tx, ulid string) (bool, error) {
	if ulid == "" {
		return false, nil
	}
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM audited_events WHERE ulid = ?`, ulid).Scan(&n)
	return n > 0, err
}

// guardrailDetail is the canonical (parseable) structure folded into
// audit_log.detail — a plain string concat of tool/machine/reason risks
// ambiguous boundaries (e.g. a reason string containing "machine=") the
// same way audit_canonical.go's length-prefixed hash avoids ambiguous byte
// concatenation for the hash chain itself.
type guardrailDetail struct {
	Tool      string `json:"tool"`
	Verdict   string `json:"verdict"`
	Reason    string `json:"reason"`
	Machine   string `json:"machine"`
	FetchedAt string `json:"snapshot_fetched_at"`
}

// canonicalGuardrailDetail builds the audit_log detail text for a central
// guardrail decision as structured JSON, not raw string concatenation.
func canonicalGuardrailDetail(rec *Record) string {
	verdict := "deny"
	if rec.Action == govern.ActionPermissionAsk {
		verdict = "ask"
	}
	b, err := json.Marshal(guardrailDetail{
		Tool:      rec.Tool,
		Verdict:   verdict,
		Reason:    rec.Payload,
		Machine:   rec.Machine,
		FetchedAt: rec.SnapshotFetchedAt,
	})
	if err != nil {
		// json.Marshal on this plain-string struct cannot fail in practice;
		// this fallback only exists so a future field addition that COULD
		// fail (e.g. a type with a custom MarshalJSON) degrades to a
		// non-empty, still-hashable detail instead of an empty string.
		return rec.Tool + ":" + rec.Payload
	}
	return string(b)
}

// guardrailContentHash is the server-derived dedup key (K): a hash of
// (run_id, action, detail), NOT the client-minted ULID. Length-prefixed
// encoding (same technique as govern's canonicalHash) so no rearrangement
// of bytes across field boundaries can collide two different tuples onto
// the same hash.
func guardrailContentHash(runID, action, detail string) string {
	h := sha256.New()
	for _, f := range []string{runID, action, detail} {
		var lenBuf [8]byte
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(f)))
		h.Write(lenBuf[:])
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}
