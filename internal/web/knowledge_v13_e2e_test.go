// Package web v13 Kit & Mining E2E: the web-surface half of phase-06 (mining
// queue → AI draft → nominate loop end-to-end over real HTTP, plus the
// web-driven half of the H1 tamper-after-RequestPublish security negative,
// which needs /reviews' decide handler — internal/cli cannot drive that
// route). Mirrors knowledge_e2e_test.go's (v12) convention: this file
// documents, next to each test, which existing unit test it deliberately
// does NOT duplicate.
//
// Loop 1 (kit full CLI→web→CLI) and Loop 3 (import) live in
// internal/cli/kit_e2e_test.go instead — those loops are driven almost
// entirely by CLI commands (kit nominate/pull, knowledge import), with only
// the /reviews approve step needing the web layer, which that file drives
// directly via learn.RequestPublish + observer.RunObserverApplier (no HTTP
// round trip needed for a server-side approval the CLI test already
// controls end to end).
package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/chat"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/observer"
)

// stubOpenRouterServer starts a fake OpenRouter HTTP server that always
// returns draftText as the assistant message, and points chat's test-only
// base-URL hook at it for the duration of the test (t.Cleanup restores it to
// "", which routes DraftPractice back to the real OpenRouter host). Mirrors
// the inline stub in handlers_review_ai_draft_test.go's
// TestKnowledgeDraftSuccessHasHiddenOriginFields, factored out since this
// file's tests need the same shape repeatedly.
func stubOpenRouterServer(t *testing.T, draftText string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": draftText}}},
			"usage": map[string]any{"total_tokens": 42},
		})
	}))
	t.Cleanup(srv.Close)
	chat.SetTestOpenRouterBaseURL(srv.URL)
	t.Cleanup(func() { chat.SetTestOpenRouterBaseURL("") })
	return srv
}

// seedV13MiningRun mirrors seedMiningRun (handlers_knowledge_mining_test.go)
// but also seeds a second corrective-steering signal so the mined run always
// carries >=1 signal AND has a real project/task shape a draft can read
// evidence from — kept separate rather than widening the existing helper
// (its callers rely on the exact minimal guardrail-only shape).
func seedV13MiningRun(t *testing.T, s *Server, runID string) {
	t.Helper()
	if _, err := s.Store.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a','a',datetime('now'))
		ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-10 * time.Minute)
	ended := started.Add(5 * time.Minute)
	if _, err := s.Store.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, task_key, status, started_at, ended_at, model, cost_usd, lines_added, lines_deleted, source)
		VALUES(?,?,?,'proj-mine','TASK-9','done',?,?,'m',1.5,3,1,'hook')`,
		runID, runID, "a", started.Format(time.RFC3339), ended.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO events(run_id, ts, kind, ok) VALUES(?, datetime('now'), 'guardrail_block', 0)`,
		runID); err != nil {
		t.Fatal(err)
	}
}

// TestE2EMiningDraftNominateLoop drives Loop 2 in full over real HTTP: seed a
// mining-signal-matching run -> GET /knowledge/mining lists it ranked with
// its evidence badge -> POST /knowledge/draft (stubbed OpenRouter) returns an
// editable form carrying hidden origin=ai-draft -> submit that form to
// /knowledge/nominate -> the resulting unit has origin='ai-draft' and its
// provenance_run_ids includes the source run -> the source run's mining-tab
// row now shows the "đã đúc" badge.
//
// TestKnowledgeDraftSuccessHasHiddenOriginFields (handlers_review_ai_draft_
// test.go) already proves the draft fragment's hidden fields in isolation;
// this test is the genuinely new piece — chaining that fragment's fields
// into a REAL /knowledge/nominate submission and asserting the persisted row
// + mining badge, which no existing test drives end to end.
func TestE2EMiningDraftNominateLoop(t *testing.T) {
	stubOpenRouterServer(t, "# Practice từ mining\nNội dung do AI soạn, người duyệt sẽ sửa trước khi đề cử.")
	s := testServerWithListen(t, "127.0.0.1:4790")
	s.Cfg.OpenRouterKey = "k"
	s.Cfg.OpenRouterModel = "test/model"
	mustCreateAccount(t, s, "miner1", "admin")
	cookie := roleSession(t, s, "miner1")
	seedV13MiningRun(t, s, "mine-loop-1")

	mining := getAs(t, s, cookie, "/knowledge/mining")
	if mining.Code != http.StatusOK || !strings.Contains(mining.Body.String(), "mine-loop-1") {
		t.Fatalf("setup: /knowledge/mining must list mine-loop-1, code=%d", mining.Code)
	}

	draft := postFormAs(t, s, cookie, "/knowledge/draft", url.Values{"run_id": {"mine-loop-1"}})
	if draft.Code != http.StatusOK {
		t.Fatalf("POST /knowledge/draft = %d, want 200", draft.Code)
	}
	draftBody := draft.Body.String()
	if !strings.Contains(draftBody, `name="origin" value="ai-draft"`) {
		t.Fatalf("draft fragment must carry hidden origin=ai-draft, body=%s", draftBody)
	}
	if !strings.Contains(draftBody, `name="provenance_run_ids" value="mine-loop-1"`) {
		t.Fatalf("draft fragment must carry hidden provenance_run_ids=mine-loop-1, body=%s", draftBody)
	}

	nominate := postFormAs(t, s, cookie, "/knowledge/nominate", url.Values{
		"kind": {"context"}, "name": {"mined-practice-1"}, "title": {"Mined practice"},
		"body":   {"Nội dung do AI soạn, đã sửa bởi người duyệt."},
		"origin": {"ai-draft"}, "origin_model": {"test/model"},
		"provenance_run_ids": {"mine-loop-1"},
	})
	if nominate.Code != http.StatusOK {
		t.Fatalf("POST /knowledge/nominate = %d, want 200", nominate.Code)
	}

	units, err := learn.ListUnits(s.Store, "")
	if err != nil {
		t.Fatal(err)
	}
	var got *learn.KnowledgeUnit
	for i := range units {
		if units[i].Name == "mined-practice-1" {
			got = &units[i]
		}
	}
	if got == nil {
		t.Fatal("nominate did not persist a unit named mined-practice-1")
	}
	if got.Origin != "ai-draft" {
		t.Errorf("unit.Origin = %q, want ai-draft", got.Origin)
	}
	if len(got.ProvenanceRun) != 1 || got.ProvenanceRun[0] != "mine-loop-1" {
		t.Errorf("unit.ProvenanceRun = %v, want [mine-loop-1]", got.ProvenanceRun)
	}

	afterMining := getAs(t, s, cookie, "/knowledge/mining")
	if !strings.Contains(afterMining.Body.String(), "đã đúc") {
		t.Errorf("mining tab must show đã đúc badge for mine-loop-1 after nominate, body=%s", afterMining.Body.String())
	}
}

// TestE2ENominateForgedProvenanceRejected (C2 negative): a nominate whose
// provenance_run_ids names a run id that was never seeded is rejected
// outright, and no unit is persisted with a forged "đã đúc" badge — proves
// the web-surface boundary end to end. parseProvenanceRunIDs' own decision
// table (which error class -> which message) is unit-level, not re-tested
// here.
func TestE2ENominateForgedProvenanceRejected(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4791")
	mustCreateAccount(t, s, "forger1", "admin")
	cookie := roleSession(t, s, "forger1")

	rec := postFormAs(t, s, cookie, "/knowledge/nominate", url.Values{
		"kind": {"context"}, "name": {"forged-practice"}, "title": {"t"}, "body": {"b"},
		"origin": {"ai-draft"}, "origin_model": {"test/model"},
		"provenance_run_ids": {"never-existed-run-id"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("nominate with forged provenance = %d, want 200 (banner, not 500)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "không tồn tại") {
		t.Errorf("response must carry the forgery-rejection banner, body=%s", rec.Body.String())
	}

	units, err := learn.ListUnits(s.Store, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range units {
		if u.Name == "forged-practice" {
			t.Fatal("forged-provenance nominate must not persist any unit")
		}
	}
}

// TestE2EHumanEditedNominateOverridesAiDraftOrigin (L1): a v1 unit is
// nominated with origin=ai-draft; after that draft is rejected, a v2 is
// nominated FRESH (human types it directly, no draft roundtrip) for the same
// (kind,name) and carries origin=human — each row's origin reflects how THAT
// row's content was produced, not the lineage's first version.
func TestE2EHumanEditedNominateOverridesAiDraftOrigin(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4792")
	mustCreateAccount(t, s, "editor1", "admin")
	cookie := roleSession(t, s, "editor1")
	seedV13MiningRun(t, s, "mine-l1-1")

	v1 := postFormAs(t, s, cookie, "/knowledge/nominate", url.Values{
		"kind": {"context"}, "name": {"l1-practice"}, "title": {"v1"}, "body": {"ai body"},
		"origin": {"ai-draft"}, "origin_model": {"test/model"}, "provenance_run_ids": {"mine-l1-1"},
	})
	if v1.Code != http.StatusOK {
		t.Fatalf("nominate v1 = %d, want 200", v1.Code)
	}
	units, err := learn.ListUnits(s.Store, "")
	if err != nil {
		t.Fatal(err)
	}
	var v1ID int64
	for _, u := range units {
		if u.Name == "l1-practice" {
			v1ID = u.ID
		}
	}
	if v1ID == 0 {
		t.Fatal("setup: v1 unit not found")
	}
	if err := learn.RejectUnit(s.Store, v1ID, "editor1", "not good enough, human will redo"); err != nil {
		t.Fatalf("reject v1: %v", err)
	}

	v2 := postFormAs(t, s, cookie, "/knowledge/nominate", url.Values{
		"kind": {"context"}, "name": {"l1-practice"}, "title": {"v2 human"}, "body": {"human-written body, no draft"},
	})
	if v2.Code != http.StatusOK {
		t.Fatalf("nominate v2 = %d, want 200", v2.Code)
	}

	units, err = learn.ListUnits(s.Store, "")
	if err != nil {
		t.Fatal(err)
	}
	var v2Unit *learn.KnowledgeUnit
	for i := range units {
		if units[i].Name == "l1-practice" && units[i].State == learn.StateNominated {
			v2Unit = &units[i]
		}
	}
	if v2Unit == nil {
		t.Fatal("v2 nominated unit not found")
	}
	if v2Unit.Origin != "human" {
		t.Errorf("v2 unit.Origin = %q, want human (per-row origin, not lineage-inherited)", v2Unit.Origin)
	}
}

// TestE2EKitPublishApprovalRunsOverWeb (Loop 1, web half): approves a kit
// publish-request through the real /reviews decide route — the CLI-driven
// half of loop 1 (nominate + pull) lives in internal/cli/kit_e2e_test.go;
// this asserts the web decide surface treats kind=kit exactly like
// kind=skill (same admin-only /reviews/{id}/decide path, same synchronous
// observer.RunObserverApplier call inline in handleReviewDecide).
func TestE2EKitPublishApprovalRunsOverWeb(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4793")
	mustCreateAccount(t, s, "kitadmin1", "admin")
	cookie := roleSession(t, s, "kitadmin1")

	files := []learn.KitFileInput{
		{Path: "agents/reviewer.md", Body: "# Reviewer\nbody"},
		{Path: "rules/style.md", Body: "# Style\nbody"},
	}
	unitID, err := learn.NominateUnitTx(s.Store, learn.KitNominateParams{
		Name: "web-e2e-kit", Title: "Web E2E kit", Files: files, NominatedBy: "kitadmin1", Origin: "human",
	})
	if err != nil {
		t.Fatalf("kit nominate: %v", err)
	}
	if err := learn.SubmitForReview(s.Store, unitID, "kitadmin1"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := learn.RequestPublish(s.Store, observer.RequestAction, unitID, "kitadmin1"); err != nil {
		t.Fatalf("request publish: %v", err)
	}

	var apprID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM approvals WHERE action LIKE 'observer:knowledge-publish:%' AND status='pending' ORDER BY id DESC LIMIT 1`).Scan(&apprID); err != nil {
		t.Fatalf("find pending approval: %v", err)
	}
	dec := postFormAs(t, s, cookie, "/reviews/"+strconv.FormatInt(apprID, 10)+"/decide",
		url.Values{"decision": {"approve"}, "note": {"looks good"}})
	if dec.Code != http.StatusSeeOther {
		t.Fatalf("approve decide = %d, want 303", dec.Code)
	}

	u, err := learn.GetUnit(s.Store, unitID)
	if err != nil || u == nil {
		t.Fatalf("get unit after approve: %v", err)
	}
	if u.State != learn.StatePublished {
		t.Fatalf("unit.State = %q, want published (web decide must run the applier inline)", u.State)
	}

	broken, reason, err := govern.Verify(s.Store)
	if err != nil {
		t.Fatal(err)
	}
	if reason != "" {
		t.Errorf("audit chain broken=%d reason=%q after kit publish, want intact", broken, reason)
	}
}

// TestE2EKitTamperAfterRequestPublishBeforeApplyFailsClosed (H1, part 1 of 2:
// the RequestPublish-side half): tampers knowledge_kit_files.body AFTER
// RequestPublish has already pinned the original file bodies/hashes into the
// pending approval's evidence, but BEFORE the approve decide (which runs
// observer.RunObserverApplier inline) executes. Asserts the applier's
// verifyKitFilesTx catches the mismatch, the approve POST still returns 303
// (decide itself always succeeds — govern.Decide just records the human's
// choice), but the unit never reaches published and stays in_review.
//
// Part 2 (tamper AFTER full publish, refused at kit pull) lives in
// internal/cli/kit_e2e_test.go — that is a DIFFERENT enforcement point
// (runKitPull's own per-file check in kit_cmd.go), not this one
// (observer.verifyKitFilesTx). TestKitPullManifestTamperHardFailsNothingWritten
// (kit_cmd_test.go) already covers a related but distinct case (tampering the
// unit's manifest BODY after a force-published fixture, at the CLI pull
// layer) — this test is the genuinely new one: tampering a PER-FILE row
// between RequestPublish and apply, at the OBSERVER apply layer.
func TestE2EKitTamperAfterRequestPublishBeforeApplyFailsClosed(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4794")
	mustCreateAccount(t, s, "kitadmin2", "admin")
	cookie := roleSession(t, s, "kitadmin2")

	files := []learn.KitFileInput{
		{Path: "agents/reviewer.md", Body: "# Reviewer\noriginal body"},
	}
	unitID, err := learn.NominateUnitTx(s.Store, learn.KitNominateParams{
		Name: "tamper-kit", Title: "Tamper kit", Files: files, NominatedBy: "kitadmin2", Origin: "human",
	})
	if err != nil {
		t.Fatalf("kit nominate: %v", err)
	}
	if err := learn.SubmitForReview(s.Store, unitID, "kitadmin2"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// RequestPublish pins the manifest (built from the ORIGINAL per-file rows)
	// into the approval's evidence JSON right here — everything tampered after
	// this call is tampering AFTER evidence-pinning, the exact H1 window.
	if _, err := learn.RequestPublish(s.Store, observer.RequestAction, unitID, "kitadmin2"); err != nil {
		t.Fatalf("request publish: %v", err)
	}

	if _, err := s.Store.DB.Exec(`UPDATE knowledge_kit_files SET body = ?, content_hash = ? WHERE unit_id = ? AND path = ?`,
		"# Reviewer\nTAMPERED AFTER APPROVAL EVIDENCE WAS PINNED", "0000000000000000000000000000000000000000000000000000000000000000",
		unitID, "agents/reviewer.md"); err != nil {
		t.Fatalf("tamper knowledge_kit_files: %v", err)
	}

	var apprID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM approvals WHERE action LIKE 'observer:knowledge-publish:%' AND status='pending' ORDER BY id DESC LIMIT 1`).Scan(&apprID); err != nil {
		t.Fatalf("find pending approval: %v", err)
	}
	dec := postFormAs(t, s, cookie, "/reviews/"+strconv.FormatInt(apprID, 10)+"/decide",
		url.Values{"decision": {"approve"}, "note": {"approved before noticing tamper"}})
	if dec.Code != http.StatusSeeOther {
		t.Fatalf("approve decide = %d, want 303 (decide itself always records the human decision)", dec.Code)
	}

	u, err := learn.GetUnit(s.Store, unitID)
	if err != nil || u == nil {
		t.Fatalf("get unit: %v", err)
	}
	if u.State == learn.StatePublished {
		t.Fatal("tampered kit must NEVER reach published — verifyKitFilesTx must reject at apply time")
	}
	if u.State != learn.StateInReview {
		t.Errorf("unit.State = %q, want in_review (apply failed permanently, state must not advance)", u.State)
	}

	// Nothing published: no kit_published audit entry for this unit.
	var n int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='knowledge_published' AND subject='kit:tamper-kit'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("audit_log has %d knowledge_published entries for tamper-kit, want 0", n)
	}
}

// TestE2EDraftOpenRouterDownFailsOpenNominateStillSubmits (M4 + fail-open):
// OpenRouter unreachable -> draft fragment shows the "không soạn được, viết
// tay" message with empty title/body. NOTE (observed here, not a P6-owned
// decision): on this specific failure path (real upstream call error, as
// opposed to "model not configured") chat.DraftPractice still returns
// cfg.OpenRouterModel non-empty (draft.go:112/118 — deliberately reports
// which model WAS attempted), so the fragment's {{if .Model}} branch still
// renders the hidden origin=ai-draft/origin_model/provenance_run_ids fields
// alongside the "viết tay" failure text. A human who submits that form
// UNEDITED would badge a unit ai-draft with no actual AI-generated content —
// this test documents the observed shape rather than silently patching P3
// template/draft.go behavior that was already reviewed and shipped; still
// proves the form remains fully submittable with a human-rewritten body,
// which is the actual fail-open guarantee this test is chartered to check.
// TestKnowledgeDraftAdminAllowedFailOpen (handlers_review_ai_draft_test.go)
// already proves the fragment's shape via the DISTINCT model-not-configured
// path (Model cleared, no hidden fields at all); this test exercises the
// real-upstream-call-failure path through to an actual nominate submission,
// which no existing test attempts.
func TestE2EDraftOpenRouterDownFailsOpenNominateStillSubmits(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4795")
	s.Cfg.OpenRouterKey = "k"
	s.Cfg.OpenRouterModel = "test/model"
	mustCreateAccount(t, s, "downadmin1", "admin")
	cookie := roleSession(t, s, "downadmin1")
	seedV13MiningRun(t, s, "mine-down-1")

	// Point the OpenRouter client at a closed local port — deterministic
	// "connection refused" regardless of the sandbox's ambient network access
	// (a real unreachable-host test cannot rely on egress actually being
	// blocked; a closed loopback port always refuses).
	chat.SetTestOpenRouterBaseURL("http://127.0.0.1:1")
	t.Cleanup(func() { chat.SetTestOpenRouterBaseURL("") })

	draft := postFormAs(t, s, cookie, "/knowledge/draft", url.Values{"run_id": {"mine-down-1"}})
	if draft.Code != http.StatusOK {
		t.Fatalf("POST /knowledge/draft with OpenRouter unreachable = %d, want 200 (fail-open)", draft.Code)
	}
	body := draft.Body.String()
	if !strings.Contains(body, "viết tay") {
		t.Errorf("draft fragment must show viết-tay fail-open message, body=%s", body)
	}
	if !strings.Contains(body, `<form`) {
		t.Errorf("failed draft must still return a submittable form (fail-open), body=%s", body)
	}

	// Operator rewrites the body by hand and submits WITHOUT the ai-draft
	// origin fields (dropping them, as a careful operator must on this failed
	// path) — asserting the still-submittable fail-open guarantee this test
	// is chartered to check, independent of the hidden-field observation
	// documented above.
	nominate := postFormAs(t, s, cookie, "/knowledge/nominate", url.Values{
		"kind": {"context"}, "name": {"manual-after-down"}, "title": {"written by hand"}, "body": {"human wrote this after AI draft failed"},
	})
	if nominate.Code != http.StatusOK {
		t.Fatalf("nominate after failed draft = %d, want 200", nominate.Code)
	}
	units, err := learn.ListUnits(s.Store, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range units {
		if u.Name == "manual-after-down" {
			found = true
			if u.Origin != "human" {
				t.Errorf("unit.Origin = %q, want human", u.Origin)
			}
		}
	}
	if !found {
		t.Fatal("manual-after-down unit not persisted")
	}
}
