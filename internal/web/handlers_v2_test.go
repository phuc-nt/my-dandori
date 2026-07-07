package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func TestSpikesAndComparePages(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 2)
	testseed.Run(t, s.Store, "r2", "a1", "failed", 1, 3)
	testseed.Event(t, s.Store, "r1", "tool_use", "Bash", -1, "")
	testseed.Event(t, s.Store, "r1", "guardrail_block", "Bash", 0, "")

	if rec := get(t, s, "/spikes"); rec.Code != 200 {
		t.Errorf("/spikes → %d", rec.Code)
	}
	rec := get(t, s, "/runs/compare?ids=r1,r2")
	if rec.Code != 200 {
		t.Fatalf("compare → %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Guardrail blocks", "r1", "r2"} {
		if !strings.Contains(body, want) {
			t.Errorf("compare body missing %q", want)
		}
	}
	if rec := get(t, s, "/runs/compare?ids=r1"); rec.Code != 400 {
		t.Errorf("compare 1 id → %d, want 400", rec.Code)
	}
	if rec := get(t, s, "/runs/compare?ids=r1,ghost"); rec.Code != 404 {
		t.Errorf("compare unknown id → %d, want 404", rec.Code)
	}
}

func TestComplianceExportEndpoint(t *testing.T) {
	s := testServer(t)
	// POST-only: the export writes an audit entry; GET must not exist.
	if rec := get(t, s, "/export/compliance"); rec.Code != 405 {
		t.Errorf("GET export → %d, want 405", rec.Code)
	}
	rec := postForm(t, s, "/export/compliance", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"verify"`) {
		t.Errorf("json export: %d", rec.Code)
	}
	rec = postForm(t, s, "/export/compliance?format=csv", nil)
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "id,ts,actor") {
		t.Errorf("csv export: %d %q", rec.Code, rec.Body.String()[:min(40, rec.Body.Len())])
	}
	// The export itself must land in the audit trail.
	var n int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='compliance_export'`).Scan(&n)
	if n != 2 {
		t.Errorf("export audit entries: %d", n)
	}
}

func TestConfluenceReportUnconfigured(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest("POST", "/reports/confluence", nil)
	req.Host = s.Cfg.Listen
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Errorf("unconfigured report sink → %d, want 503", rec.Code)
	}
	s.ReportSink = func() (string, error) { return "424242", nil }
	req = httptest.NewRequest("POST", "/reports/confluence", nil)
	req.Host = s.Cfg.Listen
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 303 {
		t.Errorf("configured report sink → %d, want 303", rec.Code)
	}
}

func TestRunsPagination(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	for i := 0; i < 55; i++ {
		testseed.Run(t, s.Store, "p-run-"+string(rune('a'+i%26))+string(rune('0'+i/26)), "a1", "done", 0, 0.1)
	}
	rec := get(t, s, "/runs?page=0")
	if !strings.Contains(rec.Body.String(), "Next →") {
		t.Error("page 0 of 55 runs must offer Next")
	}
	rec = get(t, s, "/runs?page=1")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "Next →") {
		t.Errorf("page 1 must be last: %d", rec.Code)
	}
}

var _ = store.Now

// TestPlaybookCreateNominatesNotInserts (H3) proves the viewer-facing
// "save this run as a playbook" button no longer bypasses review: it must
// nominate a kind=playbook knowledge_units row (redirect to the unit detail
// page) and must NOT insert directly into playbooks.
func TestPlaybookCreateNominatesNotInserts(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "pb-r1", "a1", "done", 1, 2.0)
	testseed.Event(t, s.Store, "pb-r1", "tool_use", "Edit", -1, `{"file_path":"api/users.go"}`)

	rec := postForm(t, s, "/runs/pb-r1/playbook", url.Values{"name": {"user-api pattern"}, "notes": {"good run"}})
	if rec.Code != 303 {
		t.Fatalf("nominate playbook → %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/knowledge/unit/") {
		t.Errorf("redirect location = %q, want /knowledge/unit/{id}", loc)
	}
	var pbCount int
	s.Store.DB.QueryRow(`SELECT count(*) FROM playbooks`).Scan(&pbCount)
	if pbCount != 0 {
		t.Errorf("playbooks table must stay empty until knowledge-publish is approved, got %d rows", pbCount)
	}
	var kuCount int
	var title, state string
	s.Store.DB.QueryRow(`SELECT count(*), title, state FROM knowledge_units WHERE kind='playbook'`).Scan(&kuCount, &title, &state)
	if kuCount != 1 || title != "user-api pattern" || state != "nominated" {
		t.Errorf("knowledge_units playbook row: count=%d title=%q state=%q", kuCount, title, state)
	}
	rec = get(t, s, "/knowledge?kind=playbook")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "user-api pattern") {
		t.Errorf("knowledge queue page: %d", rec.Code)
	}
	if rec := postForm(t, s, "/runs/pb-r1/playbook", url.Values{}); rec.Code != 400 {
		t.Errorf("nameless playbook → %d, want 400", rec.Code)
	}
	// A second nominate for the SAME run must be rejected as a duplicate draft
	// (M1/H3), not silently create a second competing knowledge_units row.
	if rec := postForm(t, s, "/runs/pb-r1/playbook", url.Values{"name": {"another name"}}); rec.Code != http.StatusConflict {
		t.Errorf("duplicate nominate → %d, want 409", rec.Code)
	}
}

func TestFailedRunShowsTrace(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "fail-r1", "a1", "failed", 0, 1)
	testseed.Event(t, s.Store, "fail-r1", "tool_result", "Bash", 0, "compile error")
	rec := get(t, s, "/runs/fail-r1")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Why did this fail?") {
		t.Errorf("failed run must show trace banner: %d", rec.Code)
	}
}
