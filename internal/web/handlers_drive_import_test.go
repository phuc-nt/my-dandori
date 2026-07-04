package web

import (
	"net/url"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/observer"
)

// driveTestServer wires the shared offline fake-gws fixture so Drive
// search/review handlers never touch the network.
func driveTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("DANDORI_GWS_BIN", fakeGwsBinPath(t))
	return testServer(t)
}

func TestDriveSearchExcludesFolders(t *testing.T) {
	s := driveTestServer(t)
	t.Setenv("FAKE_DRIVE_INCLUDE_FOLDER", "1")
	rec := get(t, s, "/contexts/drive-search?q=Plan")
	if rec.Code != 200 {
		t.Fatalf("search: %d body: %s", rec.Code, rec.Body.String())
	}
	if strings.Count(rec.Body.String(), "xem trước") != 1 {
		t.Errorf("expected exactly 1 result row (folder excluded), body: %s", rec.Body.String())
	}
}

func TestDriveReviewTooBigBlocksBody(t *testing.T) {
	s := driveTestServer(t)
	t.Setenv("FAKE_EXPORT_HUGE", "1")
	rec := get(t, s, "/contexts/drive-review?id=f1&name=Huge")
	if rec.Code != 200 {
		t.Fatalf("review: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "vượt 8000") {
		t.Errorf("expected too-big block message, body: %s", rec.Body.String())
	}
}

// C1: a secret-shaped export must be blocked, and the FULL body text must
// never appear anywhere in the response.
func TestDriveReviewSecretBlockedNeverRenders(t *testing.T) {
	s := driveTestServer(t)
	t.Setenv("FAKE_EXPORT_SECRET", "1")
	rec := get(t, s, "/contexts/drive-review?id=f1&name=Leaky")
	if rec.Code != 200 {
		t.Fatalf("review: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "chứa chuỗi giống secret") {
		t.Errorf("expected secret-block message, body: %s", body)
	}
	if strings.Contains(body, "ghp_abcdef1234567890") {
		t.Error("secret token leaked into the rendered review page")
	}
}

// review returns the FULL body (not truncated) — the reviewer must see the
// entire doc, not a head/preview.
func TestDriveReviewReturnsFullBodyNotTruncated(t *testing.T) {
	s := driveTestServer(t)
	rec := get(t, s, "/contexts/drive-review?id=f1&name=Doc")
	if rec.Code != 200 {
		t.Fatalf("review: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "fake exported content") {
		t.Errorf("expected full exported body in review page, body: %s", rec.Body.String())
	}
}

// C1: importing to ANY layer (team, agent, company) must create a
// context-import approval request — never a direct SaveContext. This is the
// core contract of the phase: no layer is exempt from human review.
func TestDriveImportAllLayersAreApprovalGatedNeverDirect(t *testing.T) {
	for _, tc := range []struct{ layer, target string }{
		{"team", "9"}, {"agent", "bot"}, {"company", ""},
	} {
		t.Run(tc.layer, func(t *testing.T) {
			s := driveTestServer(t)
			rec := postForm(t, s, "/contexts/drive-import", url.Values{
				"layer": {tc.layer}, "target": {tc.target},
				"doc_id": {"f1"}, "doc_name": {"Doc"}, "modified": {"2026-07-01T00:00:00Z"},
			})
			if rec.Code != 200 {
				t.Fatalf("import: %d body: %s", rec.Code, rec.Body.String())
			}
			// Zero rows in context_versions — no direct write happened.
			var versions int
			s.Store.DB.QueryRow(`SELECT count(*) FROM context_versions`).Scan(&versions)
			if versions != 0 {
				t.Errorf("context_versions=%d, want 0 (C1: import must never write directly)", versions)
			}
			var pending int
			s.Store.DB.QueryRow(`SELECT count(*) FROM approvals WHERE action LIKE 'observer:context-import:%' AND status='pending'`).Scan(&pending)
			if pending != 1 {
				t.Errorf("pending context-import approvals=%d, want 1", pending)
			}
		})
	}
}

// C1 trust boundary: the operator inbox (/reviews) is where the human
// actually approves — it must show the FULL pinned Drive body, not just a
// short summary/reason. A payload past a truncation boundary must not hide
// at the actual decision point.
func TestReviewsQueueShowsFullImportBody(t *testing.T) {
	s := driveTestServer(t)
	rec := postForm(t, s, "/contexts/drive-import", url.Values{
		"layer": {"team"}, "target": {"9"},
		"doc_id": {"f1"}, "doc_name": {"Playbook"}, "modified": {"2026-07-01T00:00:00Z"},
	})
	if rec.Code != 200 {
		t.Fatalf("import: %d body: %s", rec.Code, rec.Body.String())
	}
	page := get(t, s, "/reviews")
	if page.Code != 200 {
		t.Fatalf("reviews: %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "fake exported content") {
		t.Errorf("expected FULL pinned body visible on /reviews (the actual approval surface), body: %s", page.Body.String())
	}
}

// End to end: request → approve → apply must write the pinned FULL content
// with the Drive provenance note, and the note must be visible on the
// contexts editor page (effective-context view surfaces provenance).
func TestDriveImportApprovalAppliesWithProvenance(t *testing.T) {
	s := driveTestServer(t)
	rec := postForm(t, s, "/contexts/drive-import", url.Values{
		"layer": {"team"}, "target": {"9"},
		"doc_id": {"f1"}, "doc_name": {"Playbook"}, "modified": {"2026-07-01T00:00:00Z"},
	})
	if rec.Code != 200 {
		t.Fatalf("import: %d body: %s", rec.Code, rec.Body.String())
	}
	var apprID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM approvals WHERE action LIKE 'observer:context-import:%'`).Scan(&apprID); err != nil {
		t.Fatal(err)
	}
	if _, err := govern.Decide(s.Store, apprID, true, "phuc", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.RunObserverApplier(s.Store); err != nil {
		t.Fatal(err)
	}
	hub := contexthub.New(s.Store)
	head, err := hub.Head(contexthub.LayerTeam, "9")
	if err != nil {
		t.Fatal(err)
	}
	if head == nil || head.Content != "fake exported content" {
		t.Fatalf("head after approval = %+v, want pinned FULL export saved", head)
	}
	if !strings.Contains(head.Note, "imported from Drive") || !strings.Contains(head.Note, "f1") {
		t.Errorf("note = %q, want Drive provenance", head.Note)
	}
	// Provenance visible on the editor's doc list.
	page := get(t, s, "/contexts")
	if !strings.Contains(page.Body.String(), "imported from Drive") {
		t.Error("provenance note not surfaced on the contexts editor page")
	}
}
