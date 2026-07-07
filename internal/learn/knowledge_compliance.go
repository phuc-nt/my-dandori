package learn

import (
	"github.com/phuc-nt/dandori/internal/store"
)

// Knowledge compliance (F13/§"Console compliance list"): per-agent
// install-status for MANDATED skill units, for the /knowledge page. This is
// INSTALL-STATUS ONLY — ok/stale/missing — never a ranking or a score.
// Publishing a per-agent leaderboard on top of mandate compliance is exactly
// the Goodhart trap flywheel.go's own doc comment warns against for
// playbooks; the same discipline applies here.

// Compliance status values (install-status only, no numeric score).
const (
	ComplianceOK      = "ok"      // installed the CURRENT mandated version
	ComplianceStale   = "stale"   // installed an OLDER version of this (kind,name) lineage
	ComplianceMissing = "missing" // never installed any version
)

// AgentComplianceRow is one (mandated unit, operator) pair's install-status.
type AgentComplianceRow struct {
	UnitID     int64
	Kind       string
	Name       string
	Title      string
	OperatorID string
	Status     string // ComplianceOK | ComplianceStale | ComplianceMissing
}

// mandatedSkillUnit is the minimal shape AgentCompliance needs per mandated
// unit before joining against adoptions.
type mandatedSkillUnit struct {
	id    int64
	kind  string
	name  string
	title string
}

// AgentCompliance computes install-status for every (mandated skill unit) x
// (operator who has run at least once in the window all-time) pair. Read-
// only, best-effort by the caller (handlers_knowledge.go treats a query
// error as "no compliance data" rather than failing the page render) — this
// is a console convenience view, not a gate.
func AgentCompliance(st *store.Store) ([]AgentComplianceRow, error) {
	units, err := mandatedSkillUnits(st)
	if err != nil {
		return nil, err
	}
	if len(units) == 0 {
		return nil, nil
	}
	operators, err := knownOperators(st)
	if err != nil {
		return nil, err
	}

	var out []AgentComplianceRow
	for _, u := range units {
		installedCurrent, installedOlder, err := adoptedOperatorSets(st, u)
		if err != nil {
			return nil, err
		}
		for _, op := range operators {
			row := AgentComplianceRow{UnitID: u.id, Kind: u.kind, Name: u.name, Title: u.title, OperatorID: op}
			switch {
			case installedCurrent[op]:
				row.Status = ComplianceOK
			case installedOlder[op]:
				row.Status = ComplianceStale
			default:
				row.Status = ComplianceMissing
			}
			out = append(out, row)
		}
	}
	return out, nil
}

// mandatedSkillUnits lists every currently-live (published/adopted/measured)
// skill unit with required=1 — the set the SessionStart hash-check
// (hook_context.go) and this console view both watch.
func mandatedSkillUnits(st *store.Store) ([]mandatedSkillUnit, error) {
	rows, err := st.Read().Query(`SELECT id, kind, name, title FROM knowledge_units
		WHERE kind = ? AND required = 1 AND state IN ('published','adopted','measured')`, KindSkill)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mandatedSkillUnit
	for rows.Next() {
		var u mandatedSkillUnit
		if err := rows.Scan(&u.id, &u.kind, &u.name, &u.title); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// MandatedSkillUnitRef is the minimal shape the SessionStart compliance hot
// path (M3) needs per mandated skill unit: just enough to compare against
// the locally-pulled file's hash, never the full body (up to 64KB per row).
type MandatedSkillUnitRef struct {
	ID          int64
	Name        string
	ContentHash string
}

// MandatedSkillUnitRefs is the narrow, indexed hot-path query for
// hook_context.go's appendComplianceNotice: id/name/content_hash only, for
// currently-live (state='published') mandated skill units. This runs on
// EVERY SessionStart, so it intentionally does not use ListUnits (which
// selects every column including body, up to MaxUnitBodySize per row) and
// intentionally scopes to state='published' only — adopted/measured units
// have already been superseded off the "live mandate" slug by the time they
// reach those states in this pipeline's normal flow, so a hot path that
// fires on every agent turn does not need to also carry them.
func MandatedSkillUnitRefs(st *store.Store) ([]MandatedSkillUnitRef, error) {
	rows, err := st.Read().Query(`SELECT id, name, COALESCE(content_hash,'') FROM knowledge_units
		WHERE kind = 'skill' AND required = 1 AND state = 'published'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MandatedSkillUnitRef
	for rows.Next() {
		var u MandatedSkillUnitRef
		if err := rows.Scan(&u.ID, &u.Name, &u.ContentHash); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// knownOperators lists distinct operators who have driven at least one run
// (scoping the compliance view to real, active accounts rather than every
// row ever inserted into operators, e.g. disabled/service principals).
func knownOperators(st *store.Store) ([]string, error) {
	rows, err := st.Read().Query(`SELECT DISTINCT operator_id FROM runs WHERE operator_id IS NOT NULL AND operator_id != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// adoptedOperatorSets splits an operator's install status for one mandated
// unit's (kind,name) LINEAGE into "installed the current unit id" vs
// "installed an older unit id in the same lineage" — a skill's content_hash
// changes every version, so an install pinned to a superseded unit id is
// exactly the "stale" case a version bump should surface.
func adoptedOperatorSets(st *store.Store, u mandatedSkillUnit) (current, older map[string]bool, err error) {
	current = map[string]bool{}
	older = map[string]bool{}
	rows, err := st.Read().Query(`SELECT COALESCE(a.operator_id,''), a.unit_id
		FROM adoptions a JOIN knowledge_units k ON k.id = a.unit_id
		WHERE a.installed = 1 AND k.kind = ? AND k.name = ?`, u.kind, u.name)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var op string
		var unitID int64
		if err := rows.Scan(&op, &unitID); err != nil {
			return nil, nil, err
		}
		if op == "" {
			continue
		}
		if unitID == u.id {
			current[op] = true
		} else {
			older[op] = true
		}
	}
	return current, older, rows.Err()
}
