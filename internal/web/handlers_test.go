package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "no.yaml"))
	s, err := New(cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Host = s.Cfg.Listen // pass the origin guard like a real browser tab would
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func postForm(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Host = s.Cfg.Listen
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// The console binds localhost without auth; the origin guard is the boundary
// that keeps foreign hosts (DNS rebinding) and cross-origin POSTs (CSRF
// against approvals/kill switch) out.
func TestOriginGuard(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("foreign host → %d, want 403", rec.Code)
	}

	req = httptest.NewRequest("POST", "/api/kill", strings.NewReader("on=1"))
	req.Host = s.Cfg.Listen
	req.Header.Set("Origin", "http://evil.example.com")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("cross-origin kill POST → %d, want 403", rec.Code)
	}
	if s.Store.Setting("kill_switch_global") == "1" {
		t.Error("cross-origin request must not flip the kill switch")
	}

	req = httptest.NewRequest("POST", "/api/kill", strings.NewReader("on=1"))
	req.Host = s.Cfg.Listen
	req.Header.Set("Origin", "http://"+s.Cfg.Listen)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("same-origin POST → %d, want 303", rec.Code)
	}
}

// Every page must render 200 on an empty DB (graceful empty states).
func TestPagesEmptyDB(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/", "/dash/org", "/runs", "/reviews", "/budgets", "/provenance", "/rules"} {
		if rec := get(t, s, path); rec.Code != 200 {
			t.Errorf("%s → %d", path, rec.Code)
		}
	}
}

func TestPagesWithData(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 1, 1.5)
	testseed.Event(t, s.Store, "r1", "tool_use", "Edit", -1, "{}")
	for _, path := range []string{"/", "/dash/org", "/dash/agent/a1", "/dash/project/proj",
		"/runs", "/runs/r1", "/provenance?agent=a1&metric=acceptance"} {
		rec := get(t, s, path)
		if rec.Code != 200 {
			t.Errorf("%s → %d: %s", path, rec.Code, rec.Body.String()[:min(200, rec.Body.Len())])
		}
		if strings.Contains(rec.Body.String(), "render error") {
			t.Errorf("%s: template error: %s", path, rec.Body.String())
		}
	}
}

func TestApproveFlowWritesAudit(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "running", 0, 0)
	s.Store.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('r1','git push','gate', ?)`, store.Now())

	rec := postForm(t, s, "/reviews/1/decide", url.Values{"decision": {"approve"}, "note": {"lgtm"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("decide → %d", rec.Code)
	}
	var status, by string
	s.Store.DB.QueryRow(`SELECT status, decided_by FROM approvals WHERE id=1`).Scan(&status, &by)
	if status != "approved" || by == "" {
		t.Errorf("approval: %s by %q", status, by)
	}
	var audits int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='approval_approved'`).Scan(&audits)
	if audits != 1 {
		t.Errorf("audit entries: %d", audits)
	}
	// Reject without a note must 400.
	s.Store.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('r1','deploy','gate', ?)`, store.Now())
	if rec := postForm(t, s, "/reviews/2/decide", url.Values{"decision": {"reject"}}); rec.Code != 400 {
		t.Errorf("reject without note → %d, want 400", rec.Code)
	}
}

func TestKillAndBudgetActions(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "running", 0, 0)

	if rec := postForm(t, s, "/runs/r1/kill", url.Values{"reason": {"stuck"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("kill → %d", rec.Code)
	}
	var status string
	s.Store.DB.QueryRow(`SELECT status FROM runs WHERE id='r1'`).Scan(&status)
	if status != "killed" {
		t.Errorf("status: %s", status)
	}

	if rec := postForm(t, s, "/budgets", url.Values{
		"scope_type": {"agent"}, "scope_id": {"a1"}, "limit_usd": {"25"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("budget set → %d", rec.Code)
	}
	var limit float64
	s.Store.DB.QueryRow(`SELECT limit_usd FROM budgets WHERE scope_type='agent' AND scope_id='a1'`).Scan(&limit)
	if limit != 25 {
		t.Errorf("limit: %f", limit)
	}

	// Global kill from header.
	if rec := postForm(t, s, "/api/kill", url.Values{"on": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("global kill → %d", rec.Code)
	}
	if s.Store.Setting("kill_switch_global") != "1" {
		t.Error("kill switch not set")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
