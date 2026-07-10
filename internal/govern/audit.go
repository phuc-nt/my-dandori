package govern

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/phuc-nt/dandori/internal/store"
)

// auditCheckpointDirEnv/OffsiteEnv configure the checkpoint sink. Env-only
// (like DANDORI_AUDIT_SIGNING_KEY) rather than a Config struct field: Audit
// is constructed directly (no *config.Config) at 25+ call sites across
// web/observer/cli, and threading config through all of them just to reach
// two rarely-changed path strings would be a large blast radius for no
// benefit — an operator sets these in the same .env/shell env as the
// signing key itself.
const (
	auditCheckpointDirEnv     = "DANDORI_AUDIT_CHECKPOINT_DIR"
	auditCheckpointOffsiteEnv = "DANDORI_AUDIT_CHECKPOINT_OFFSITE"
)

// Audit appends tamper-evident entries: each row's hash covers the previous
// row's hash, so any edit breaks the chain from that point on. When a
// signing key is configured (DANDORI_AUDIT_SIGNING_KEY), each row is also
// Ed25519-signed and the chain periodically checkpointed to a sink outside
// the database — see audit_sign.go and audit_checkpoint.go for why the
// per-row hash chain alone cannot detect a full rebuild or tail truncation.
type Audit struct {
	St    *store.Store
	Actor string
}

// firstSignedIDSettingKey is the settings row recording the id of the first
// audit row that received a signature. Any unsigned row appearing AFTER this
// id is proof of a rebuild-with-NULL-signatures attack (once signing starts,
// it must never stop) — see Verify's monotonic check.
const firstSignedIDSettingKey = "audit_first_signed_id"

// resolveCheckpointDirs reads the configured checkpoint sink(s) from env,
// falling back to DefaultCheckpointDir when unset. offsite stays ""
// (disabled) when unset — it is opt-in.
func resolveCheckpointDirs() (dir, offsite string) {
	dir = os.Getenv(auditCheckpointDirEnv)
	if dir == "" {
		dir = DefaultCheckpointDir
	}
	offsite = os.Getenv(auditCheckpointOffsiteEnv)
	return dir, offsite
}

// CheckpointDir exposes the primary (non-offsite) checkpoint sink for
// callers outside this package that need to read the latest checkpoint —
// e.g. the ingest server's skill/kit distribution endpoints, which serve the
// latest checkpoint alongside a unit's approve-hash so a remote client can
// verify the anchor itself (P5). Kept as a thin exported wrapper rather than
// exporting resolveCheckpointDirs itself, since the offsite dir is a
// write-time-only concern no read-side caller needs.
func CheckpointDir() string {
	dir, _ := resolveCheckpointDirs()
	return dir
}

// Append writes one audit entry and returns its id, in its own transaction.
func (a *Audit) Append(action, subject, detail string) (int64, error) {
	tx, err := a.St.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := a.AppendTx(tx, action, subject, detail)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// AppendTx writes one audit entry using the caller's transaction (no new
// Begin) — this lets a caller that already holds the single write connection
// open (e.g. an ingest batch applying multiple rows atomically) append audit
// rows without a second Begin, which would deadlock against
// store.Store's SetMaxOpenConns(1).
//
// When a signing key is configured, the row is Ed25519-signed and, every
// checkpointEveryN rows, a signed checkpoint is written to the configured
// sink (best-effort — a checkpoint write failure does not fail the append;
// the next cadence tick or an explicit export will retry).
func (a *Audit) AppendTx(tx *sql.Tx, action, subject, detail string) (int64, error) {
	var prev string
	_ = tx.QueryRow(`SELECT hash FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&prev)
	if prev == "" {
		prev = "genesis"
	}
	ts := store.Now()
	hash := canonicalHash(prev, ts, a.Actor, action, subject, detail)

	priv, signed := loadSigningKey()
	var sig []byte
	var keyID sql.NullInt64
	if signed {
		sig = signHash(priv, hash)
		keyID = sql.NullInt64{Int64: DefaultKeyID, Valid: true}
	} else {
		warnUnsignedOnceLog()
	}

	res, err := tx.Exec(`INSERT INTO audit_log(ts, actor, action, subject, detail, prev_hash, hash, signature, key_id)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, ts, a.Actor, action, subject, detail, prev, hash, nullBytes(sig), keyID)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()

	if signed {
		becameFirstSigned, err := a.recordFirstSignedID(tx, id)
		if err != nil {
			return id, err
		}
		// Write a checkpoint immediately the moment signing turns on (row 1
		// of the signed era), not only at the every-N cadence below — a
		// rebuild-and-strip-signatures attack in the window before row 100
		// (or before any export) would otherwise have no external anchor to
		// be caught against at all. See Verify's key-required-mode fallback
		// for what still catches this even if this write fails.
		if becameFirstSigned || id%checkpointEveryN == 0 {
			// firstSignedID must be the TRUE first-signed row, not this row's
			// own id — on the periodic (non-first) branch this row is just
			// the current tip, and the checkpoint's embedded attestation must
			// still point at whenever signing actually started, or Verify's
			// marker-independent check (via the checkpoint payload) would
			// silently narrow the monotonic floor on every periodic write.
			firstSignedID := id
			if !becameFirstSigned {
				firstSignedID = firstSignedIDFromTx(tx)
			}
			dir, offsite := resolveCheckpointDirs()
			if err := WriteCheckpoint(priv, dir, offsite, id, hash, ts, firstSignedID); err != nil {
				fmt.Fprintf(os.Stderr, "audit: checkpoint at id %d failed: %v\n", id, err)
			}
		}
	}
	return id, nil
}

// recordFirstSignedID sets the first_signed_id setting once, on the first
// signed row only (INSERT OR IGNORE semantics via a settings row that's
// never overwritten once present) — the monotonic-signing rule that closes
// the "rebuild with signature=NULL" attack needs this recorded durably, not
// derived live from MIN(id), so that a delete of early rows can't erase the
// fact that signing was already turned on. Returns becameFirstSigned=true
// only on the call that actually set it (this row is the first-ever signed
// row), so the caller knows to anchor a checkpoint immediately.
func (a *Audit) recordFirstSignedID(tx *sql.Tx, id int64) (becameFirstSigned bool, err error) {
	var existing string
	_ = tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, firstSignedIDSettingKey).Scan(&existing)
	if existing != "" {
		return false, nil
	}
	if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO NOTHING`, firstSignedIDSettingKey, fmt.Sprintf("%d", id)); err != nil {
		return false, err
	}
	return true, nil
}

// nullBytes converts an empty/nil signature into a SQL NULL rather than an
// empty-but-non-NULL blob, so unsigned rows read back as signature IS NULL.
func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// KillRun marks a run killed (its next tool call is denied) with audit.
func KillRun(st *store.Store, runID, actor, reason string) error {
	if _, err := st.DB.Exec(`UPDATE runs SET status = 'killed', ended_at = ? WHERE id = ?`,
		store.Now(), runID); err != nil {
		return err
	}
	_, _ = st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, ?, 'kill', '', 0, ?)`, runID, store.Now(), reason)
	a := &Audit{St: st, Actor: actor}
	_, err := a.Append("kill_run", runID, reason)
	return err
}

// SetGlobalKill flips the global kill switch with audit.
func SetGlobalKill(st *store.Store, on bool, actor, reason string) error {
	val := "0"
	if on {
		val = "1"
	}
	if err := st.SetSetting("kill_switch_global", val); err != nil {
		return err
	}
	a := &Audit{St: st, Actor: actor}
	_, err := a.Append(fmt.Sprintf("kill_switch_global=%s", val), "global", reason)
	return err
}
