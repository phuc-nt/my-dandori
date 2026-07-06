package web

import (
	"net/http"
	"strconv"

	"github.com/phuc-nt/dandori/internal/learn"
)

// insightsWindow reads the ?days filter, defaulting to the configured learn
// window. days=0 (all-time) is allowed explicitly via ?days=0.
func (s *Server) insightsWindow(r *http.Request) int {
	if v := r.URL.Query().Get("days"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d >= 0 {
			return d
		}
	}
	return s.Cfg.LearnWindowDays
}

// handleInsights renders the data-driven efficiency page: per-model cost &
// cache efficiency (phase 6) and cost-per-outcome by project/agent (phase 7).
// Every ratio ships with its sample size; rows under MinSampleForInsight are
// flagged, never compared as if reliable.
func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	days := s.insightsWindow(r)
	models, err := learn.ModelStats(s.Store, days)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	best, worst, err := learn.TopCacheRuns(s.Store, days, 5)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	group := r.URL.Query().Get("group")
	if group != "agent" {
		group = "project"
	}
	outcomes, err := learn.CostPerOutcome(s.Store, days, group)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "insights", map[string]any{
		"Page": "insights", "Window": days, "MinSample": learn.MinSampleForInsight,
		"Models": models, "BestCache": best, "WorstCache": worst,
		"Outcomes": outcomes, "Group": group,
	})
}
