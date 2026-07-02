package web

import (
	"net/http"
	"strconv"
)

// handleBudgets renders the budget policy form + live spend table (UE1/UA3).
func (s *Server) handleBudgets(w http.ResponseWriter, r *http.Request) {
	budgets, err := s.queryBudgets()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	agents := s.listAgents()
	projects := s.listProjects()
	data := map[string]any{"Page": "budgets", "Budgets": budgets, "Agents": agents, "Projects": projects}
	if isHTMX(r) {
		s.renderFragment(w, "budgets", "budgets_table", data)
		return
	}
	s.render(w, r, "budgets", data)
}

// handleBudgetSet upserts a budget limit; the circuit breaker reads the table
// on every tool call, so the new value applies immediately.
func (s *Server) handleBudgetSet(w http.ResponseWriter, r *http.Request) {
	scopeType := r.FormValue("scope_type")
	scopeID := r.FormValue("scope_id")
	limit, err := strconv.ParseFloat(r.FormValue("limit_usd"), 64)
	if err != nil || limit < 0 {
		http.Error(w, "invalid limit", 400)
		return
	}
	switch scopeType {
	case "global", "agent", "project":
	default:
		http.Error(w, "invalid scope", 400)
		return
	}
	if scopeType == "global" {
		scopeID = ""
	}
	_, err = s.Store.DB.Exec(`INSERT INTO budgets(scope_type, scope_id, limit_usd) VALUES(?, ?, ?)
		ON CONFLICT(scope_type, scope_id) DO UPDATE SET limit_usd = excluded.limit_usd`,
		scopeType, scopeID, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit("set_budget", scopeType+":"+scopeID, r.FormValue("limit_usd"))
	redirectBack(w, r, "/budgets")
}

func (s *Server) listAgents() []string {
	rows, err := s.Store.DB.Query(`SELECT id FROM agents ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			out = append(out, v)
		}
	}
	return out
}

func (s *Server) listProjects() []string {
	rows, err := s.Store.DB.Query(`SELECT DISTINCT project FROM runs WHERE project != '' ORDER BY project`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			out = append(out, v)
		}
	}
	return out
}
