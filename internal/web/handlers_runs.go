package web

import (
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/store"
)

const runsPageSize = 50

// handleRuns lists runs with filters and offset pagination; HTMX requests
// get just the tbody.
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	where, args := runFilters(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	runs, err := s.queryRunsPage(where, page, args...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	hasNext := len(runs) > runsPageSize
	if hasNext {
		runs = runs[:runsPageSize]
	}
	data := map[string]any{"Page": "runs", "Runs": runs, "Q": r.URL.Query(),
		"PageNum": page, "HasNext": hasNext, "PrevPage": page - 1, "NextPage": page + 1}
	if isHTMX(r) {
		s.renderFragment(w, "runs", "runs_tbody", data)
		return
	}
	s.render(w, r, "runs", data)
}

// queryRunsPage fetches one page plus a lookahead row for HasNext.
func (s *Server) queryRunsPage(where string, page int, args ...any) ([]RunRow, error) {
	q := `SELECT id, session_id, COALESCE(agent_id,''), COALESCE(project,''), COALESCE(task_key,''),
		status, COALESCE(model,''), COALESCE(started_at,''), COALESCE(ended_at,''), source, runtime,
		cost_usd, input_tokens, output_tokens, lines_added, lines_deleted
		FROM runs ` + where + ` ORDER BY started_at DESC LIMIT ? OFFSET ?`
	rows, err := s.Store.DB.Query(q, append(args, runsPageSize+1, page*runsPageSize)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRow
	for rows.Next() {
		var r RunRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.AgentID, &r.Project, &r.TaskKey, &r.Status,
			&r.Model, &r.StartedAt, &r.EndedAt, &r.Source, &r.Runtime,
			&r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.LinesAdded, &r.LinesDeleted); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func runFilters(r *http.Request) (string, []any) {
	where, args := "WHERE 1=1", []any{}
	if v := r.URL.Query().Get("agent"); v != "" {
		where += " AND agent_id = ?"
		args = append(args, v)
	}
	if v := r.URL.Query().Get("project"); v != "" {
		where += " AND project = ?"
		args = append(args, v)
	}
	if v := r.URL.Query().Get("status"); v != "" {
		where += " AND status = ?"
		args = append(args, v)
	}
	return where, args
}

// handleRunDetail: event timeline with usage/cost and action buttons.
func (s *Server) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	runs, err := s.queryRuns(`WHERE id = ?`, id)
	if err != nil || len(runs) == 0 {
		http.NotFound(w, r)
		return
	}
	events, _ := s.queryEvents(id)
	flags, _ := s.queryFlags("open")
	s.render(w, r, "run_detail", map[string]any{
		"Page": "runs", "Run": runs[0], "Events": events, "Flags": flags,
	})
}

// handleRunKill kills one running session (UA2).
func (s *Server) handleRunKill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	reason := r.FormValue("reason")
	if reason == "" {
		reason = "killed from console"
	}
	if err := govern.KillRun(s.Store, id, s.Cfg.UserName, reason); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	redirectBack(w, r, "/runs/"+id)
}

// handleRunTaskKey sets/corrects the Jira task attribution inline (UA3-style).
func (s *Server) handleRunTaskKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := r.FormValue("task_key")
	if _, err := s.Store.DB.Exec(`UPDATE runs SET task_key = ? WHERE id = ?`, key, id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit("set_task_key", id, key)
	redirectBack(w, r, "/runs/"+id)
}

// handleRunFlag opens a flag on a run (UC1 — the Jira leg attaches in the
// integrations phase via the flag sink).
func (s *Server) handleRunFlag(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	reason := r.FormValue("reason")
	if reason == "" {
		reason = "flagged from console"
	}
	res, err := s.Store.DB.Exec(`INSERT INTO flags(run_id, reason, created_at) VALUES(?, ?, ?)`,
		id, reason, store.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	flagID, _ := res.LastInsertId()
	s.audit("flag_run", id, reason)
	if s.FlagSink != nil {
		go s.FlagSink(flagID) // integrations: flag → Jira ticket
	}
	redirectBack(w, r, "/runs/"+id)
}

// handleGlobalKill toggles the global kill switch from the header button.
func (s *Server) handleGlobalKill(w http.ResponseWriter, r *http.Request) {
	on := r.FormValue("on") == "1"
	reason := r.FormValue("reason")
	if reason == "" {
		reason = "toggled from console header"
	}
	if err := govern.SetGlobalKill(s.Store, on, s.Cfg.UserName, reason); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	redirectBack(w, r, "/")
}

// audit appends a console-actor audit entry; failures are loud in the log —
// a silent audit gap defeats the point of having one.
func (s *Server) audit(action, subject, detail string) {
	a := &govern.Audit{St: s.Store, Actor: s.Cfg.UserName}
	if _, err := a.Append(action, subject, detail); err != nil {
		log.Printf("AUDIT WRITE FAILED action=%s subject=%s: %v", action, subject, err)
	}
}

// redirectBack sends HTMX callers a refresh and normal forms a redirect.
func redirectBack(w http.ResponseWriter, r *http.Request, fallback string) {
	if isHTMX(r) {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	to := r.Header.Get("Referer")
	if to == "" {
		to = fallback
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
