package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

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

// handleRules lists guardrail rules with enable/disable toggles.
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.queryRules()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "rules", map[string]any{"Page": "rules", "Rules": rules})
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
	s.audit("toggle_rule", chi.URLParam(r, "id"), "")
	redirectBack(w, r, "/rules")
}
