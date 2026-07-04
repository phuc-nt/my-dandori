package web

import (
	"errors"
	"html/template"
	"net/http"
	"os"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/runner"
)

// launchableAgents returns the agents the console may launch: the intersection
// of configured binaries and the launcher's enabled set (claude only in v6).
func (s *Server) launchableAgents() []string {
	var out []string
	for name := range s.Cfg.AgentBinaries {
		if name == "claude" { // v6: argvFor enables claude only
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// projectDirs lists the immediate subdirectories of ProjectsDir — the only
// cwds a run may launch in. The client can only pick what we offer, and
// runner.Launch re-validates the boundary regardless (defense in depth).
func (s *Server) projectDirs() []string {
	entries, err := os.ReadDir(s.Cfg.ProjectsDir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs
}

func (s *Server) handleLaunchForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "launch", map[string]any{
		"Page": "launch", "Agents": s.launchableAgents(), "Dirs": s.projectDirs(),
		"ProjectsDir": s.Cfg.ProjectsDir,
	})
}

// handleLaunch starts an async console run and HX-Redirects to its live detail.
func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	if s.Launcher == nil {
		http.Error(w, "launcher not available", http.StatusServiceUnavailable)
		return
	}
	agent := r.FormValue("agent")
	prompt := r.FormValue("prompt")
	cwd := s.resolveCwd(r.FormValue("cwd"))
	runID, err := s.Launcher.Launch(agent, prompt, cwd, s.launchActor(), "")
	if s.launchError(w, r, err) {
		return
	}
	a := &govern.Audit{St: s.Store, Actor: s.launchActor()}
	_, _ = a.Append("run_launched", runID, "agent="+agent+" cwd="+cwd)
	w.Header().Set("HX-Redirect", "/runs/"+runID)
	w.WriteHeader(http.StatusNoContent)
}

// handleRetryForm renders the launch form prefilled from a finished console
// run's stored launch prompt.
func (s *Server) handleRetryForm(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var source, agentName, cwd, prompt string
	s.Store.Read().QueryRow(`SELECT r.source, COALESCE(a.name,''), COALESCE(r.cwd,'')
		FROM runs r LEFT JOIN agents a ON a.id = r.agent_id WHERE r.id = ?`, id).
		Scan(&source, &agentName, &cwd)
	if source != "console" {
		http.Error(w, "chỉ có thể chạy lại run phóng từ console", http.StatusBadRequest)
		return
	}
	s.Store.Read().QueryRow(`SELECT payload FROM events WHERE run_id = ? AND kind = 'launch_prompt'`, id).Scan(&prompt)
	s.render(w, r, "launch", map[string]any{
		"Page": "launch", "Agents": s.launchableAgents(), "Dirs": s.projectDirs(),
		"ProjectsDir": s.Cfg.ProjectsDir,
		"RetryOf":     id, "PrefillPrompt": prompt, "PrefillAgent": agentName, "PrefillCwd": cwd,
	})
}

// handleRetry launches a new run linked to the original. retry_of comes from
// the URL PATH (M5) — never a client field — so a crafted POST cannot forge
// lineage.
func (s *Server) handleRetry(w http.ResponseWriter, r *http.Request) {
	if s.Launcher == nil {
		http.Error(w, "launcher not available", http.StatusServiceUnavailable)
		return
	}
	originalID := chi.URLParam(r, "id")
	var source string
	if err := s.Store.Read().QueryRow(`SELECT source FROM runs WHERE id = ?`, originalID).Scan(&source); err != nil || source != "console" {
		http.Error(w, "run gốc không hợp lệ", http.StatusBadRequest)
		return
	}
	agent := r.FormValue("agent")
	prompt := r.FormValue("prompt")
	cwd := s.resolveCwd(r.FormValue("cwd"))
	runID, err := s.Launcher.Launch(agent, prompt, cwd, s.launchActor(), originalID)
	if s.launchError(w, r, err) {
		return
	}
	a := &govern.Audit{St: s.Store, Actor: s.launchActor()}
	_, _ = a.Append("run_retried", runID, "from="+originalID)
	w.Header().Set("HX-Redirect", "/runs/"+runID)
	w.WriteHeader(http.StatusNoContent)
}

// launchError renders a friendly in-form error and reports whether it handled
// the response. Capacity → 429; refusal/bad input → 200 with the message.
func (s *Server) launchError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	code := http.StatusOK
	if errors.Is(err, runner.ErrAtCapacity) {
		code = http.StatusTooManyRequests
	}
	w.WriteHeader(code)
	w.Write([]byte(`<div class="bg-red-50 border border-red-200 rounded p-3 text-sm text-red-700">` +
		template.HTMLEscapeString(msg) + `</div>`))
	return true
}

// resolveCwd joins a chosen project subdir onto ProjectsDir. An absolute or
// escaping value is left as-is for runner.Launch to reject.
func (s *Server) resolveCwd(dir string) string {
	if dir == "" {
		return s.Cfg.ProjectsDir
	}
	return s.Cfg.ProjectsDir + string(os.PathSeparator) + dir
}

func (s *Server) launchActor() string { return s.Cfg.UserName + "@console" }
