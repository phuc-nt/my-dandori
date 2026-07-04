package web

import (
	"net/http"
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
