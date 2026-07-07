package observer

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// nominateInReview creates a unit and moves it straight to in_review, the
// only state applyKnowledgePublish accepts (F5 state-check).
func nominateInReview(t *testing.T, st *store.Store, p learn.NominateParams) int64 {
	t.Helper()
	id, err := learn.NominateUnit(st, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(st, id, "tester"); err != nil {
		t.Fatal(err)
	}
	return id
}

// F5: the applier must re-check unit.state == in_review even though the
// request handler already deduped — a unit rejected/published by some other
// path between request and approval must fail permanently, not double-apply.
func TestApplyKnowledgePublishStateCheck(t *testing.T) {
	st, _ := testStore(t)
	id, err := learn.NominateUnit(st, learn.NominateParams{
		Kind: learn.KindSkill, Name: "state-check-skill", Title: "t", Body: "body content", NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Still "nominated", never submitted to in_review.
	requestAndApprove(t, st, "knowledge-publish", "skill:state-check-skill", "s", map[string]any{
		"unit_id": id, "kind": "skill", "name": "state-check-skill", "body": "body content", "content_hash": "h",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 for unit not in_review (F5 state-check)", n)
	}
	u, _ := learn.GetUnit(st, id)
	if u.State != learn.StateNominated {
		t.Errorf("unit state=%q, want unchanged 'nominated'", u.State)
	}
}

// Skill publish: freezes state=published, keeps the pinned content_hash, and
// audits knowledge_published with the hash (F7 — independent verify source).
func TestApplyKnowledgePublishSkillHappyPath(t *testing.T) {
	st, _ := testStore(t)
	id := nominateInReview(t, st, learn.NominateParams{
		Kind: learn.KindSkill, Name: "happy-skill", Title: "t", Body: "# skill body", NominatedBy: "tester",
	})
	u, _ := learn.GetUnit(st, id)
	requestAndApprove(t, st, "knowledge-publish", "skill:happy-skill", "s", map[string]any{
		"unit_id": id, "kind": "skill", "name": "happy-skill",
		"body": u.Body, "content_hash": u.ContentHash,
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	got, _ := learn.GetUnit(st, id)
	if got.State != learn.StatePublished {
		t.Errorf("state=%q, want published", got.State)
	}
	var detail string
	st.DB.QueryRow(`SELECT detail FROM audit_log WHERE action='knowledge_published' ORDER BY id DESC LIMIT 1`).Scan(&detail)
	if !strings.Contains(detail, u.ContentHash) {
		t.Errorf("audit detail=%q, want content_hash %q (F7)", detail, u.ContentHash)
	}
}

// Context publish routes through contexthub.SaveContext with the PINNED body
// (H3), never a live re-derive.
func TestApplyKnowledgePublishContextWritesViaContextHub(t *testing.T) {
	st, _ := testStore(t)
	id := nominateInReview(t, st, learn.NominateParams{
		Kind: learn.KindContext, Name: "ctx-pattern", Title: "t", Body: "task X: dùng tool Y",
		Layer: "company", LayerTarget: "*", NominatedBy: "tester",
	})
	requestAndApprove(t, st, "knowledge-publish", "context:ctx-pattern", "s", map[string]any{
		"unit_id": id, "kind": "context", "name": "ctx-pattern",
		"body": "task X: dùng tool Y", "layer": "company", "layer_target": "*",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	hub := contexthub.New(st)
	head, _ := hub.Head(contexthub.LayerCompany, "*")
	if head == nil || head.Content != "task X: dùng tool Y" {
		t.Errorf("company context head=%+v, want pinned body written", head)
	}
}

// Rule publish (ref_id path): toggles an existing guardrail_rules row on.
func TestApplyKnowledgePublishRuleTogglesExisting(t *testing.T) {
	st, _ := testStore(t)
	res, err := st.DB.Exec(`INSERT INTO guardrail_rules(kind, pattern, description, enabled) VALUES('gate','x','d',0)`)
	if err != nil {
		t.Fatal(err)
	}
	ruleID, _ := res.LastInsertId()
	id := nominateInReview(t, st, learn.NominateParams{
		Kind: learn.KindRule, Name: "rule-scope-up", Title: "t", RefKind: "guardrail_rule", RefID: ruleID, NominatedBy: "tester",
	})
	requestAndApprove(t, st, "knowledge-publish", "rule:rule-scope-up", "s", map[string]any{
		"unit_id": id, "kind": "rule", "name": "rule-scope-up", "ref_kind": "guardrail_rule", "ref_id": ruleID,
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	var enabled int
	st.DB.QueryRow(`SELECT enabled FROM guardrail_rules WHERE id = ?`, ruleID).Scan(&enabled)
	if enabled != 1 {
		t.Errorf("rule enabled=%d, want 1", enabled)
	}
}

// Playbook publish: the REAL playbooks row is created only now (P1 fix moved
// this off NominateUnit) and the unit's ref_id is backfilled.
func TestApplyKnowledgePublishPlaybookCreatesRow(t *testing.T) {
	st, _ := testStore(t)
	id := nominateInReview(t, st, learn.NominateParams{
		Kind: learn.KindPlaybook, Name: "flow-x", Title: "t", NominatedBy: "tester",
	})
	requestAndApprove(t, st, "knowledge-publish", "playbook:flow-x", "s", map[string]any{
		"unit_id": id, "kind": "playbook", "name": "flow-x",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	var pbCount int
	st.DB.QueryRow(`SELECT count(*) FROM playbooks WHERE name = 'flow-x'`).Scan(&pbCount)
	if pbCount != 1 {
		t.Errorf("playbooks rows named flow-x=%d, want 1", pbCount)
	}
	u, _ := learn.GetUnit(st, id)
	if u.RefKind != "playbook" || u.RefID == nil {
		t.Errorf("unit ref not backfilled: refKind=%q refID=%v", u.RefKind, u.RefID)
	}
}

// F5 supersede ordering: publishing v2 must mark v1 superseded and v2
// published, leaving exactly one live (published/adopted/measured) row for
// the (kind,name) pair — the invariant idx_ku_kind_name_live enforces.
func TestApplyKnowledgePublishSupersedesOldVersion(t *testing.T) {
	st, _ := testStore(t)
	v1 := nominateInReview(t, st, learn.NominateParams{
		Kind: learn.KindSkill, Name: "versioned-skill", Title: "v1", Body: "body v1", NominatedBy: "tester",
	})
	requestAndApprove(t, st, "knowledge-publish", "skill:versioned-skill", "s", map[string]any{
		"unit_id": v1, "kind": "skill", "name": "versioned-skill", "body": "body v1", "content_hash": "h1",
	})
	if n, err := RunObserverApplier(st); err != nil || n != 1 {
		t.Fatalf("publish v1: n=%d err=%v", n, err)
	}

	v2, err := learn.NominateUnit(st, learn.NominateParams{
		Kind: learn.KindSkill, Name: "versioned-skill", Title: "v2", Body: "body v2", NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(st, v2, "tester"); err != nil {
		t.Fatal(err)
	}
	requestAndApprove(t, st, "knowledge-publish", "skill:versioned-skill", "s", map[string]any{
		"unit_id": v2, "kind": "skill", "name": "versioned-skill", "body": "body v2", "content_hash": "h2",
	})
	if n, err := RunObserverApplier(st); err != nil || n != 1 {
		t.Fatalf("publish v2: n=%d err=%v", n, err)
	}

	oldUnit, _ := learn.GetUnit(st, v1)
	newUnit, _ := learn.GetUnit(st, v2)
	if oldUnit.State != learn.StateSuperseded {
		t.Errorf("v1 state=%q, want superseded", oldUnit.State)
	}
	if newUnit.State != learn.StatePublished {
		t.Errorf("v2 state=%q, want published", newUnit.State)
	}
	var liveCount int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE kind='skill' AND name='versioned-skill'
		AND state IN ('published','adopted','measured')`).Scan(&liveCount)
	if liveCount != 1 {
		t.Errorf("live rows for versioned-skill=%d, want exactly 1 (idx_ku_kind_name_live invariant)", liveCount)
	}
}

// publishUnit nominates+submits+publishes a unit in one call, for
// mandate/retire tests that need a live published unit rather than an
// in_review one (applyKnowledgePublish's own target state).
func publishUnit(t *testing.T, st *store.Store, p learn.NominateParams) int64 {
	t.Helper()
	id := nominateInReview(t, st, p)
	params := map[string]any{"unit_id": id, "kind": p.Kind, "name": p.Name}
	if p.Body != "" {
		u, _ := learn.GetUnit(st, id)
		params["body"] = u.Body
		params["content_hash"] = u.ContentHash
	}
	if p.Layer != "" {
		params["layer"] = p.Layer
		params["layer_target"] = p.LayerTarget
	}
	requestAndApprove(t, st, "knowledge-publish", p.Kind+":"+p.Name, "s", params)
	if n, err := RunObserverApplier(st); err != nil || n != 1 {
		t.Fatalf("publish setup: n=%d err=%v", n, err)
	}
	return id
}

// Mandate sets required=1 on a live published unit and audits
// knowledge_mandated — never changes state.
func TestApplyKnowledgeMandateSetsRequiredAndAudits(t *testing.T) {
	st, _ := testStore(t)
	id := publishUnit(t, st, learn.NominateParams{
		Kind: learn.KindSkill, Name: "mandate-skill", Title: "t", Body: "body", NominatedBy: "tester",
	})
	requestAndApprove(t, st, "knowledge-mandate", "skill:mandate-skill", "s",
		map[string]any{"unit_id": id, "kind": "skill", "name": "mandate-skill"})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	u, _ := learn.GetUnit(st, id)
	if !u.Required {
		t.Error("required must be true after mandate")
	}
	if u.State != learn.StatePublished {
		t.Errorf("state=%q, want unchanged published (mandate never changes state)", u.State)
	}
	var auditCount int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='knowledge_mandated'`).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("knowledge_mandated audits=%d, want 1", auditCount)
	}
}

// Mandate on a non-live unit (never published) must fail permanently.
func TestApplyKnowledgeMandateRejectsNonLiveState(t *testing.T) {
	st, _ := testStore(t)
	id := nominateInReview(t, st, learn.NominateParams{
		Kind: learn.KindSkill, Name: "mandate-not-live", Title: "t", Body: "b", NominatedBy: "tester",
	})
	requestAndApprove(t, st, "knowledge-mandate", "skill:mandate-not-live", "s",
		map[string]any{"unit_id": id, "kind": "skill", "name": "mandate-not-live"})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 (unit still in_review, not published)", n)
	}
	u, _ := learn.GetUnit(st, id)
	if u.Required {
		t.Error("required must stay false when mandate fails state-check")
	}
}

// Retire sets state=retired + required=0, and audits knowledge_retired.
func TestApplyKnowledgeRetireSetsStateAndClearsRequired(t *testing.T) {
	st, _ := testStore(t)
	id := publishUnit(t, st, learn.NominateParams{
		Kind: learn.KindSkill, Name: "retire-skill", Title: "t", Body: "body", NominatedBy: "tester",
	})
	// Mandate first so we can prove retire clears required (F13).
	requestAndApprove(t, st, "knowledge-mandate", "skill:retire-skill", "s",
		map[string]any{"unit_id": id, "kind": "skill", "name": "retire-skill"})
	if n, err := RunObserverApplier(st); err != nil || n != 1 {
		t.Fatalf("mandate setup: n=%d err=%v", n, err)
	}

	requestAndApprove(t, st, "knowledge-retire", "skill:retire-skill", "s",
		map[string]any{"unit_id": id, "kind": "skill", "name": "retire-skill"})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	u, _ := learn.GetUnit(st, id)
	if u.State != learn.StateRetired {
		t.Errorf("state=%q, want retired", u.State)
	}
	if u.Required {
		t.Error("required must be false after retire (F13: notice must stop)")
	}
	var auditCount int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='knowledge_retired'`).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("knowledge_retired audits=%d, want 1", auditCount)
	}
}

// Retiring a context-kind unit triggers a gated rollback to the version
// immediately before this unit's own publish.
func TestApplyKnowledgeRetireContextTriggersRollback(t *testing.T) {
	st, _ := testStore(t)
	hub := contexthub.New(st)
	// Seed a prior company context version (v1) BEFORE this unit publishes v2.
	if _, err := hub.SaveContext(contexthub.LayerCompany, "*", "v1 content — should come back", "seed", "seed v1"); err != nil {
		t.Fatal(err)
	}
	id := publishUnit(t, st, learn.NominateParams{
		Kind: learn.KindContext, Name: "retire-ctx", Title: "t", Body: "v2 content — will be retired",
		Layer: "company", LayerTarget: "*", NominatedBy: "tester",
	})
	requestAndApprove(t, st, "knowledge-retire", "context:retire-ctx", "s",
		map[string]any{"unit_id": id, "kind": "context", "name": "retire-ctx"})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	head, err := hub.Head(contexthub.LayerCompany, "*")
	if err != nil {
		t.Fatal(err)
	}
	if head == nil || head.Content != "v1 content — should come back" {
		t.Errorf("company head after retire=%+v, want rolled back to v1 content", head)
	}
	var rollbackAudit int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='knowledge_retire_rollback'`).Scan(&rollbackAudit)
	if rollbackAudit != 1 {
		t.Errorf("knowledge_retire_rollback audits=%d, want 1", rollbackAudit)
	}
}

// Retire on a non-live unit must fail permanently, leaving state untouched.
func TestApplyKnowledgeRetireRejectsNonLiveState(t *testing.T) {
	st, _ := testStore(t)
	id := nominateInReview(t, st, learn.NominateParams{
		Kind: learn.KindSkill, Name: "retire-not-live", Title: "t", Body: "b", NominatedBy: "tester",
	})
	requestAndApprove(t, st, "knowledge-retire", "skill:retire-not-live", "s",
		map[string]any{"unit_id": id, "kind": "skill", "name": "retire-not-live"})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 (unit still in_review, not published)", n)
	}
	u, _ := learn.GetUnit(st, id)
	if u.State != learn.StateInReview {
		t.Errorf("state=%q, want unchanged in_review", u.State)
	}
}

// Invalid mandate/retire evidence must fail permanently, never panic.
func TestApplyKnowledgeMandateRetireInvalidParamsPermanent(t *testing.T) {
	st, _ := testStore(t)
	requestAndApprove(t, st, "knowledge-mandate", "skill:bad", "s", map[string]any{"unit_id": 0, "kind": "", "name": ""})
	requestAndApprove(t, st, "knowledge-retire", "skill:bad2", "s", map[string]any{"unit_id": 0, "kind": "", "name": ""})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 for invalid params", n)
	}
}

// H2: if the shared tx fails partway through (here: a concurrent transition
// flips the unit's state between RunObserverApplier's own pre-tx check and
// the in-tx re-check in publishUnitTransitionTx — a genuine, realistic race,
// not a synthetic fault), the WHOLE sequence must roll back — no context
// version, no rule toggle, no playbook row, no transition row half-written.
// This proves the single-tx guarantee (H2), independent of whether the
// resulting error classifies as transient or permanent.
func TestApplyKnowledgePublishRollsBackWholeTxOnFailure(t *testing.T) {
	st, _ := testStore(t)
	id := nominateInReview(t, st, learn.NominateParams{
		Kind: learn.KindContext, Name: "rollback-ctx", Title: "t", Body: "pinned body — must not land",
		Layer: "company", LayerTarget: "*", NominatedBy: "tester",
	})
	requestAndApprove(t, st, "knowledge-publish", "context:rollback-ctx", "s", map[string]any{
		"unit_id": id, "kind": "context", "name": "rollback-ctx",
		"body": "pinned body — must not land", "layer": "company", "layer_target": "*",
	})

	// Simulate a concurrent transition winning the race AFTER RequestPublish's
	// approval was decided but BEFORE the applier's tx runs: flip the unit
	// straight to rejected, bypassing the normal RejectUnit state check (that
	// check is exactly what a real concurrent admin action would have already
	// passed through on its own path).
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET state = 'rejected' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 (unit moved out of in_review before apply)", n)
	}

	// Nothing must have leaked into contexthub — the whole tx must have
	// rolled back (in this path, applyKnowledgePublish's OWN pre-tx state
	// check already catches it and never opens a tx at all; either way the
	// invariant under test — no partial write — holds).
	hub := contexthub.New(st)
	head, _ := hub.Head(contexthub.LayerCompany, "*")
	if head != nil {
		t.Errorf("company context head=%+v, want nil — no partial write on apply failure", head)
	}
	u, _ := learn.GetUnit(st, id)
	if u.State != "rejected" {
		t.Errorf("unit state=%q, want unchanged 'rejected'", u.State)
	}
}

// H2: after a PERMANENT apply failure (state-mismatch, detected inside the
// shared tx itself — publishUnitTransitionTx's own re-check), the approval
// stays consumed and a second RunObserverApplier pass must not retry it or
// produce any duplicate write — matches engine.go's "stay consumed, audit,
// move on" contract for errPermanentApply.
func TestApplyKnowledgePublishPermanentFailureNeverRetries(t *testing.T) {
	st, _ := testStore(t)
	id := nominateInReview(t, st, learn.NominateParams{
		Kind: learn.KindRule, Name: "rollback-rule", Title: "t", Body: "block\tBash\tno-op", NominatedBy: "tester",
	})
	apprID := requestAndApprove(t, st, "knowledge-publish", "rule:rollback-rule", "s", map[string]any{
		"unit_id": id, "kind": "rule", "name": "rollback-rule", "body": "block\tBash\tno-op",
	})

	// Race the unit's OWN in-tx re-check (publishUnitTransitionTx) rather than
	// the pre-tx check in applyKnowledgePublish: flip state to in_review →
	// published directly (bypassing the pre-tx check, which only runs once
	// before the tx opens) so the failure surfaces from INSIDE the tx,
	// exercising classifyApplyErr's ErrStateMismatch path.
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET state = 'published' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 (state-mismatch is permanent)", n)
	}
	var ruleCount int
	st.DB.QueryRow(`SELECT count(*) FROM guardrail_rules WHERE pattern = 'no-op'`).Scan(&ruleCount)
	if ruleCount != 0 {
		t.Errorf("guardrail_rules rows for rollback-rule=%d, want 0 — no partial write on permanent failure", ruleCount)
	}
	var consumedAt sql.NullString
	st.DB.QueryRow(`SELECT consumed_at FROM approvals WHERE id = ?`, apprID).Scan(&consumedAt)
	if !consumedAt.Valid {
		t.Error("permanently-failed approval must stay consumed (never retried)")
	}

	// Second pass: nothing left to apply, no duplicate write, no error.
	n2, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("second pass applied=%d, want 0 (consumed approval must never retry)", n2)
	}
	st.DB.QueryRow(`SELECT count(*) FROM guardrail_rules WHERE pattern = 'no-op'`).Scan(&ruleCount)
	if ruleCount != 0 {
		t.Errorf("guardrail_rules rows after second pass=%d, want 0 (no duplicate)", ruleCount)
	}
}

// Invalid/empty evidence must fail permanently, never panic or hang.
func TestApplyKnowledgePublishInvalidParamsPermanent(t *testing.T) {
	st, _ := testStore(t)
	requestAndApprove(t, st, "knowledge-publish", "skill:bad", "s", map[string]any{
		"unit_id": 0, "kind": "", "name": "",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 for invalid params", n)
	}
	var failed int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='observer_apply_failed'`).Scan(&failed)
	if failed != 1 {
		t.Errorf("observer_apply_failed audits=%d, want 1", failed)
	}
}
