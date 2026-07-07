package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/observer"
)

// F9: nominate is any authenticated operator — viewer role must succeed.
func TestKnowledgeNominateViewerOK(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "viewer1", "viewer")
	cookie := roleSession(t, s, "viewer1")

	rec := postFormAs(t, s, cookie, "/knowledge/nominate", url.Values{
		"kind": {"skill"}, "name": {"viewer-nominated-skill"}, "title": {"t"}, "body": {"body content"},
	})
	if rec.Code == http.StatusForbidden {
		t.Fatalf("viewer nominate = 403, want NOT forbidden (F9 viewer-ok)")
	}
	var n int
	s.Store.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE name='viewer-nominated-skill'`).Scan(&n)
	if n != 1 {
		t.Errorf("nominated units named viewer-nominated-skill=%d, want 1", n)
	}
}

// F9: every decide route (submit/reject/publish-request) is admin-only.
func TestKnowledgeDecideRoutesAdminOnly(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "viewer2", "viewer")
	viewerCookie := roleSession(t, s, "viewer2")

	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindSkill, Name: "decide-gate-skill", Title: "t", Body: "b", NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	idStr := strconv.FormatInt(id, 10)

	for _, path := range []string{
		"/knowledge/unit/" + idStr + "/submit",
		"/knowledge/unit/" + idStr + "/reject",
		"/knowledge/unit/" + idStr + "/publish-request",
	} {
		rec := postFormAs(t, s, viewerCookie, path, url.Values{"note": {"why"}})
		if rec.Code != http.StatusForbidden {
			t.Errorf("viewer POST %s = %d, want 403 (F9 admin-only decide)", path, rec.Code)
		}
	}
}

// F9: 64KB body cap enforced at the nominate handler boundary.
func TestKnowledgeNominateBodyCap(t *testing.T) {
	s := testServer(t) // local-trust admin — cap must still apply regardless of role
	big := strings.Repeat("a", knowledgeMaxBodyBytes+1)
	rec := postForm(t, s, "/knowledge/nominate", url.Values{
		"kind": {"skill"}, "name": {"too-big-skill"}, "title": {"t"}, "body": {big},
	})
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized nominate body status=%d, want 400 or 413", rec.Code)
	}
	var n int
	s.Store.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE name='too-big-skill'`).Scan(&n)
	if n != 0 {
		t.Error("oversized body must never be nominated")
	}
}

// F9: secret-shaped content is rejected before NominateUnit is even called.
func TestKnowledgeNominateSecretRejected(t *testing.T) {
	s := testServer(t)
	rec := postForm(t, s, "/knowledge/nominate", url.Values{
		"kind": {"skill"}, "name": {"secret-skill"}, "title": {"t"}, "body": {"token: abcdef123456789"},
	})
	if !strings.Contains(rec.Body.String(), "giống secret") {
		t.Errorf("no secret banner: %s", rec.Body.String())
	}
	var n int
	s.Store.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE name='secret-skill'`).Scan(&n)
	if n != 0 {
		t.Error("secret-shaped body must never be nominated")
	}
}

// F5: a second publish-request while one is still open must not create a
// second approval — banner instead.
func TestKnowledgePublishRequestDedup(t *testing.T) {
	s := testServer(t)
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindSkill, Name: "dedup-skill", Title: "t", Body: "b", NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(s.Store, id, "tester"); err != nil {
		t.Fatal(err)
	}
	idStr := strconv.FormatInt(id, 10)
	postForm(t, s, "/knowledge/unit/"+idStr+"/publish-request", url.Values{})
	postForm(t, s, "/knowledge/unit/"+idStr+"/publish-request", url.Values{})

	var n int
	s.Store.DB.QueryRow(`SELECT count(*) FROM approvals WHERE action LIKE 'observer:knowledge-publish:%'`).Scan(&n)
	if n != 1 {
		t.Errorf("approvals for dedup-skill=%d, want 1 (F5 dedup)", n)
	}
}

// F1 CRITICAL: /reviews must render the FULL pinned body + hash for a
// knowledge-publish approval, not a truncated summary, at the decide surface
// itself (not just /knowledge/unit/{id}).
func TestReviewsRendersFullKnowledgeBody(t *testing.T) {
	s := testServer(t)
	longBody := "# Full skill body\n" + strings.Repeat("chi tiết dòng này lặp lại nhiều lần. ", 50)
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindSkill, Name: "full-render-skill", Title: "t", Body: longBody, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(s.Store, id, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := learn.RequestPublish(s.Store, observer.RequestAction, id, "tester"); err != nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/reviews")
	if rec.Code != http.StatusOK {
		t.Fatalf("/reviews status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), longBody) {
		t.Error("/reviews did not render the FULL pinned knowledge body (F1 — no truncation allowed)")
	}
	u, _ := learn.GetUnit(s.Store, id)
	if !strings.Contains(rec.Body.String(), u.ContentHash) {
		t.Error("/reviews did not render the content_hash (F1/F7)")
	}
}

// C1 CRITICAL: the same full-body pinning F1 requires for skill must also
// hold for a body-carrying context unit (RefID==0, detector-proposed new
// content) — actionParams must key on body PRESENCE, not kind=skill. Real
// RequestPublish → evidence → /reviews path, no hand-crafted evidence map.
func TestReviewsRendersFullKnowledgeBodyForContext(t *testing.T) {
	s := testServer(t)
	longBody := "task X: dùng tool Y thay vì Z. " + strings.Repeat("chi tiết lặp lại nhiều lần. ", 50)
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindContext, Name: "full-render-context", Title: "t", Body: longBody,
		Layer: "company", LayerTarget: "*", NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(s.Store, id, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := learn.RequestPublish(s.Store, observer.RequestAction, id, "tester"); err != nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/reviews")
	if rec.Code != http.StatusOK {
		t.Fatalf("/reviews status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), longBody) {
		t.Error("/reviews did not render the FULL pinned context body (C1 — body pinning must not be skill-only)")
	}
	u, _ := learn.GetUnit(s.Store, id)
	if u.ContentHash == "" {
		t.Fatal("context unit missing content_hash — C1 must pin content_hash for every body-carrying unit")
	}
	if !strings.Contains(rec.Body.String(), u.ContentHash) {
		t.Error("/reviews did not render the content_hash for a body-carrying context unit (C1/F7)")
	}

	// Approve and drive the REAL apply path — proves the pinned body actually
	// reaches contexthub, not just the review render.
	var apprID int64
	s.Store.DB.QueryRow(`SELECT id FROM approvals WHERE action LIKE 'observer:knowledge-publish:%' ORDER BY id DESC LIMIT 1`).Scan(&apprID)
	if _, err := govern.Decide(s.Store, apprID, true, "tester@console", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.RunObserverApplier(s.Store); err != nil {
		t.Fatal(err)
	}
	got, _ := learn.GetUnit(s.Store, id)
	if got.State != learn.StatePublished {
		t.Errorf("context unit state=%q, want published", got.State)
	}
}

// C1 CRITICAL: same body-pinning proof for a body-carrying rule unit
// (RefID==0, detector-proposed new rule text — the P2 tool-pattern shape).
func TestReviewsRendersFullKnowledgeBodyForRule(t *testing.T) {
	s := testServer(t)
	longBody := "block\tBash\trm -rf /\n" + strings.Repeat("# ghi chú lặp lại nhiều lần.\n", 50)
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindRule, Name: "full-render-rule", Title: "t", Body: longBody, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(s.Store, id, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := learn.RequestPublish(s.Store, observer.RequestAction, id, "tester"); err != nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/reviews")
	if rec.Code != http.StatusOK {
		t.Fatalf("/reviews status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), longBody) {
		t.Error("/reviews did not render the FULL pinned rule body (C1 — body pinning must not be skill-only)")
	}
	u, _ := learn.GetUnit(s.Store, id)
	if u.ContentHash == "" {
		t.Fatal("rule unit missing content_hash — C1 must pin content_hash for every body-carrying unit")
	}
	if !strings.Contains(rec.Body.String(), u.ContentHash) {
		t.Error("/reviews did not render the content_hash for a body-carrying rule unit (C1/F7)")
	}
}

// H1: rule_intent must be pinned in the evidence for each of the three
// intents — enable (plain new rule, default — no warning banner by design,
// it is the non-destructive path), retire, and scope-up
// (detectRuleLifecycle's deterministic name suffixes) — and retire/scope-up
// must render their distinct, prominent warning banner at /reviews so a
// human never mistakes a "gỡ rule" approval for a plain publish. Real
// RequestPublish path per intent, not a hand-crafted evidence map.
func TestReviewsShowsRuleIntentPerPath(t *testing.T) {
	for _, tc := range []struct {
		name       string
		unitName   string
		wantIntent string
		wantBanner string // empty = no dedicated banner expected (enable path)
	}{
		{"enable", "intent-enable-rule", learn.RuleIntentEnable, ""},
		{"retire", "rule-77-retire", learn.RuleIntentRetire, "ĐỀ XUẤT TẮT RULE"},
		{"scope-up", "rule-77-scope-up", learn.RuleIntentScopeUp, "NÂNG SCOPE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer(t)
			id, err := learn.NominateUnit(s.Store, learn.NominateParams{
				Kind: learn.KindRule, Name: tc.unitName, Title: "t", Body: "block\tBash\tsome pattern", NominatedBy: "tester",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := learn.SubmitForReview(s.Store, id, "tester"); err != nil {
				t.Fatal(err)
			}
			if _, err := learn.RequestPublish(s.Store, observer.RequestAction, id, "tester"); err != nil {
				t.Fatal(err)
			}
			// The evidence pinned in the approval must carry the correct intent —
			// proves actionParams (learn/knowledge_units_actions.go) derives it
			// from the unit's slug via RuleIntentFromName, not a default.
			var evidence string
			s.Store.DB.QueryRow(`SELECT evidence FROM insights WHERE type = 'request_knowledge-publish'
				AND subject = ? ORDER BY id DESC LIMIT 1`, "rule:"+tc.unitName).Scan(&evidence)
			if !strings.Contains(evidence, `"rule_intent":"`+tc.wantIntent+`"`) {
				t.Errorf("evidence=%q, want rule_intent=%q pinned", evidence, tc.wantIntent)
			}
			rec := get(t, s, "/reviews")
			if rec.Code != http.StatusOK {
				t.Fatalf("/reviews status=%d", rec.Code)
			}
			body := rec.Body.String()
			if tc.wantBanner != "" && !strings.Contains(body, tc.wantBanner) {
				t.Errorf("/reviews did not display the %q warning banner for intent %q", tc.wantBanner, tc.wantIntent)
			}
			if tc.wantBanner == "" {
				for _, warn := range []string{"ĐỀ XUẤT TẮT RULE", "NÂNG SCOPE"} {
					if strings.Contains(body, warn) {
						t.Errorf("/reviews showed destructive banner %q for a plain enable-intent rule", warn)
					}
				}
			}
		})
	}
}

// F1 CRITICAL negative: a knowledge-publish approval must NEVER be reachable
// by the Slack poller's SELECTs — proven directly against the same queries
// approvals.go uses (postNew/pollReactions), since this package cannot import
// the slack package's unexported internals.
func TestKnowledgeApprovalExcludedFromSlackQueries(t *testing.T) {
	s := testServer(t)
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindSkill, Name: "slack-excluded-skill", Title: "t", Body: "b", NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(s.Store, id, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := learn.RequestPublish(s.Store, observer.RequestAction, id, "tester"); err != nil {
		t.Fatal(err)
	}

	var postNewCount int
	s.Store.DB.QueryRow(`SELECT count(*) FROM approvals WHERE status = 'pending' AND slack_ts IS NULL
		AND action NOT LIKE 'observer:knowledge-%'`).Scan(&postNewCount)
	if postNewCount != 0 {
		t.Errorf("postNew-equivalent SELECT surfaced %d knowledge approval(s), want 0", postNewCount)
	}

	// Simulate the row having somehow acquired a slack_ts (belt-and-suspenders
	// case pollReactions guards against too).
	s.Store.DB.Exec(`UPDATE approvals SET slack_ts = 'ts123' WHERE action LIKE 'observer:knowledge-publish:%'`)
	var pollCount int
	s.Store.DB.QueryRow(`SELECT count(*) FROM approvals WHERE status = 'pending' AND slack_ts IS NOT NULL
		AND slack_ts != 'dry-run' AND action NOT LIKE 'observer:knowledge-%'`).Scan(&pollCount)
	if pollCount != 0 {
		t.Errorf("pollReactions-equivalent SELECT surfaced %d knowledge approval(s), want 0", pollCount)
	}
}

// A decided (approved) knowledge-publish approval, when applied, publishes
// the unit and RunObserverApplier can be driven directly from a web-issued
// govern.Decide the same way handleReviewDecide does.
func TestKnowledgeReviewDecideAppliesPublish(t *testing.T) {
	s := testServer(t)
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindSkill, Name: "decide-apply-skill", Title: "t", Body: "b", NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(s.Store, id, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := learn.RequestPublish(s.Store, observer.RequestAction, id, "tester"); err != nil {
		t.Fatal(err)
	}
	var apprID int64
	s.Store.DB.QueryRow(`SELECT id FROM approvals WHERE action LIKE 'observer:knowledge-publish:%'`).Scan(&apprID)
	if _, err := govern.Decide(s.Store, apprID, true, "tester@console", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.RunObserverApplier(s.Store); err != nil {
		t.Fatal(err)
	}
	u, _ := learn.GetUnit(s.Store, id)
	if u.State != learn.StatePublished {
		t.Errorf("unit state=%q, want published", u.State)
	}
}
