package learn

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/phuc-nt/dandori/internal/store"
)

// KnowledgeUnit is one row of the pipeline envelope.
type KnowledgeUnit struct {
	ID            int64
	Kind          string
	Name          string
	Title         string
	State         string
	VersionN      int
	SupersedesID  *int64
	RefKind       string
	RefID         *int64
	Body          string
	ContentHash   string
	Layer         string
	LayerTarget   string
	Required      bool
	NPresent      *int
	NAbsent       *int
	DonePresent   *float64
	DoneAbsent    *float64
	CIPresentLo   *int
	CIPresentHi   *int
	CIAbsentLo    *int
	CIAbsentHi    *int
	CostPresent   *float64
	CostAbsent    *float64
	ProvenanceRun []string
	NominatedBy   string
	// Origin + OriginModel (v13 anti-Goodhart badge) follow the unit end-to-
	// end: "human" (default, pre-v13 rows and plain nominate) | "import-
	// memory"/"import-journal" | "ai-draft" (OriginModel set) | "detector".
	// COALESCE'd to 'human' at SELECT time (migration 017 has a DB DEFAULT,
	// but a defensive COALESCE keeps GetUnit/ListUnits honest even against a
	// future NULL). M3: added in lockstep with unitSelectCols + scanUnit —
	// see scanUnit's doc comment for why order/count must never drift.
	Origin      string
	OriginModel string
	CreatedAt   string
	UpdatedAt   string
}

const unitSelectCols = `id, kind, name, title, state, version_n, supersedes_id,
	COALESCE(ref_kind,''), ref_id, COALESCE(body,''), COALESCE(content_hash,''),
	COALESCE(layer,''), COALESCE(layer_target,''), required,
	n_present, n_absent, done_present, done_absent,
	ci_present_lo, ci_present_hi, ci_absent_lo, ci_absent_hi,
	cost_present, cost_absent, COALESCE(provenance_run_ids,'[]'), COALESCE(nominated_by,''),
	COALESCE(origin,'human'), COALESCE(origin_model,''),
	created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

// scanUnit's Scan targets must stay in the EXACT same order/count as
// unitSelectCols (M3 — the v11 F-fix column-count trap): adding a column to
// one without the matching struct field + positional Scan target here breaks
// EVERY unit read (/knowledge, /reviews, skill pull) with a
// "sql: expected N destination arguments" panic-on-Scan, not a compile error.
func scanUnit(row rowScanner) (KnowledgeUnit, error) {
	var u KnowledgeUnit
	var required int
	var prov string
	if err := row.Scan(&u.ID, &u.Kind, &u.Name, &u.Title, &u.State, &u.VersionN, &u.SupersedesID,
		&u.RefKind, &u.RefID, &u.Body, &u.ContentHash,
		&u.Layer, &u.LayerTarget, &required,
		&u.NPresent, &u.NAbsent, &u.DonePresent, &u.DoneAbsent,
		&u.CIPresentLo, &u.CIPresentHi, &u.CIAbsentLo, &u.CIAbsentHi,
		&u.CostPresent, &u.CostAbsent, &prov, &u.NominatedBy,
		&u.Origin, &u.OriginModel,
		&u.CreatedAt, &u.UpdatedAt); err != nil {
		return u, err
	}
	u.Required = required != 0
	_ = json.Unmarshal([]byte(prov), &u.ProvenanceRun)
	return u, nil
}

// ListUnits lists units in a given state, newest first. Empty state lists all.
func ListUnits(st *store.Store, state string) ([]KnowledgeUnit, error) {
	q := `SELECT ` + unitSelectCols + ` FROM knowledge_units`
	args := []any{}
	if state != "" {
		q += ` WHERE state = ?`
		args = append(args, state)
	}
	q += ` ORDER BY id DESC`
	rows, err := st.Read().Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeUnit
	for rows.Next() {
		u, err := scanUnit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUnit fetches one unit by id, or nil (no error) when absent.
func GetUnit(st *store.Store, id int64) (*KnowledgeUnit, error) {
	row := st.Read().QueryRow(`SELECT `+unitSelectCols+` FROM knowledge_units WHERE id = ?`, id)
	u, err := scanUnit(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// KitFileRow is one knowledge_kit_files row for review-UI/CLI rendering —
// path+size for the manifest list, and the full body for on-demand expand
// (both /reviews and /knowledge recompute their per-file display from these
// SAME rows, per H1: the manifest hash being approved must always agree with
// what a reviewer can expand and read).
type KitFileRow struct {
	Path        string
	Body        string
	ContentHash string
	Size        int
}

// KitFiles loads every knowledge_kit_files row for unitID, ordered by path
// (matching the canonical manifest order BuildKitManifest produces) — used by
// the review UI (manifest list + per-file expand + version-bump diff) and
// available to any future kit consumer that needs the real per-file bodies
// rather than just the manifest's path/hash/size summary.
func KitFiles(st *store.Store, unitID int64) ([]KitFileRow, error) {
	rows, err := st.Read().Query(`SELECT path, body, content_hash, size FROM knowledge_kit_files
		WHERE unit_id = ? ORDER BY path`, unitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KitFileRow
	for rows.Next() {
		var f KitFileRow
		if err := rows.Scan(&f.Path, &f.Body, &f.ContentHash, &f.Size); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// transition validates the current state matches "from", writes the new
// state + updated_at, and records the transition row — all in one tx so a
// concurrent transition can't race past the from-state check.
func transition(st *store.Store, unitID int64, from, to, actor, note string) error {
	tx, err := st.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cur string
	if err := tx.QueryRow(`SELECT state FROM knowledge_units WHERE id = ?`, unitID).Scan(&cur); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("unit %d not found", unitID)
		}
		return err
	}
	if cur != from {
		return fmt.Errorf("unit %d in state %q, expected %q", unitID, cur, from)
	}
	if _, err := tx.Exec(`UPDATE knowledge_units SET state = ?, updated_at = ? WHERE id = ?`,
		to, store.Now(), unitID); err != nil {
		return err
	}
	if err := recordTransitionTx(tx, unitID, from, to, actor, note); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkSuperseded transitions a published/adopted/measured unit to superseded.
// This is the applier-side half of F5 ("Publish v2 → v1 tự chuyển
// superseded") — P1 (NominateUnit) only ever sets a supersedes_id pointer; P3
// calls MarkSuperseded in the same tx as applying an approved knowledge-
// publish action for the new version. unitID must currently be in a "live
// published" state (any of published/adopted/measured); any other state is a
// caller error.
//
// Ordering matters: idx_ku_kind_name_live allows only ONE row per (kind,name)
// in state published/adopted/measured, so the applier must call
// MarkSuperseded(oldID) BEFORE transitioning the new version into published —
// otherwise the new version's own transition would collide with the old
// row's still-live index entry.
func MarkSuperseded(st *store.Store, unitID int64, actor, note string) error {
	tx, err := st.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := MarkSupersededTx(tx, unitID, actor, note); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkSupersededTx is the Tx-scoped twin of MarkSuperseded, for callers
// (H2 — observer.applyKnowledgePublish) that must fold this transition into a
// larger atomic sequence sharing one connection (st.DB has exactly one open
// connection; a nested Begin() while already inside a tx would deadlock).
// ErrStateMismatch marks the from-state check failure so callers can classify
// it as permanent (never retryable) same as a UNIQUE-violation.
func MarkSupersededTx(tx *sql.Tx, unitID int64, actor, note string) error {
	var cur string
	if err := tx.QueryRow(`SELECT state FROM knowledge_units WHERE id = ?`, unitID).Scan(&cur); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: unit %d not found", ErrStateMismatch, unitID)
		}
		return err
	}
	switch cur {
	case StatePublished, StateAdopted, StateMeasured:
	default:
		return fmt.Errorf("%w: unit %d in state %q, expected published/adopted/measured", ErrStateMismatch, unitID, cur)
	}
	if _, err := tx.Exec(`UPDATE knowledge_units SET state = ?, updated_at = ? WHERE id = ?`,
		StateSuperseded, store.Now(), unitID); err != nil {
		return err
	}
	return recordTransitionTx(tx, unitID, cur, StateSuperseded, actor, note)
}

func recordTransitionTx(tx *sql.Tx, unitID int64, from, to, actor, note string) error {
	_, err := tx.Exec(`INSERT INTO knowledge_transitions(unit_id, from_state, to_state, actor, note, at)
		VALUES(?, ?, ?, ?, ?, ?)`, unitID, from, to, actor, note, store.Now())
	return err
}

func nullStrIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt64If(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// contentHashIf returns sha256(body) hex for ANY unit that carries a body
// (skill always; context/rule only in the RefID==0 "detector proposes new
// text" case) — empty when there is no body to hash. C1: every body-carrying
// unit needs a content_hash pinned alongside it so the applier's "pinned
// body+hash" contract (gatedKnowledgeWrite) is satisfiable regardless of
// kind, not just skill.
func contentHashIf(kind, body string) any {
	if body == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}
