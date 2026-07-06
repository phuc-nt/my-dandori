package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/runner"
)

// wireLauncher attaches a real launcher to a test server, with claude mapped
// to /bin/echo and a temp projects dir.
func wireLauncher(t *testing.T, s *Server) string {
	t.Helper()
	proj := t.TempDir()
	s.Cfg.ProjectsDir = proj
	s.Cfg.AgentBinaries = map[string]string{"claude": "/bin/echo"}
	s.Cfg.MaxConcurrentLaunches = 2
	s.Cfg.UserName = "phuc"
	s.Launcher = runner.New(s.Cfg, s.Store, &capture.Ingestor{Cfg: s.Cfg, St: s.Store})
	return proj
}

func TestLaunchCreatesRunAndAudits(t *testing.T) {
	s := testServer(t)
	wireLauncher(t, s)
	rec := postForm(t, s, "/launch", url.Values{"agent": {"claude"}, "prompt": {"xin chào"}, "cwd": {""}})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("launch: code %d body=%s", rec.Code, rec.Body)
	}
	redir := rec.Header().Get("HX-Redirect")
	if !strings.HasPrefix(redir, "/runs/") {
		t.Fatalf("no HX-Redirect to run detail: %q", redir)
	}
	runID := strings.TrimPrefix(redir, "/runs/")
	var source, launchedBy string
	s.Store.DB.QueryRow(`SELECT source, COALESCE(launched_by,'') FROM runs WHERE id=?`, runID).Scan(&source, &launchedBy)
	if source != "console" || launchedBy != "phuc@console" {
		t.Errorf("run source=%q launched_by=%q", source, launchedBy)
	}
	var audits int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='run_launched'`).Scan(&audits)
	if audits != 1 {
		t.Errorf("run_launched audit rows: %d", audits)
	}
}

func TestLaunchRefusedByKillSwitchInForm(t *testing.T) {
	s := testServer(t)
	wireLauncher(t, s)
	s.Store.SetSetting("kill_switch_global", "1")
	rec := postForm(t, s, "/launch", url.Values{"agent": {"claude"}, "prompt": {"hi"}, "cwd": {""}})
	if rec.Code != http.StatusOK { // in-form error, not a redirect
		t.Errorf("kill-switch launch: code %d, want 200 with error", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "kill switch") {
		t.Errorf("no refusal message: %s", rec.Body.String())
	}
	var n int
	s.Store.DB.QueryRow(`SELECT count(*) FROM runs WHERE source='console'`).Scan(&n)
	if n != 0 {
		t.Error("run created despite kill switch")
	}
}

func TestLaunchRejectsNonClaudeAgent(t *testing.T) {
	s := testServer(t)
	wireLauncher(t, s)
	rec := postForm(t, s, "/launch", url.Values{"agent": {"codex"}, "prompt": {"hi"}, "cwd": {""}})
	// codex not launchable → error rendered, no run.
	if strings.Contains(rec.Header().Get("HX-Redirect"), "/runs/") {
		t.Error("non-claude agent launched")
	}
}

// M5: retry_of comes from the URL PATH; a crafted hidden form field must not
// change lineage.
func TestRetryLineageFromPathNotForm(t *testing.T) {
	s := testServer(t)
	proj := wireLauncher(t, s)
	_ = proj
	// Seed an original console run with a stored launch prompt.
	s.Store.DB.Exec(`INSERT INTO agents(id,name,created_at) VALUES('claude','claude','now') ON CONFLICT DO NOTHING`)
	s.Store.DB.Exec(`INSERT INTO runs(id,session_id,agent_id,source,status,started_at) VALUES('orig','orig','claude','console','done','now')`)
	s.Store.DB.Exec(`INSERT INTO events(run_id,ts,kind,payload) VALUES('orig','now','launch_prompt','prompt cũ')`)

	// Craft a POST to /runs/orig/retry with a bogus retry_of form field.
	rec := postForm(t, s, "/runs/orig/retry", url.Values{
		"agent": {"claude"}, "prompt": {"prompt mới"}, "cwd": {""},
		"retry_of": {"HACKED"}, // must be ignored — path wins
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("retry: code %d body=%s", rec.Code, rec.Body)
	}
	newID := strings.TrimPrefix(rec.Header().Get("HX-Redirect"), "/runs/")
	var retryOf string
	s.Store.DB.QueryRow(`SELECT COALESCE(retry_of,'') FROM runs WHERE id=?`, newID).Scan(&retryOf)
	if retryOf != "orig" {
		t.Errorf("retry_of = %q, want 'orig' (from path, not the HACKED form field)", retryOf)
	}
}

func TestRetryRejectsNonConsoleOrigin(t *testing.T) {
	s := testServer(t)
	wireLauncher(t, s)
	s.Store.DB.Exec(`INSERT INTO agents(id,name,created_at) VALUES('a','a','now') ON CONFLICT DO NOTHING`)
	s.Store.DB.Exec(`INSERT INTO runs(id,session_id,agent_id,source,status,started_at) VALUES('hookrun','hookrun','a','hook','done','now')`)
	rec := postForm(t, s, "/runs/hookrun/retry", url.Values{"agent": {"claude"}, "prompt": {"x"}, "cwd": {""}})
	if rec.Code == http.StatusNoContent {
		t.Error("retry of a non-console (hook) run should be rejected")
	}
}

// P2 smoke test: approve + kill + launch, under a REAL logged-in session (not
// local-trust), must each attribute their audit entry to the real operator
// principal — never a stale hardcoded "@console"/Cfg.UserName string. This
// covers more than approve alone (the spec calls out kill/launch explicitly
// since execActor/launchActor were separate hardcoded helpers pre-P2).
func TestApproveKillLaunchAuditRealPrincipal(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	wireLauncher(t, s)
	operator := mustCreateAccount(t, s, "priya", "admin")
	sessID, err := s.sessions.Create(operator)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	authed := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		var body *strings.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		} else {
			body = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, body)
		req.Host = s.Cfg.Listen
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessID})
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	// Approve: seed a pending approval, decide it, check decided_by.
	s.Store.DB.Exec(`INSERT INTO agents(id,name,created_at) VALUES('bot','bot','now') ON CONFLICT DO NOTHING`)
	s.Store.DB.Exec(`INSERT INTO runs(id,session_id,agent_id,source,status,started_at) VALUES('r1','r1','bot','console','running','now')`)
	s.Store.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('r1', 'git push', 'gate', 'now')`)
	rec := authed("POST", "/reviews/1/decide", url.Values{"decision": {"approve"}})
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent && rec.Code != http.StatusSeeOther {
		t.Fatalf("approve decide: code %d body=%s", rec.Code, rec.Body)
	}
	var decidedBy string
	s.Store.DB.QueryRow(`SELECT COALESCE(decided_by,'') FROM approvals WHERE id=1`).Scan(&decidedBy)
	if decidedBy != operator {
		t.Errorf("approve decided_by = %q, want real principal %q (not @console)", decidedBy, operator)
	}

	// Kill: the running console run above.
	rec = authed("POST", "/runs/r1/kill", url.Values{"reason": {"smoke test"}})
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent && rec.Code != http.StatusSeeOther {
		t.Fatalf("kill: code %d body=%s", rec.Code, rec.Body)
	}
	var killAudits int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='kill_run' AND actor = ?`, operator).Scan(&killAudits)
	if killAudits == 0 {
		t.Error("kill_run audit entry missing real principal")
	}

	// Launch: a fresh console run.
	rec = authed("POST", "/launch", url.Values{"agent": {"claude"}, "prompt": {"xin chào"}, "cwd": {""}})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("launch: code %d body=%s", rec.Code, rec.Body)
	}
	var launchAudits int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='run_launched' AND actor = ?`, operator).Scan(&launchAudits)
	if launchAudits == 0 {
		t.Error("run_launched audit entry missing real principal")
	}

	// Never a stale hardcoded actor for any of the three audited actions above.
	var staleAudits int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action IN ('approval_approved','kill_run','run_launched') AND actor LIKE '%@console'`).Scan(&staleAudits)
	if staleAudits != 0 {
		t.Errorf("found %d audit entries still attributed to @console under a real session", staleAudits)
	}
}
