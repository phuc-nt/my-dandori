package govern

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/phuc-nt/dandori/internal/store"
)

// Audit appends tamper-evident entries: each row's hash covers the previous
// row's hash, so any edit breaks the chain from that point on.
type Audit struct {
	St    *store.Store
	Actor string
}

func chainHash(prevHash, ts, actor, action, subject, detail string) string {
	h := sha256.Sum256([]byte(prevHash + "|" + ts + "|" + actor + "|" + action + "|" + subject + "|" + detail))
	return hex.EncodeToString(h[:])
}

// Append writes one audit entry and returns its id.
func (a *Audit) Append(action, subject, detail string) (int64, error) {
	tx, err := a.St.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var prev string
	_ = tx.QueryRow(`SELECT hash FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&prev)
	if prev == "" {
		prev = "genesis"
	}
	ts := store.Now()
	hash := chainHash(prev, ts, a.Actor, action, subject, detail)
	res, err := tx.Exec(`INSERT INTO audit_log(ts, actor, action, subject, detail, prev_hash, hash)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, ts, a.Actor, action, subject, detail, prev, hash)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

// Verify walks the whole chain and returns the first broken entry id (0 = intact).
func Verify(st *store.Store) (int64, error) {
	rows, err := st.DB.Query(`SELECT id, ts, actor, action, COALESCE(subject,''), COALESCE(detail,''), prev_hash, hash
		FROM audit_log ORDER BY id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	prev := "genesis"
	for rows.Next() {
		var id int64
		var ts, actor, action, subject, detail, prevHash, hash string
		if err := rows.Scan(&id, &ts, &actor, &action, &subject, &detail, &prevHash, &hash); err != nil {
			return 0, err
		}
		if prevHash != prev || chainHash(prevHash, ts, actor, action, subject, detail) != hash {
			return id, nil
		}
		prev = hash
	}
	return 0, rows.Err()
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
