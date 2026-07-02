package govern

import (
	"database/sql"
	"fmt"

	"github.com/phuc-nt/dandori/internal/store"
)

// Autonomy bands make grades consequential: LEARN decides where an agent
// sits, GOVERN gates accordingly. Block/sandbox/budget/kill checks never
// loosen with the band — only the permission-gate strictness moves.
const (
	BandSupervised = "supervised" // every edit tool call needs approval
	BandGated      = "gated"      // default: gate rules apply
	BandTrusted    = "trusted"    // gate rules skipped except critical ones
)

// ValidBand reports whether s is a recognized band name.
func ValidBand(s string) bool {
	return s == BandSupervised || s == BandGated || s == BandTrusted
}

// BandFor returns an agent's band. No row = gated (the default). A real DB
// error fails STRICT (supervised): a safety control must not silently loosen
// on a transient failure — consistent with the engine's fail-closed rules.
func BandFor(st *store.Store, agentID string) string {
	var band string
	err := st.DB.QueryRow(`SELECT band FROM agent_bands WHERE agent_id = ?`, agentID).Scan(&band)
	switch {
	case err == sql.ErrNoRows:
		return BandGated
	case err != nil:
		return BandSupervised
	case !ValidBand(band):
		return BandGated
	}
	return band
}

// SetBand upserts an agent's band with an audit entry.
func SetBand(st *store.Store, agentID, band, actor, reason string) error {
	if !ValidBand(band) {
		return fmt.Errorf("invalid band %q (supervised|gated|trusted)", band)
	}
	if _, err := st.DB.Exec(`INSERT INTO agent_bands(agent_id, band, updated_at, updated_by, reason)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET band = excluded.band,
			updated_at = excluded.updated_at, updated_by = excluded.updated_by, reason = excluded.reason`,
		agentID, band, store.Now(), actor, reason); err != nil {
		return err
	}
	a := &Audit{St: st, Actor: actor}
	_, err := a.Append("set_band", agentID, band+" — "+reason)
	return err
}
