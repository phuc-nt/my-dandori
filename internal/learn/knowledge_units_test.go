package learn

import (
	"errors"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

func seedCtxUnit(t *testing.T, st *store.Store) int64 {
	t.Helper()
	id, err := NominateUnit(st, NominateParams{
		Kind: KindContext, Name: "handoff-notes", Title: "Handoff notes rule",
		RefKind: "context_version", RefID: 1, NominatedBy: "dandori-observer",
		ProvenanceRun: []string{"r1", "r2"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return id
}

func TestNominateSubmitReject(t *testing.T) {
	st := testStore(t)
	id := seedCtxUnit(t, st)

	u, err := GetUnit(st, id)
	if err != nil || u == nil {
		t.Fatalf("GetUnit: %+v err=%v", u, err)
	}
	if u.State != StateNominated || u.VersionN != 1 || u.SupersedesID != nil {
		t.Errorf("fresh nominate: %+v", u)
	}

	if err := SubmitForReview(st, id, "admin"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	u, _ = GetUnit(st, id)
	if u.State != StateInReview {
		t.Errorf("after submit: state=%s", u.State)
	}

	if err := RejectUnit(st, id, "admin", "not ready"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	u, _ = GetUnit(st, id)
	if u.State != StateRejected {
		t.Errorf("after reject: state=%s", u.State)
	}

	// Transitions recorded: detected->nominated, nominated->in_review, in_review->rejected.
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_transitions WHERE unit_id = ?`, id).Scan(&n)
	if n != 3 {
		t.Errorf("transitions recorded: %d, want 3", n)
	}

	// Wrong from-state transition must fail (already rejected).
	if err := SubmitForReview(st, id, "admin"); err == nil {
		t.Error("submit on rejected unit must fail")
	}
}

// TestNominateOriginRoundTrip is the M3 lockstep proof: unitSelectCols +
// KnowledgeUnit struct fields + scanUnit's positional Scan targets must all
// agree on column order/count, or this panics on Scan instead of merely
// failing an assertion. Must be green before anything else in v13 P2 reads a
// unit.
func TestNominateOriginRoundTrip(t *testing.T) {
	st := testStore(t)

	// Explicit origin + origin_model round-trips exactly.
	id, err := NominateUnit(st, NominateParams{
		Kind: KindSkill, Name: "draft-skill", Title: "Drafted skill",
		Body: "# body", NominatedBy: "operator1",
		Origin: "ai-draft", OriginModel: "anthropic/claude-3.5",
	})
	if err != nil {
		t.Fatalf("nominate: %v", err)
	}
	u, err := GetUnit(st, id)
	if err != nil || u == nil {
		t.Fatalf("GetUnit: %+v err=%v", u, err)
	}
	if u.Origin != "ai-draft" {
		t.Errorf("Origin = %q, want ai-draft", u.Origin)
	}
	if u.OriginModel != "anthropic/claude-3.5" {
		t.Errorf("OriginModel = %q, want anthropic/claude-3.5", u.OriginModel)
	}
	// Every OTHER field must still line up (proves the column-count lockstep,
	// not just that Origin happens to read back correctly).
	if u.Title != "Drafted skill" || u.Body != "# body" || u.NominatedBy != "operator1" {
		t.Errorf("other fields shifted after adding origin cols: %+v", u)
	}

	// Empty Origin resolves to "human" (DB-default-equivalent), never blank
	// or NULL-scanned-as-zero-value-that-happens-to-look-right.
	id2, err := NominateUnit(st, NominateParams{
		Kind: KindContext, Name: "plain-note", Title: "Plain",
		RefKind: "context_version", RefID: 1, NominatedBy: "operator2",
	})
	if err != nil {
		t.Fatalf("nominate2: %v", err)
	}
	u2, _ := GetUnit(st, id2)
	if u2.Origin != "human" {
		t.Errorf("default Origin = %q, want human", u2.Origin)
	}
	if u2.OriginModel != "" {
		t.Errorf("default OriginModel = %q, want empty", u2.OriginModel)
	}

	// ListUnits must scan the same way as GetUnit (both call scanUnit).
	units, err := ListUnits(st, StateNominated)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	found := map[int64]string{}
	for _, uu := range units {
		found[uu.ID] = uu.Origin
	}
	if found[id] != "ai-draft" || found[id2] != "human" {
		t.Errorf("ListUnits origin mismatch: %+v", found)
	}
}

func TestNominateInvalidSlugRejected(t *testing.T) {
	st := testStore(t)
	for _, bad := range []string{"Has-Upper", "-leading-dash", "has space", "under_score", ""} {
		_, err := NominateUnit(st, NominateParams{
			Kind: KindContext, Name: bad, Title: "t",
			RefKind: "context_version", RefID: 1, NominatedBy: "x",
		})
		if err == nil {
			t.Errorf("slug %q should be rejected", bad)
		}
	}
}

func TestNominateBodyTooLargeRejected(t *testing.T) {
	st := testStore(t)
	big := strings.Repeat("a", MaxUnitBodySize+1)
	_, err := NominateUnit(st, NominateParams{
		Kind: KindSkill, Name: "big-skill", Title: "Big", Body: big, NominatedBy: "x",
	})
	if err == nil {
		t.Fatal("body over cap must be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error should mention size cap: %v", err)
	}
}

func TestNominateSecretBodyRejected(t *testing.T) {
	st := testStore(t)
	_, err := NominateUnit(st, NominateParams{
		Kind: KindSkill, Name: "leaky-skill", Title: "Leaky",
		Body: "run with Bearer sk-abcdef1234567890 please", NominatedBy: "x",
	})
	if err == nil {
		t.Fatal("secret-shaped body must be rejected")
	}
}

// PromoteCandidate is covered in flywheel_test.go; here we directly assert
// NominateUnit(kind=playbook) never touches the playbooks table.
func TestNominatePlaybookCreatesNoPlaybookRow(t *testing.T) {
	st := testStore(t)
	_, err := NominateUnit(st, NominateParams{
		Kind: KindPlaybook, Name: "run-abc123", Title: "Pattern: agent-a",
		ProvenanceRun: []string{"abc123"}, NominatedBy: "phucnt",
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM playbooks`).Scan(&n)
	if n != 0 {
		t.Errorf("nominate must not write playbooks, found %d", n)
	}
}

// TestVersionMonotonicAndSupersedes covers F5: nominating v2 while v1 is
// PUBLISHED must succeed (v1 keeps serving, untouched) with version_n/
// supersedes_id set as a lineage pointer only; the actual supersede of v1
// happens later via MarkSuperseded (applier-owned), not at nominate time.
func TestVersionMonotonicAndSupersedes(t *testing.T) {
	st := testStore(t)
	id1 := seedCtxUnit(t, st)
	if err := SubmitForReview(st, id1, "admin"); err != nil {
		t.Fatalf("submit v1: %v", err)
	}
	if err := transition(st, id1, StateInReview, StatePublished, "admin", "approved"); err != nil {
		t.Fatalf("publish v1: %v", err)
	}

	id2, err := NominateUnit(st, NominateParams{
		Kind: KindContext, Name: "handoff-notes", Title: "Handoff notes rule v2",
		RefKind: "context_version", RefID: 2, NominatedBy: "dandori-observer",
	})
	if err != nil {
		t.Fatalf("v2 nominate while v1 published: %v", err)
	}
	u2, _ := GetUnit(st, id2)
	if u2.VersionN != 2 {
		t.Errorf("version_n: %d, want 2", u2.VersionN)
	}
	if u2.SupersedesID == nil || *u2.SupersedesID != id1 {
		t.Errorf("supersedes_id: %+v, want %d", u2.SupersedesID, id1)
	}

	// v1 must still be published — nominate never supersedes eagerly (F5).
	u1, _ := GetUnit(st, id1)
	if u1.State != StatePublished {
		t.Errorf("v1 state after v2 nominate: %s, want published (denial-of-knowledge if not)", u1.State)
	}

	// Now the applier approves knowledge-publish for v2: idx_ku_kind_name_live
	// allows only ONE published/adopted/measured row per (kind,name), so the
	// applier must retire v1's claim on the slug (MarkSuperseded) before v2's
	// own transition into that same live set — this is the real P3 ordering
	// (both calls happen inside one tx in production; sequenced here since
	// this package's transition/MarkSuperseded each own their own tx).
	if err := transition(st, id2, StateNominated, StateInReview, "admin", "submitted"); err != nil {
		t.Fatalf("submit v2: %v", err)
	}
	if err := MarkSuperseded(st, id1, "admin", "superseded by v2"); err != nil {
		t.Fatalf("MarkSuperseded: %v", err)
	}
	if err := transition(st, id2, StateInReview, StatePublished, "admin", "applier: knowledge-publish approved"); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	u1, _ = GetUnit(st, id1)
	if u1.State != StateSuperseded {
		t.Errorf("v1 state after MarkSuperseded: %s, want superseded", u1.State)
	}
}

// TestNominateDuplicateDraftRejected covers the denial-of-knowledge fix: a
// second nominate for the same (kind,name) while a draft (nominated/
// in_review) is already pending must be rejected — one draft at a time.
func TestNominateDuplicateDraftRejected(t *testing.T) {
	st := testStore(t)
	seedCtxUnit(t, st) // v1 stays in state=nominated (never submitted)

	_, err := NominateUnit(st, NominateParams{
		Kind: KindContext, Name: "handoff-notes", Title: "Handoff notes rule v2",
		RefKind: "context_version", RefID: 2, NominatedBy: "dandori-observer",
	})
	if err == nil {
		t.Fatal("nominate must reject a second draft while one is already nominated/in_review")
	}
}

// TestMarkSupersededRejectsWrongState covers the applier-side guard: only a
// published/adopted/measured unit can be marked superseded.
func TestMarkSupersededRejectsWrongState(t *testing.T) {
	st := testStore(t)
	id := seedCtxUnit(t, st) // state=nominated
	if err := MarkSuperseded(st, id, "admin", "should fail"); err == nil {
		t.Error("MarkSuperseded on a nominated (non-published) unit must fail")
	}
}

func TestListUnitsByState(t *testing.T) {
	st := testStore(t)
	id := seedCtxUnit(t, st)
	nominated, err := ListUnits(st, StateNominated)
	if err != nil || len(nominated) != 1 || nominated[0].ID != id {
		t.Fatalf("ListUnits(nominated): %+v err=%v", nominated, err)
	}
	if pub, _ := ListUnits(st, StatePublished); len(pub) != 0 {
		t.Errorf("ListUnits(published) should be empty: %+v", pub)
	}
	all, err := ListUnits(st, "")
	if err != nil || len(all) != 1 {
		t.Fatalf("ListUnits(\"\"): %+v err=%v", all, err)
	}
}

// fakeRequestAction stands in for observer.RequestAction (learn cannot
// import observer — reverse import would cycle). It records calls so the
// test can assert the pinned params without a real approvals table.
func fakeRequestAction(calls *[]map[string]any) RequestActionFunc {
	return func(st *store.Store, typ, subject, summary string, params map[string]any, requestedBy, surface string) (int64, error) {
		rec := map[string]any{"typ": typ, "subject": subject, "surface": surface}
		for k, v := range params {
			rec[k] = v
		}
		*calls = append(*calls, rec)
		return int64(len(*calls)), nil
	}
}

func TestRequestPublishPinsSkillBodyAndHash(t *testing.T) {
	st := testStore(t)
	id, err := NominateUnit(st, NominateParams{
		Kind: KindSkill, Name: "cook-plan", Title: "Cook Plan Skill",
		Body: "# Cook Plan\ndo the thing", NominatedBy: "phucnt",
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls []map[string]any
	approvalID, err := RequestPublish(st, fakeRequestAction(&calls), id, "admin")
	if err != nil || approvalID == 0 {
		t.Fatalf("RequestPublish: id=%d err=%v", approvalID, err)
	}
	if len(calls) != 1 {
		t.Fatalf("requestAction calls: %d, want 1", len(calls))
	}
	c := calls[0]
	if c["typ"] != "knowledge-publish" || c["surface"] != "operator" {
		t.Errorf("call shape: %+v", c)
	}
	if c["body"] == "" || c["body"] == nil {
		t.Error("pinned body missing for skill kind")
	}
	if c["content_hash"] == "" || c["content_hash"] == nil {
		t.Error("pinned content_hash missing for skill kind")
	}
	// State must NOT change on request — only the applier (post-approval)
	// changes state (F5).
	u, _ := GetUnit(st, id)
	if u.State != StateNominated {
		t.Errorf("RequestPublish must not change state, got %s", u.State)
	}
}

// TestRequestPublishRejectsRetireProposal covers M2: a NominateRetireProposals
// draft (RefKind == RefKindRetireTarget) must never be requestable for a
// knowledge-publish approval — the correct action is retiring the TARGET
// unit (RefID) directly, not publishing the proposal itself.
func TestRequestPublishRejectsRetireProposal(t *testing.T) {
	st := testStore(t)
	targetID := seedCtxUnit(t, st) // stand-in "target" unit being proposed for retire

	id, err := NominateUnit(st, NominateParams{
		Kind: KindPlaybook, Name: "retire-proposal-handoff-notes", Title: "Retire proposal: handoff-notes",
		RefKind: RefKindRetireTarget, RefID: targetID, NominatedBy: "dandori-observer",
	})
	if err != nil {
		t.Fatalf("nominate retire-proposal: %v", err)
	}

	var calls []map[string]any
	approvalID, err := RequestPublish(st, fakeRequestAction(&calls), id, "admin")
	if err == nil {
		t.Fatalf("RequestPublish must reject a retire-proposal, got approvalID=%d", approvalID)
	}
	if !errors.Is(err, ErrRetireProposalNotPublishable) {
		t.Errorf("err = %v, want ErrRetireProposalNotPublishable", err)
	}
	if len(calls) != 0 {
		t.Errorf("requestAction must not be invoked for a rejected retire-proposal, got %d calls", len(calls))
	}

	// State must remain untouched by the rejected request.
	u, _ := GetUnit(st, id)
	if u.State != StateNominated {
		t.Errorf("rejected RequestPublish must not change state, got %s", u.State)
	}
}

func TestRequestMandateAndRetireNoStateChange(t *testing.T) {
	st := testStore(t)
	id := seedCtxUnit(t, st)
	// L2: RequestMandate/RequestRetire now require a LIVE unit (published/
	// adopted/measured) — same live-state check the applier enforces, moved
	// earlier. Drive the seeded draft to published before requesting.
	if err := SubmitForReview(st, id, "admin"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := transition(st, id, StateInReview, StatePublished, "admin", "approved"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	var calls []map[string]any
	if _, err := RequestMandate(st, fakeRequestAction(&calls), id, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := RequestRetire(st, fakeRequestAction(&calls), id, "admin"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0]["typ"] != "knowledge-mandate" || calls[1]["typ"] != "knowledge-retire" {
		t.Errorf("calls: %+v", calls)
	}
	u, _ := GetUnit(st, id)
	if u.State != StatePublished {
		t.Errorf("mandate/retire request must not change state, got %s", u.State)
	}
}

// TestRequestMandateRejectsDraftState covers L2: a direct admin POST for
// mandate/retire against a unit that never reached a live state (still
// nominated/in_review, or already retired/superseded/rejected) must be
// refused up front — the applier's own live-state check would otherwise
// dead-end the approval later with no explanation surfaced at request time.
func TestRequestMandateRejectsDraftState(t *testing.T) {
	st := testStore(t)
	id := seedCtxUnit(t, st) // state=nominated
	var calls []map[string]any

	if _, err := RequestMandate(st, fakeRequestAction(&calls), id, "admin"); !errors.Is(err, ErrUnitNotLive) {
		t.Errorf("RequestMandate on a nominated unit: err=%v, want ErrUnitNotLive", err)
	}
	if _, err := RequestRetire(st, fakeRequestAction(&calls), id, "admin"); !errors.Is(err, ErrUnitNotLive) {
		t.Errorf("RequestRetire on a nominated unit: err=%v, want ErrUnitNotLive", err)
	}
	if len(calls) != 0 {
		t.Errorf("requestAction must not be invoked for a non-live unit, got %d calls", len(calls))
	}
}

func TestRequestActionMissingFuncErrors(t *testing.T) {
	st := testStore(t)
	id := seedCtxUnit(t, st)
	if _, err := RequestPublish(st, nil, id, "admin"); err == nil {
		t.Error("nil requestAction func must error, not panic")
	}
}

// adoptions must accept BOTH the legacy playbook_id-only row shape and the
// new unit_id+installed shape (F4 additive).
func TestAdoptionsAcceptsUnitAndLegacyPlaybookRows(t *testing.T) {
	st := testStore(t)
	id := seedCtxUnit(t, st)

	if _, err := st.DB.Exec(`INSERT INTO adoptions(unit_id, installed, operator_id, adopted_at)
		VALUES(?, 1, 'bob@dev', ?)`, id, store.Now()); err != nil {
		t.Fatalf("insert unit_id adoption: %v", err)
	}

	// Legacy shape: playbooks row + playbook_id-only adoption (P6-owned
	// generalization untouched — must still work after this migration).
	res, err := st.DB.Exec(`INSERT INTO playbooks(name, created_at) VALUES('legacy', ?)`, store.Now())
	if err != nil {
		t.Fatal(err)
	}
	pbID, _ := res.LastInsertId()
	if _, err := st.DB.Exec(`INSERT INTO adoptions(playbook_id, operator_id, adopted_at)
		VALUES(?, 'alice@mac', ?)`, pbID, store.Now()); err != nil {
		t.Fatalf("insert legacy playbook_id adoption: %v", err)
	}

	var n int
	st.DB.QueryRow(`SELECT count(*) FROM adoptions`).Scan(&n)
	if n != 2 {
		t.Errorf("adoptions count: %d, want 2", n)
	}
}
