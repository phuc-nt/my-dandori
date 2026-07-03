package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/observer"
)

// C1: a company-layer save must NOT write a version — it creates an
// operator-surface approval. Team/agent saves write directly.
func TestCompanySaveIsApprovalGated(t *testing.T) {
	s := testServer(t)
	rec := postForm(t, s, "/contexts/save", url.Values{
		"layer": {"company"}, "target": {"*"}, "content": {"Chính sách mới."}, "note": {""},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d", rec.Code)
	}
	// No company version yet.
	if head, _ := contexthub.New(s.Store).Head(contexthub.LayerCompany, "*"); head != nil {
		t.Errorf("company save wrote a version directly (C1 bypass): %+v", head)
	}
	// An approval request exists.
	var n int
	s.Store.DB.QueryRow(`SELECT count(*) FROM approvals WHERE action LIKE 'observer:context-company-edit:%' AND status='pending'`).Scan(&n)
	if n != 1 {
		t.Errorf("company save should create 1 pending approval, got %d", n)
	}
}

func TestTeamSaveWritesDirectly(t *testing.T) {
	s := testServer(t)
	rec := postForm(t, s, "/contexts/save", url.Values{
		"layer": {"team"}, "target": {"5"}, "content": {"Đội 5 viết test."}, "note": {"init"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d", rec.Code)
	}
	head, _ := contexthub.New(s.Store).Head(contexthub.LayerTeam, "5")
	if head == nil || head.Content != "Đội 5 viết test." {
		t.Errorf("team save did not write directly: %+v", head)
	}
}

// M1: secret-shaped content is rejected with a banner, nothing written.
func TestSaveSecretRejected(t *testing.T) {
	s := testServer(t)
	rec := postForm(t, s, "/contexts/save", url.Values{
		"layer": {"team"}, "target": {"5"}, "content": {"token: abcdef123456789"},
	})
	if !strings.Contains(rec.Body.String(), "giống secret") {
		t.Errorf("no secret banner: %s", rec.Body.String())
	}
	if head, _ := contexthub.New(s.Store).Head(contexthub.LayerTeam, "5"); head != nil {
		t.Error("secret content was written")
	}
}

// H1: a context-promote/company-edit approval is operator-surface and must
// NOT appear in the CEO one-tap inbox.
func TestContextApprovalNotInCEOInbox(t *testing.T) {
	s := testServer(t)
	// company edit → operator approval
	postForm(t, s, "/contexts/save", url.Values{
		"layer": {"company"}, "target": {"*"}, "content": {"Chính sách X."},
	})
	view, err := BuildExecView(s.Store, 30)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range view.Inbox {
		if strings.Contains(item.Summary, "chính sách") || strings.Contains(item.Summary, "công ty") {
			t.Errorf("context approval leaked into CEO inbox: %q", item.Summary)
		}
	}
	// It IS in the operator surface.
	var op int
	s.Store.DB.QueryRow(`SELECT count(*) FROM insights WHERE surface='operator' AND type='request_context-company-edit'`).Scan(&op)
	if op != 1 {
		t.Errorf("operator-surface context insight count = %d, want 1", op)
	}
}

// H3: editing the team doc AFTER proposing a promote must not change what
// gets applied — the applier uses the pinned version, not the moved head.
func TestPromoteAppliesPinnedNotHead(t *testing.T) {
	s := testServer(t)
	hub := contexthub.New(s.Store)
	// team head v1 = the bytes the approver will see.
	hub.SaveContext(contexthub.LayerTeam, "9", "ĐÚNG: nội dung được duyệt", "phuc", "")
	s.Store.DB.Exec(`INSERT INTO teams(id,name,created_at) VALUES(9,'t9','now') ON CONFLICT DO NOTHING`)

	rec := postForm(t, s, "/contexts/promote", url.Values{"team": {"9"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("promote: %d", rec.Code)
	}
	// Team head moves to v2 AFTER the proposal.
	hub.SaveContext(contexthub.LayerTeam, "9", "SAI: sửa sau khi đề xuất", "phuc", "")

	// Approve the promote.
	var apprID int64
	s.Store.DB.QueryRow(`SELECT id FROM approvals WHERE action LIKE 'observer:context-promote:%'`).Scan(&apprID)
	if _, err := govern.Decide(s.Store, apprID, true, "phuc", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.RunObserverApplier(s.Store); err != nil {
		t.Fatal(err)
	}
	// Company head must be the PINNED v1 content, not the edited head.
	company, _ := hub.Head(contexthub.LayerCompany, "*")
	if company == nil || !strings.Contains(company.Content, "ĐÚNG") {
		t.Errorf("applied content = %+v, want pinned 'ĐÚNG' version (H3)", company)
	}
	if company != nil && strings.Contains(company.Content, "SAI") {
		t.Error("applied the edited head instead of the pinned version (H3 TOCTOU)")
	}
}

// L5: proposing the same team twice yields one approval.
func TestPromoteDedup(t *testing.T) {
	s := testServer(t)
	hub := contexthub.New(s.Store)
	hub.SaveContext(contexthub.LayerTeam, "3", "đội 3", "phuc", "")
	s.Store.DB.Exec(`INSERT INTO teams(id,name,created_at) VALUES(3,'t3','now') ON CONFLICT DO NOTHING`)
	postForm(t, s, "/contexts/promote", url.Values{"team": {"3"}})
	postForm(t, s, "/contexts/promote", url.Values{"team": {"3"}})
	var n int
	s.Store.DB.QueryRow(`SELECT count(*) FROM approvals WHERE action LIKE 'observer:context-promote:%'`).Scan(&n)
	if n != 1 {
		t.Errorf("double promote created %d approvals, want 1 (dedup L5)", n)
	}
}

func TestEffectivePreviewMatchesMerge(t *testing.T) {
	s := testServer(t)
	hub := contexthub.New(s.Store)
	hub.SaveContext(contexthub.LayerCompany, "*", "Cty.", "phuc", "")
	s.Store.DB.Exec(`INSERT INTO agents(id,name,created_at) VALUES('bot','bot','now')`)
	want, _, _ := hub.EffectiveContext("bot")

	rec := get(t, s, "/contexts/effective?agent=bot")
	// The template HTML-escapes the content (correct — it's user data), so the
	// rendered page holds the escaped form of the merged bytes.
	if !strings.Contains(want, "Cty.") {
		t.Fatal("merge sanity: company content missing")
	}
	if !strings.Contains(rec.Body.String(), "Cty.") ||
		!strings.Contains(rec.Body.String(), "Chính sách công ty") {
		t.Errorf("preview does not render the merged context")
	}
	// The raw comment marker must be escaped, not injected as real HTML.
	if strings.Contains(rec.Body.String(), "<!-- dandori-context: begin") {
		t.Error("preview injected raw HTML comment instead of escaping it")
	}
}
