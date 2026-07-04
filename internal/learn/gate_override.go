package learn

import (
	"errors"
	"fmt"

	"github.com/phuc-nt/dandori/internal/store"
)

// ErrOverrideReasonRequired is returned when a caller tries to override a
// gate check without a justification (UB4 — separation of duties: every
// override is a recorded human decision, never a silent bypass).
var ErrOverrideReasonRequired = errors.New("override reason is required")

// Auditor is the narrow slice of govern.Audit this package needs. Kept local
// (structural typing, same pattern as ghub/gws Gate) so learn does not import
// govern for one method — govern would import learn back (LeaderboardRow),
// creating a cycle. *govern.Audit satisfies this interface as-is.
type Auditor interface {
	Append(action, subject, detail string) (int64, error)
}

// OverrideGate marks one failed gate_results row as human-overridden (UB4).
// Per-check, never blanket: only the row matching run_id+checkName is
// touched. Idempotent — a check that is already overridden (or was never
// failing) yields zero rows affected and no duplicate audit entry. When this
// override clears the LAST un-overridden failed check for the run, the
// "quality gate failed" flag is marked resolved (status change only, never
// deleted — traceability). Override rows are immutable: this only ever
// UPDATEs a row WHERE overridden_at IS NULL, so a second call on the same
// check is a no-op, not a toggle.
func OverrideGate(st *store.Store, a Auditor, runID, checkName, by, reason string) error {
	if reason == "" {
		return ErrOverrideReasonRequired
	}
	res, err := st.DB.Exec(`UPDATE gate_results SET overridden_at = ?, overridden_by = ?, override_reason = ?
		WHERE run_id = ? AND check_name = ? AND ok = 0 AND overridden_at IS NULL`,
		store.Now(), by, reason, runID, checkName)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil // already overridden (or never failed) — no-op, no duplicate audit
	}
	if _, err := a.Append("gate_overridden", fmt.Sprintf("run:%s:%s", runID, checkName), reason); err != nil {
		return err
	}
	return resolveGateFlagIfClear(st, a, runID)
}

// resolveGateFlagIfClear marks the run's "quality gate failed" flag resolved
// once no un-overridden failed check remains. Status change only — flags are
// never deleted, so the override trail stays queryable.
func resolveGateFlagIfClear(st *store.Store, a Auditor, runID string) error {
	var remaining int
	if err := st.DB.QueryRow(`SELECT count(*) FROM gate_results
		WHERE run_id = ? AND ok = 0 AND overridden_at IS NULL`, runID).Scan(&remaining); err != nil {
		return err
	}
	if remaining > 0 {
		return nil
	}
	var flagID int64
	err := st.DB.QueryRow(`SELECT id FROM flags
		WHERE run_id = ? AND status = 'open' AND reason LIKE 'quality gate failed%'
		ORDER BY id DESC LIMIT 1`, runID).Scan(&flagID)
	if err != nil {
		return nil // no matching open flag — nothing to resolve
	}
	if _, err := st.DB.Exec(`UPDATE flags SET status = 'resolved' WHERE id = ?`, flagID); err != nil {
		return err
	}
	_, err = a.Append("flag_resolved", fmt.Sprintf("run:%s", runID),
		fmt.Sprintf("flag #%d: all failed gate checks overridden", flagID))
	return err
}
