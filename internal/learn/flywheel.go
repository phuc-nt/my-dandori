package learn

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/store"
)

// Flywheel: detect what top performers do differently, package it as a
// playbook PATTERN, and measure whether adopters improve. Publication
// content is patterns and playbooks only — never a human ranking (Goodhart:
// publish steering counts and people stop steering, even when they should).

// Candidate is a clean, well-structured run worth promoting to a playbook.
type Candidate struct {
	RunID     string
	AgentID   string
	AgentName string
	TaskKey   string
	Model     string
	CostUSD   float64
	Why       string // plain Vietnamese pattern description
}

// adoptionMinRuns is how many subsequent runs an adopter needs before the
// after-metric means anything.
const adoptionMinRuns = 3

// DetectCandidates finds recent runs showing the top-performer pattern:
// finished clean (no tool errors), driven with a specific opening prompt
// (file paths / task ref / acceptance criteria), little steering, and not
// already nominated as a knowledge unit (playbook kind).
func DetectCandidates(st *store.Store, windowDays int) ([]Candidate, error) {
	rows, err := st.Read().Query(`
		SELECT r.id, r.agent_id, a.name, COALESCE(r.task_key,''), COALESCE(r.model,''), r.cost_usd,
		       COALESCE((SELECT e.payload FROM events e WHERE e.run_id = r.id AND e.kind = 'prompt_proxy'), ''),
		       COALESCE((SELECT CAST(e.payload AS INTEGER) FROM events e WHERE e.run_id = r.id AND e.kind = 'user_msg'), 0)
		FROM runs r JOIN agents a ON a.id = r.agent_id
		WHERE r.status = 'done'
		  AND r.started_at >= ` + windowClause(windowDays) + `
		  AND NOT EXISTS (SELECT 1 FROM knowledge_units k
		                  WHERE k.kind = 'playbook' AND k.provenance_run_ids LIKE '%"' || r.id || '"%')
		  AND NOT EXISTS (SELECT 1 FROM events e WHERE e.run_id = r.id AND e.kind = 'tool_result' AND e.ok = 0)
		ORDER BY r.started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var proxy string
		var steering int
		if err := rows.Scan(&c.RunID, &c.AgentID, &c.AgentName, &c.TaskKey, &c.Model, &c.CostUSD, &proxy, &steering); err != nil {
			return nil, err
		}
		spec := proxySpec(proxy)
		if spec == 0 || steering > 2 {
			continue // not the pattern we want to spread
		}
		c.Why = candidateWhy(spec, steering)
		out = append(out, c)
	}
	return out, rows.Err()
}

func proxySpec(proxyJSON string) int {
	var p struct{ W, Spec int }
	if len(proxyJSON) == 0 {
		return 0
	}
	if err := json.Unmarshal([]byte(proxyJSON), &p); err != nil {
		return 0
	}
	return p.Spec
}

func candidateWhy(spec, steering int) string {
	why := "Run sạch không lỗi tool"
	if spec&capture.SpecHasPath != 0 {
		why += " · prompt nêu rõ file cần sửa"
	}
	if spec&capture.SpecHasTaskRef != 0 {
		why += " · gắn mã task"
	}
	if spec&capture.SpecHasCriteria != 0 {
		why += " · nêu tiêu chí hoàn thành ngay từ đầu"
	}
	if steering == 0 {
		why += " · agent tự chạy đến đích, không cần can thiệp"
	}
	return why + " — mẫu đáng nhân bản."
}

// PromoteCandidate nominates a candidate run as a knowledge_units row
// (kind=playbook, state=nominated). It no longer writes the playbooks table
// directly — that bypassed review entirely. A real playbook row is created
// by the applier only after a human approves the "knowledge-publish"
// request (P3). The card documents the PATTERN (why), never a human
// comparison.
func PromoteCandidate(st *store.Store, c Candidate, actor string) (int64, error) {
	var exists int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE kind = ? AND provenance_run_ids LIKE ?`,
		KindPlaybook, `%"`+c.RunID+`"%`).Scan(&exists)
	if exists > 0 {
		// Same class of outcome as NominateUnit's ErrDuplicateDraft (M2): a
		// batch caller (DetectKnowledgeUnits) needs to tell "already proposed,
		// skip" apart from a real failure that must propagate.
		return 0, fmt.Errorf("%w: run %s already promoted", ErrDuplicateDraft, c.RunID)
	}
	title := "Pattern: " + c.AgentName
	if c.TaskKey != "" {
		title += " · " + c.TaskKey
	}
	return NominateUnit(st, NominateParams{
		Kind:          KindPlaybook,
		Name:          PlaybookSlug(c.RunID),
		Title:         title,
		RefKind:       "candidate_run",
		RefID:         0, // no existing playbooks row yet — created at publish
		Layer:         "",
		LayerTarget:   "",
		ProvenanceRun: []string{c.RunID},
		NominatedBy:   actor,
		Stats:         StatsSnapshot{}, // flywheel candidates carry no present/absent split yet
	})
}

// PlaybookSlug turns a run id into a stable kebab-case knowledge_units.name.
// Run ids are already short opaque tokens (ULID-ish or CLI-provided), so
// lower-casing and swapping disallowed characters for '-' is enough to make
// a valid slug without losing traceability back to the run. Exported (H3) so
// the web layer's manual "save this run as a playbook" path can nominate
// through the same slug scheme the flywheel's auto-detector uses, keeping
// one playbook-per-run at the knowledge_units level regardless of which path
// nominated it.
func PlaybookSlug(runID string) string {
	b := strings.ToLower(runID)
	var out strings.Builder
	for _, r := range b {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
	}
	s := strings.Trim(out.String(), "-")
	if s == "" {
		s = "run"
	}
	return "run-" + s
}

// RecordAdoption marks that an operator started a run from a playbook.
// metric_before freezes their done-rate at adoption time.
func RecordAdoption(st *store.Store, playbookID int64, operatorID, runID string, windowDays int) (int64, error) {
	var before sql.NullFloat64
	if operatorID != "" {
		if ob, err := OperatorBehaviorAgg(st, operatorID, windowDays); err == nil && ob.Runs > 0 {
			before = sql.NullFloat64{Float64: ob.DoneRate, Valid: true}
		}
	}
	res, err := st.DB.Exec(`INSERT INTO adoptions(playbook_id, operator_id, run_id, adopted_at, metric_before)
		VALUES(?, ?, NULLIF(?, ''), ?, ?)`, playbookID, nullStr(operatorID), runID, store.Now(), before)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RecordUnitAdoption is the kind-agnostic twin of RecordAdoption (F4): any
// knowledge_units kind (context/rule/playbook/skill) can be adopted, not just
// playbooks. installed distinguishes a skill "pulled to disk" (installed=1)
// from a plain suggest-only click; ComputeAdoptionOutcomes only promotes a
// skill row to "measured" once it is also ACTIVE (F2/§5.4 — installed-not-
// active never farms an outcome). RecordAdoption's own signature and
// behavior are UNCHANGED — this is a new function, not a generalization of
// the old one, so handlers_playbooks.go:141 (P4's caller) never breaks.
//
// M5: dedups per (unit_id, operator_id) — a repeat pull of the SAME unit by
// the SAME operator (e.g. re-pulling after a version bump, or just running
// `skill pull` again) refreshes the existing row's adopted_at/installed/
// metric_before in place instead of inserting a second one. Without this, a
// single operator repeat-pulling the same unit N times would (a) inflate
// "number of adopters" by N for one real adopter, and (b) let
// NominateRetireProposals' avg(metric_before/after) double-count that
// operator's outcome N times, skewing the retire signal toward whichever
// operator happens to pull most often. A stale already-computed outcome
// (metric_after/computed_at) from a PRIOR adoption is cleared on refresh —
// it described the old adopted_at baseline, not this one, so
// ComputeAdoptionOutcomes must compute it fresh relative to the new pull.
// operatorID == "" never dedups (nothing to key on) — matches the pre-M5
// behavior of always inserting when there is no operator to attribute to.
func RecordUnitAdoption(st *store.Store, unitID int64, operatorID, runID string, installed bool, windowDays int) (int64, error) {
	var before sql.NullFloat64
	if operatorID != "" {
		if ob, err := OperatorBehaviorAgg(st, operatorID, windowDays); err == nil && ob.Runs > 0 {
			before = sql.NullFloat64{Float64: ob.DoneRate, Valid: true}
		}
	}
	installedInt := 0
	if installed {
		installedInt = 1
	}
	if operatorID != "" {
		var existingID int64
		err := st.DB.QueryRow(`SELECT id FROM adoptions WHERE unit_id = ? AND operator_id = ?`,
			unitID, operatorID).Scan(&existingID)
		if err == nil {
			if _, err := st.DB.Exec(`UPDATE adoptions SET installed = ?, run_id = NULLIF(?, ''),
				adopted_at = ?, metric_before = ?, metric_after = NULL, computed_at = NULL WHERE id = ?`,
				installedInt, runID, store.Now(), before, existingID); err != nil {
				return 0, err
			}
			return existingID, nil
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
	}
	res, err := st.DB.Exec(`INSERT INTO adoptions(unit_id, installed, operator_id, run_id, adopted_at, metric_before)
		VALUES(?, ?, ?, NULLIF(?, ''), ?, ?)`, unitID, installedInt, nullStr(operatorID), runID, store.Now(), before)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// skillActive reports whether operatorID has invoked the named skill
// (tool_use/Skill, $.skill=name) in any run started AFTER since — the
// "active" half of installed-vs-active (F2/§5.4): an adopt-click only
// installs; measuring requires actual subsequent use, so a click alone can
// never farm an outcome.
func skillActive(st *store.Store, name, operatorID, since string) (bool, error) {
	var exists int
	err := st.Read().QueryRow(`SELECT EXISTS(
		SELECT 1 FROM events e JOIN runs r ON r.id = e.run_id
		WHERE e.kind = 'tool_use' AND e.tool_name = 'Skill'
		  AND json_extract(e.payload, '$.skill') = ?
		  AND r.operator_id = ? AND r.started_at > ?)`, name, operatorID, since).Scan(&exists)
	return exists != 0, err
}

// ComputeAdoptionOutcomes fills metric_after for adoptions whose operator
// has completed enough runs since adopting. Outcomes stay PRIVATE (tech-lead
// coaching data) — they are never part of a published card. unit-aware
// (F4/§5.4): rows carrying unit_id read the unit's kind+name; a skill-kind
// row must also be ACTIVE (skillActive) before metric_after computes —
// installed-not-active rows are left pending forever (never measured, never
// retried into a false outcome) until the operator actually uses it.
func ComputeAdoptionOutcomes(st *store.Store) (int, error) {
	rows, err := st.DB.Query(`SELECT a.id, COALESCE(a.operator_id,''), a.adopted_at,
		a.unit_id, a.installed, COALESCE(k.kind,''), COALESCE(k.name,'')
		FROM adoptions a LEFT JOIN knowledge_units k ON k.id = a.unit_id
		WHERE a.computed_at IS NULL`)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id             int64
		op, since      string
		unitID         sql.NullInt64
		installed      int
		kind, unitName string
	}
	var ps []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.op, &p.since, &p.unitID, &p.installed, &p.kind, &p.unitName); err != nil {
			rows.Close()
			return 0, err
		}
		ps = append(ps, p)
	}
	rows.Close()
	done := 0
	for _, p := range ps {
		if p.op == "" {
			continue
		}
		if p.unitID.Valid && p.kind == KindSkill {
			active, err := skillActive(st, p.unitName, p.op, p.since)
			if err != nil {
				return done, err
			}
			if !active {
				continue // installed-not-active: skip, adopt-click can't farm outcomes
			}
		}
		var runs int
		var rate sql.NullFloat64
		err := st.Read().QueryRow(`SELECT count(*),
			avg(CASE WHEN status IN ('done','failed','killed') THEN CASE WHEN status='done' THEN 1.0 ELSE 0.0 END END)
			FROM runs WHERE operator_id = ? AND started_at > ?`, p.op, p.since).Scan(&runs, &rate)
		if err != nil || runs < adoptionMinRuns || !rate.Valid {
			continue
		}
		if _, err := st.DB.Exec(`UPDATE adoptions SET metric_after = ?, computed_at = ? WHERE id = ?`,
			rate.Float64, store.Now(), p.id); err != nil {
			return done, err
		}
		done++
	}
	if done > 0 {
		// Best-effort (§5.3/F10): a failure nominating a retire-proposal card
		// must never turn a successful outcome-compute pass into an error —
		// the outcomes themselves already landed above.
		_, _ = NominateRetireProposals(st)
	}
	return done, nil
}

// retireProposalMargin is how much worse (in done-rate) a unit's active
// adopters must measure, on average, before a retire-proposal is worth
// surfacing to a human — small negative deltas are exactly the noise F10
// warns about (regression-to-mean can move either direction), so this floor
// keeps the signal from firing on every minor dip.
const retireProposalMargin = 0.10

// NominateRetireProposals scans measured (computed_at set, both before/after
// present) adoptions per live unit and, when the average after-rate is
// worse than before by more than retireProposalMargin, nominates a
// retire-proposal draft (kind = the unit's own kind, RefKind/RefID pointing
// at the live unit) — a signal for a human to review, NEVER an automatic
// retire (§5.3: "NOT auto — người quyết"). The draft's title carries the
// F10 regression-to-mean caveat inline so the card is self-explanatory in
// the /knowledge queue without a template change. One proposal at a time
// per unit: NominateUnit's own (kind,name) draft dedup (idx_ku_kind_name_
// draft) rejects a second nominate while one is still nominated/in_review,
// so a repeat measured-worse pass on the same unit is a silent no-op, not
// an error — this function only reports actual new proposals.
func NominateRetireProposals(st *store.Store) (int, error) {
	rows, err := st.Read().Query(`SELECT k.id, k.kind, k.name, k.title,
			avg(a.metric_before), avg(a.metric_after), count(*)
		FROM adoptions a JOIN knowledge_units k ON k.id = a.unit_id
		WHERE a.unit_id IS NOT NULL AND a.metric_before IS NOT NULL AND a.metric_after IS NOT NULL
		  AND k.state IN ('published','adopted','measured')
		GROUP BY k.id`)
	if err != nil {
		return 0, err
	}
	type worse struct {
		id                int64
		kind, name, title string
		before, after     float64
		n                 int
	}
	var candidates []worse
	for rows.Next() {
		var w worse
		if err := rows.Scan(&w.id, &w.kind, &w.name, &w.title, &w.before, &w.after, &w.n); err != nil {
			rows.Close()
			return 0, err
		}
		if w.before-w.after >= retireProposalMargin {
			candidates = append(candidates, w)
		}
	}
	rows.Close()

	nominated := 0
	for _, w := range candidates {
		name := retireProposalSlug(w.name)
		if !ValidSlug(name) {
			continue // defensive: unit name too long/odd to build a valid slug — skip, don't fail the batch
		}
		title := fmt.Sprintf("[Đề xuất retire] %s — đo được sau adopt TỆ HƠN trước %.0f điểm phần trăm (quan sát — có thể do hồi quy về trung bình, KHÔNG kết luận nhân quả)",
			w.title, (w.before-w.after)*100)
		unitID := w.id
		_, err := NominateUnit(st, NominateParams{
			Kind: w.kind,
			Name: name,
			Title: title,
			// M2: RefKindRetireTarget (not w.kind) marks this draft as
			// signal-only — RequestPublish refuses to gate a publish approval
			// for it; the correct human action is retiring the TARGET unit
			// (RefID) directly, via its own Retire button.
			RefKind:     RefKindRetireTarget,
			RefID:       unitID,
			NominatedBy: "dandori-observer",
		})
		if err != nil {
			if errors.Is(err, ErrDuplicateDraft) {
				continue // already proposed, awaiting review — not an error
			}
			return nominated, err
		}
		nominated++
	}
	return nominated, nil
}

// retireProposalSlug derives a stable, valid (kind,name)-dedup slug for a
// unit's retire-proposal draft, capped at MaxSlugLen (M6) the same way every
// other nominate-time slug is.
func retireProposalSlug(unitName string) string {
	s := unitName + "-retire-proposal"
	if len(s) > MaxSlugLen {
		s = s[:MaxSlugLen]
	}
	return strings.TrimRight(s, "-")
}

// AdoptionRow is one adopter's outcome for a playbook.
type AdoptionRow struct {
	OperatorID string
	AdoptedAt  string
	Before     *float64
	After      *float64
	Improved   *bool
	EnoughData bool
}

// AdoptionReport lists a playbook's adopters and their before/after.
func AdoptionReport(st *store.Store, playbookID int64) ([]AdoptionRow, error) {
	rows, err := st.Read().Query(`SELECT COALESCE(operator_id,''), adopted_at, metric_before, metric_after
		FROM adoptions WHERE playbook_id = ? ORDER BY adopted_at`, playbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdoptionRow
	for rows.Next() {
		var r AdoptionRow
		var before, after sql.NullFloat64
		if err := rows.Scan(&r.OperatorID, &r.AdoptedAt, &before, &after); err != nil {
			return nil, err
		}
		if before.Valid {
			r.Before = &before.Float64
		}
		if after.Valid {
			r.After = &after.Float64
			r.EnoughData = true
			if before.Valid {
				improved := after.Float64 > before.Float64
				r.Improved = &improved
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
