package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
)

// handleProvenance (UF2): pick agent + metric → formula, value, and the raw
// rows that produced the number. Trust through verifiability.
func (s *Server) handleProvenance(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	metricName := r.URL.Query().Get("metric")
	data := map[string]any{
		"Page": "provenance", "Agents": s.listAgents(),
		"Agent": agent, "MetricName": metricName,
		"Metrics": []string{"acceptance", "success", "autonomy", "reliability"},
	}
	if agent != "" && metricName != "" {
		metric, rows, err := learn.Provenance(s.Store, agent, metricName, s.Cfg.LearnWindowDays)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		data["Metric"] = metric
		data["Rows"] = rows
	}
	s.render(w, r, "provenance", data)
}

// handleRules lists guardrail rules with toggles, the add-rule form and the
// policy simulator (UE4).
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.queryRules()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "rules", map[string]any{"Page": "rules", "Rules": rules,
		"Agents": s.listAgents(), "Projects": s.listProjects(), "Window": s.Cfg.LearnWindowDays})
}

// handleRuleSimulate replays a candidate rule against history — nothing is
// saved; the result table shows exactly what would have been blocked.
func (s *Server) handleRuleSimulate(w http.ResponseWriter, r *http.Request) {
	res, err := govern.Simulate(s.Store, r.FormValue("pattern"),
		formOr(r, "scope_type", "global"), r.FormValue("scope_id"), s.Cfg.LearnWindowDays)
	if err != nil {
		w.WriteHeader(400)
		s.renderFragment(w, r, "rules", "sim_result", map[string]any{"Error": err.Error()})
		return
	}
	s.renderFragment(w, r, "rules", "sim_result", map[string]any{"Sim": res, "Window": s.Cfg.LearnWindowDays})
}

// handleRuleCreate persists a new rule from the builder form.
func (s *Server) handleRuleCreate(w http.ResponseWriter, r *http.Request) {
	_, err := govern.CreateRule(s.Store, r.FormValue("kind"), r.FormValue("pattern"),
		r.FormValue("description"), formOr(r, "scope_type", "global"), r.FormValue("scope_id"),
		r.FormValue("critical") == "1", s.actor(r))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	redirectBack(w, r, "/rules")
}

// handleRuleDelete removes a rule.
func (s *Server) handleRuleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	if err := govern.DeleteRule(s.Store, id, s.actor(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	redirectBack(w, r, "/rules")
}

func formOr(r *http.Request, key, def string) string {
	if v := r.FormValue(key); v != "" {
		return v
	}
	return def
}

// handleRuleToggle flips one rule on/off with audit.
func (s *Server) handleRuleToggle(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	if _, err := s.Store.DB.Exec(`UPDATE guardrail_rules SET enabled = 1 - enabled WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "toggle_rule", chi.URLParam(r, "id"), "")
	redirectBack(w, r, "/rules")
}
