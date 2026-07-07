package observer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint
// failure (modernc.org/sqlite surfaces these as a message string) — mirrors
// contexthub.isUniqueViolation/learn.isUniqueViolation; duplicated here
// (2-line string match) rather than exported cross-package (H2).
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// knowledgePublishParams is the evidence shape RequestPublish pins at request
// time (learn.unitActionParams mirror — this package cannot import the
// unexported type, so the JSON shape is the contract between the two). Every
// field here is what the human reviewer saw and approved; the applier reads
// ONLY these pinned bytes, never the live knowledge_units row content, so a
// row edited between request and approval cannot silently change what gets
// published (H3 TOCTOU).
type knowledgePublishParams struct {
	UnitID      int64  `json:"unit_id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	RefKind     string `json:"ref_kind"`
	RefID       int64  `json:"ref_id"`
	Body        string `json:"body"`
	ContentHash string `json:"content_hash"`
	Layer       string `json:"layer"`
	LayerTarget string `json:"layer_target"`
	// RuleIntent (H1) is the pinned human-approved effect for kind=rule:
	// "enable" (default — new/candidate rule turns on), "retire" (turn OFF),
	// "scope-up" (widen scope_type to company/global). Empty/unknown defaults
	// to "enable" for backward-compat with any pre-H1 pinned evidence.
	RuleIntent string `json:"rule_intent"`
}

// applyKnowledgePublish is the sole path from an approved "knowledge-publish"
// action to a live, distributed knowledge unit (F1/F5/F7). H2: the whole
// sequence (distribution write + supersede-old + transition-new) runs inside
// ONE *sql.Tx — st.DB has exactly one open connection, so nested Begin()
// calls from composing SaveContext/MarkSuperseded/publishUnitTransition each
// opening their own tx would deadlock; every write below therefore goes
// through a Tx-accepting variant instead. Ordering (F5): if the unit has a
// live predecessor (supersedes_id), MarkSuperseded(old) MUST happen before
// the new unit transitions into published — the idx_ku_kind_name_live
// partial unique index allows only one published/adopted/measured row per
// (kind,name), so publishing the new version first would collide with the
// still-live old row. A state-mismatch or UNIQUE-violation anywhere in the
// tx is permanent (retrying cannot fix a stale state or a genuine
// collision) — the tx rolls back on any error, so nothing is ever
// half-written; the audit append runs once, after a successful commit
// (matching the established pattern elsewhere in this package: audit is a
// best-effort record of a write that already landed, not part of the write
// itself).
func applyKnowledgePublish(st *store.Store, evidence string, insightID int64, decidedBy string) error {
	var ev knowledgePublishParams
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		return errPermanentApply{err}
	}
	if ev.UnitID == 0 || ev.Kind == "" || ev.Name == "" {
		return errPermanentApply{fmt.Errorf("insight %d: invalid knowledge-publish params", insightID)}
	}
	// M2 defense-in-depth: learn.RequestPublish already refuses to open this
	// approval for a retire-proposal draft, but re-check the pinned evidence
	// here too — a stale/pre-fix approval row (requested before the
	// RequestPublish gate landed) must still dead-end cleanly rather than
	// creating a live playbooks row for an empty-body proposal, or bouncing
	// off errPermanentApply with no explanation for skill/context.
	if ev.RefKind == learn.RefKindRetireTarget {
		return errPermanentApply{fmt.Errorf("insight %d: unit %d is a retire-proposal — retire the target unit (ref_id=%d) instead of publishing",
			insightID, ev.UnitID, ev.RefID)}
	}

	unit, err := learn.GetUnit(st, ev.UnitID)
	if err != nil {
		return err // transient — retry
	}
	if unit == nil {
		return errPermanentApply{fmt.Errorf("insight %d: unit %d not found", insightID, ev.UnitID)}
	}
	// F5 idempotency backstop: the applier re-checks state at apply time (not
	// just at request time) — a unit already published/retired/rejected since
	// this approval was requested must not be double-applied.
	if unit.State != learn.StateInReview {
		return errPermanentApply{fmt.Errorf("insight %d: unit %d in state %q, expected %q — already applied or moved",
			insightID, ev.UnitID, unit.State, learn.StateInReview)}
	}

	note := fmt.Sprintf("knowledge-publish duyệt #%d", insightID)

	tx, err := st.DB.Begin()
	if err != nil {
		return err // transient — retry
	}
	defer tx.Rollback()

	if err := gatedKnowledgeWriteTx(tx, ev, decidedBy, note); err != nil {
		return classifyApplyErr(err, insightID)
	}
	// F5: supersede the old live head BEFORE publishing the new one — ordering
	// is load-bearing (see doc comment + learn.MarkSupersededTx).
	if unit.SupersedesID != nil {
		if err := learn.MarkSupersededTx(tx, *unit.SupersedesID, decidedBy, note); err != nil {
			return classifyApplyErr(err, insightID)
		}
	}
	if err := publishUnitTransitionTx(tx, ev.UnitID, decidedBy, note); err != nil {
		return classifyApplyErr(err, insightID)
	}
	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err) {
			return errPermanentApply{fmt.Errorf("insight %d: %w", insightID, err)}
		}
		return err // transient — retry
	}

	a := &govern.Audit{St: st, Actor: decidedBy}
	detail := fmt.Sprintf("unit_id=%d %s %q v%d published (insight #%d)", ev.UnitID, ev.Kind, ev.Name, unit.VersionN, insightID)
	if ev.Kind == learn.KindSkill {
		// F7/H1: audit is the independent hash source `skill pull` verifies
		// against — never the knowledge_units row itself. unit_id is pinned
		// into the detail (not just kind:name subject, which is shared across
		// every version in the (kind,name) lineage) so skillreg.ApproveHash can
		// select "the entry belonging to THIS unit id" instead of scanning
		// history for any entry whose hash happens to agree with the row.
		detail = fmt.Sprintf("skill %q v%d published, unit_id=%d, content_hash=%s (insight #%d)", ev.Name, unit.VersionN, ev.UnitID, ev.ContentHash, insightID)
	}
	_, err = a.Append("knowledge_published", ev.Kind+":"+ev.Name, detail)
	return err
}

// classifyApplyErr maps a write error from inside applyKnowledgePublish's tx
// to errPermanentApply when it is a state-mismatch (learn.ErrStateMismatch)
// or a UNIQUE-violation (a concurrent apply/supersede raced ahead) — neither
// is fixable by retrying the same evidence again. Any other error (a plain
// transient DB failure) passes through unchanged so the caller un-consumes
// and retries next cycle.
func classifyApplyErr(err error, insightID int64) error {
	if _, ok := err.(errPermanentApply); ok {
		return err
	}
	if errors.Is(err, learn.ErrStateMismatch) || isUniqueViolation(err) {
		return errPermanentApply{fmt.Errorf("insight %d: %w", insightID, err)}
	}
	return err
}

// gatedKnowledgeWriteTx performs the per-kind distribution write from PINNED
// evidence params — never re-derived from the live unit row (H3), and always
// inside the caller's shared tx (H2). context and rule reuse existing
// distribution rails (context = SessionStart injection, rule = server-side
// enforcement); playbook creates its real row for the first time here (P1:
// NominateUnit never writes playbooks directly); skill's "write" is simply
// freezing state+hash — actual bytes are already pinned in the unit row and
// served pull-only (P5).
func gatedKnowledgeWriteTx(tx *sql.Tx, ev knowledgePublishParams, decidedBy, note string) error {
	switch ev.Kind {
	case learn.KindContext:
		if ev.Layer == "" || ev.LayerTarget == "" {
			return errPermanentApply{fmt.Errorf("context publish requires layer+layer_target (unit %d)", ev.UnitID)}
		}
		if ev.Body == "" {
			return errPermanentApply{fmt.Errorf("context publish requires pinned content (unit %d)", ev.UnitID)}
		}
		if _, err := contexthub.SaveContextTx(tx, ev.Layer, ev.LayerTarget, ev.Body, decidedBy, note); err != nil {
			if err == contexthub.ErrSecretInContent {
				return errPermanentApply{err}
			}
			return err
		}
		return nil
	case learn.KindRule:
		return applyKnowledgeRuleWriteTx(tx, ev)
	case learn.KindPlaybook:
		return applyKnowledgePlaybookWriteTx(tx, ev, decidedBy)
	case learn.KindSkill:
		// Body+content_hash are already pinned on the unit row by NominateUnit
		// (P1) — publish only freezes state (the transition below) and the
		// audit hash after commit; no additional write needed here.
		if ev.Body == "" || ev.ContentHash == "" {
			return errPermanentApply{fmt.Errorf("skill publish requires pinned body+content_hash (unit %d)", ev.UnitID)}
		}
		return nil
	default:
		return errPermanentApply{fmt.Errorf("unknown knowledge kind %q (unit %d)", ev.Kind, ev.UnitID)}
	}
}

// applyKnowledgeRuleWrite gates a new-or-updated guardrail rule into effect,
// switching on the PINNED rule_intent (H1) rather than always enabling:
// ref_id > 0 covers the two lifecycle signals detectRuleLifecycle nominates
// against an EXISTING rule ("retire" → disable, "scope-up" → widen to
// global) plus the plain "enable" toggle-on for any other ref_id>0
// candidate; ref_id == 0 means the rule text itself was pinned onto the unit
// at nominate time (ev.Body carries a "kind\tpattern\tdescription" triple —
// a plain string body, same body field skill uses, kept simple rather than
// adding a second params shape) and a fresh row is inserted enabled — a
// brand-new rule has nothing to retire or scope-up, so intent is irrelevant
// there.
func applyKnowledgeRuleWriteTx(tx *sql.Tx, ev knowledgePublishParams) error {
	if ev.RefID > 0 {
		switch ev.RuleIntent {
		case learn.RuleIntentRetire:
			res, err := tx.Exec(`UPDATE guardrail_rules SET enabled = 0 WHERE id = ?`, ev.RefID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errPermanentApply{fmt.Errorf("rule retire: guardrail_rules id %d not found (unit %d)", ev.RefID, ev.UnitID)}
			}
			return nil
		case learn.RuleIntentScopeUp:
			res, err := tx.Exec(`UPDATE guardrail_rules SET scope_type = 'global' WHERE id = ?`, ev.RefID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errPermanentApply{fmt.Errorf("rule scope-up: guardrail_rules id %d not found (unit %d)", ev.RefID, ev.UnitID)}
			}
			return nil
		default: // "enable" or unset (backward-compat) — plain toggle-on
			res, err := tx.Exec(`UPDATE guardrail_rules SET enabled = 1 WHERE id = ?`, ev.RefID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errPermanentApply{fmt.Errorf("rule publish: guardrail_rules id %d not found (unit %d)", ev.RefID, ev.UnitID)}
			}
			return nil
		}
	}
	if ev.Body == "" {
		return errPermanentApply{fmt.Errorf("rule publish requires ref_id or pinned rule body (unit %d)", ev.UnitID)}
	}
	kind, pattern, description, err := parseRuleBody(ev.Body)
	if err != nil {
		return errPermanentApply{fmt.Errorf("rule publish: %w (unit %d)", err, ev.UnitID)}
	}
	_, err = tx.Exec(`INSERT INTO guardrail_rules(kind, pattern, description, enabled) VALUES(?, ?, ?, 1)`,
		kind, pattern, description)
	return err
}

// parseRuleBody splits a nominated rule's pinned body ("kind\tpattern\t
// description") — the simplest shape that fits inside the unit's existing
// Body/content_hash fields without a new column, matching skill's use of the
// same field for its own full text.
func parseRuleBody(body string) (kind, pattern, description string, err error) {
	parts := splitN3(body, '\t')
	if parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("rule body must be \"kind\\tpattern\\tdescription\"")
	}
	return parts[0], parts[1], parts[2], nil
}

func splitN3(s string, sep rune) [3]string {
	var out [3]string
	idx := 0
	start := 0
	for i, r := range s {
		if idx >= 2 {
			break
		}
		if r == sep {
			out[idx] = s[start:i]
			idx++
			start = i + 1
		}
	}
	out[idx] = s[start:]
	return out
}

// applyKnowledgePlaybookWriteTx creates the REAL playbooks row for the first
// time — PromoteCandidate (P1 fix) only ever nominates a knowledge_units row;
// this is now the sole path that inserts into playbooks, gated behind human
// review. Both writes share the caller's tx (H2) so a failure on the second
// leaves no orphan playbooks row.
func applyKnowledgePlaybookWriteTx(tx *sql.Tx, ev knowledgePublishParams, decidedBy string) error {
	res, err := tx.Exec(`INSERT INTO playbooks(name, created_at, created_by) VALUES(?, ?, ?)`,
		ev.Name, store.Now(), decidedBy)
	if err != nil {
		return err
	}
	pbID, _ := res.LastInsertId()
	if _, err := tx.Exec(`UPDATE knowledge_units SET ref_kind = 'playbook', ref_id = ? WHERE id = ?`,
		pbID, ev.UnitID); err != nil {
		return err
	}
	return nil
}

// applyKnowledgeMandate flips a published unit's required flag to 1
// (compliance-visibility ON, §"Mandate = compliance visibility") — it never
// touches state (a mandated unit stays published/adopted/measured) and never
// pushes/refuses anything: SessionStart's LOCAL compliance-notice check
// (hook_context.go) is the only consumer of required=1, and it is fail-open
// by construction. The applier re-checks the live unit at apply time (F5,
// same pattern as applyKnowledgePublish): a unit that moved to
// retired/rejected/superseded between request and approval must not be
// silently mandated.
func applyKnowledgeMandate(st *store.Store, evidence string, insightID int64, decidedBy string) error {
	unitID, kind, name, err := parseUnitActionEvidence(evidence, insightID)
	if err != nil {
		return err
	}
	unit, err := learn.GetUnit(st, unitID)
	if err != nil {
		return err // transient — retry
	}
	if unit == nil {
		return errPermanentApply{fmt.Errorf("insight %d: unit %d not found", insightID, unitID)}
	}
	switch unit.State {
	case learn.StatePublished, learn.StateAdopted, learn.StateMeasured:
	default:
		return errPermanentApply{fmt.Errorf("insight %d: unit %d in state %q, expected a live published state — cannot mandate",
			insightID, unitID, unit.State)}
	}

	tx, err := st.DB.Begin()
	if err != nil {
		return err // transient — retry
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE knowledge_units SET required = 1, updated_at = ? WHERE id = ?`, store.Now(), unitID); err != nil {
		return err
	}
	note := fmt.Sprintf("knowledge-mandate duyệt #%d", insightID)
	if err := recordUnitNoteTx(tx, unitID, unit.State, unit.State, decidedBy, note); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err // transient — retry
	}

	a := &govern.Audit{St: st, Actor: decidedBy}
	_, err = a.Append("knowledge_mandated", kind+":"+name, fmt.Sprintf("%s %q bắt buộc compliance (insight #%d)", kind, name, insightID))
	return err
}

// applyKnowledgeRetire moves a unit to retired AND clears required (F13: the
// compliance notice must stop the instant a mandate is retired — a mandated-
// then-retired skill's required flag staying 1 would keep SessionStart
// flagging engineers against a unit nobody can act on anymore). F13
// (documented, not enforced in code — Dandori has no channel to touch an
// engineer's machine): files already pulled to a developer's local
// .claude/skills/<name>/ tree are LEFT ALONE. The engineer owns that
// checkout; retiring the knowledge unit only stops the compliance nudge, it
// never un-pulls or deletes anything on disk. kind=context additionally
// triggers a gated rollback: the layer/layer_target this unit published to
// is rolled back to the version immediately before the unit's own publish
// (best-effort — a missing prior version or a layer/target unit never
// actually published to leaves the retire itself intact, since retire's
// primary job — stop the mandate nudge — must not be blocked by a rollback
// nicety failing).
func applyKnowledgeRetire(st *store.Store, evidence string, insightID int64, decidedBy string) error {
	unitID, kind, name, err := parseUnitActionEvidence(evidence, insightID)
	if err != nil {
		return err
	}
	unit, err := learn.GetUnit(st, unitID)
	if err != nil {
		return err // transient — retry
	}
	if unit == nil {
		return errPermanentApply{fmt.Errorf("insight %d: unit %d not found", insightID, unitID)}
	}
	switch unit.State {
	case learn.StatePublished, learn.StateAdopted, learn.StateMeasured:
	default:
		return errPermanentApply{fmt.Errorf("insight %d: unit %d in state %q, expected a live published state — cannot retire",
			insightID, unitID, unit.State)}
	}

	tx, err := st.DB.Begin()
	if err != nil {
		return err // transient — retry
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE knowledge_units SET state = ?, required = 0, updated_at = ? WHERE id = ?`,
		learn.StateRetired, store.Now(), unitID); err != nil {
		return err
	}
	note := fmt.Sprintf("knowledge-retire duyệt #%d", insightID)
	if err := recordUnitNoteTx(tx, unitID, unit.State, learn.StateRetired, decidedBy, note); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err) {
			return errPermanentApply{fmt.Errorf("insight %d: %w", insightID, err)}
		}
		return err // transient — retry
	}

	if kind == learn.KindContext && unit.Layer != "" && unit.LayerTarget != "" {
		rollbackRetiredContext(st, unit, decidedBy, insightID)
	}

	a := &govern.Audit{St: st, Actor: decidedBy}
	_, err = a.Append("knowledge_retired", kind+":"+name,
		fmt.Sprintf("%s %q retired (insight #%d) — file đã pull trên máy engineer GIỮ NGUYÊN, không un-pull", kind, name, insightID))
	return err
}

// rollbackRetiredContext is the gated, best-effort context-kind side effect
// of a retire (§"Retire/rollback" — kind=context với rollback param →
// contexthub.Rollback gated). It never uses contexthub.Rollback directly
// (that helper opens its own tx via SaveContext, and st.DB has exactly one
// open connection — nested Begin() calls after applyKnowledgeRetire's own
// tx already committed would still be fine on their own, but composing two
// independent top-level txs here would leave a window where the retire
// commits without the rollback landing on a crash; reading the prior
// version and writing it back through SaveContextTx WOULD need its own tx
// anyway, so this runs as a clearly-separate best-effort step instead,
// exactly like every other "gated side effect after commit" in this file —
// e.g. the audit append). Best-effort: any failure here (no prior version,
// content mismatch, DB error) is logged to the audit trail as a distinct
// action rather than failing the whole retire — the retire's core guarantee
// (mandate notice stops) must land regardless.
func rollbackRetiredContext(st *store.Store, unit *learn.KnowledgeUnit, decidedBy string, insightID int64) {
	hub := contexthub.New(st)
	head, err := hub.Head(unit.Layer, unit.LayerTarget)
	a := &govern.Audit{St: st, Actor: decidedBy}
	if err != nil || head == nil || head.VersionN <= 1 {
		_, _ = a.Append("knowledge_retire_rollback_skipped", unit.Kind+":"+unit.Name,
			fmt.Sprintf("no prior version to roll back to for %s/%s (insight #%d)", unit.Layer, unit.LayerTarget, insightID))
		return
	}
	prior, err := hub.Version(unit.Layer, unit.LayerTarget, head.VersionN-1)
	if err != nil || prior == nil {
		_, _ = a.Append("knowledge_retire_rollback_skipped", unit.Kind+":"+unit.Name,
			fmt.Sprintf("prior version v%d unreadable for %s/%s (insight #%d)", head.VersionN-1, unit.Layer, unit.LayerTarget, insightID))
		return
	}
	if _, err := hub.SaveContext(unit.Layer, unit.LayerTarget, prior.Content, decidedBy,
		fmt.Sprintf("rollback → v%d (retire tri thức #%d)", prior.VersionN, insightID)); err != nil {
		_, _ = a.Append("knowledge_retire_rollback_failed", unit.Kind+":"+unit.Name,
			fmt.Sprintf("rollback write failed for %s/%s (insight #%d): %v", unit.Layer, unit.LayerTarget, insightID, err))
		return
	}
	_, _ = a.Append("knowledge_retire_rollback", unit.Kind+":"+unit.Name,
		fmt.Sprintf("%s/%s rolled back → v%d (retire tri thức #%d)", unit.Layer, unit.LayerTarget, prior.VersionN, insightID))
}

// parseUnitActionEvidence decodes the shared unitActionParams JSON shape
// (knowledge_units_actions.go) that RequestMandate/RequestRetire both pin at
// request time — mandate/retire only ever need unit_id/kind/name, so this
// stays intentionally narrower than knowledgePublishParams.
func parseUnitActionEvidence(evidence string, insightID int64) (unitID int64, kind, name string, err error) {
	var ev struct {
		UnitID int64  `json:"unit_id"`
		Kind   string `json:"kind"`
		Name   string `json:"name"`
	}
	if e := json.Unmarshal([]byte(evidence), &ev); e != nil {
		return 0, "", "", errPermanentApply{e}
	}
	if ev.UnitID == 0 || ev.Kind == "" || ev.Name == "" {
		return 0, "", "", errPermanentApply{fmt.Errorf("insight %d: invalid knowledge-mandate/retire params", insightID)}
	}
	return ev.UnitID, ev.Kind, ev.Name, nil
}

// recordUnitNoteTx writes a knowledge_transitions row sharing the caller's
// tx (H2) — for mandate, from==to (the required flag changed, not state);
// for retire, a real state transition. Mirrors learn's own unexported
// recordTransitionTx (internal/learn is P1/P2-owned this round — same
// rationale as publishUnitTransitionTx above: the table shape is the stable
// migration 016 contract, not an internal learn-package detail).
func recordUnitNoteTx(tx *sql.Tx, unitID int64, from, to, actor, note string) error {
	_, err := tx.Exec(`INSERT INTO knowledge_transitions(unit_id, from_state, to_state, actor, note, at)
		VALUES(?, ?, ?, ?, ?, ?)`, unitID, from, to, actor, note, store.Now())
	return err
}

// publishUnitTransitionTx moves a unit from in_review to published and
// records the transition row, sharing the caller's tx (H2 — mirrors learn's
// own unexported `transition` helper; internal/learn is P1/P2-owned this
// round, so this package cannot add an exported sibling there; the table
// shapes are the stable migration 016 contract, not an internal
// learn-package detail). The from-state check happens again here, inside the
// tx, so a concurrent transition can't race past what applyKnowledgePublish
// already checked before its own writes.
func publishUnitTransitionTx(tx *sql.Tx, unitID int64, actor, note string) error {
	var cur string
	if err := tx.QueryRow(`SELECT state FROM knowledge_units WHERE id = ?`, unitID).Scan(&cur); err != nil {
		return err
	}
	if cur != learn.StateInReview {
		return errPermanentApply{fmt.Errorf("%w: unit %d in state %q, expected %q — concurrent transition",
			learn.ErrStateMismatch, unitID, cur, learn.StateInReview)}
	}
	if _, err := tx.Exec(`UPDATE knowledge_units SET state = ?, updated_at = ? WHERE id = ?`,
		learn.StatePublished, store.Now(), unitID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO knowledge_transitions(unit_id, from_state, to_state, actor, note, at)
		VALUES(?, ?, ?, ?, ?, ?)`, unitID, learn.StateInReview, learn.StatePublished, actor, note, store.Now()); err != nil {
		return err
	}
	return nil
}
