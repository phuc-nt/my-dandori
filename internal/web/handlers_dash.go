package web

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
)

// handleStandup is the landing page: what ran overnight + what needs you now.
func (s *Server) handleStandup(w http.ResponseWriter, r *http.Request) {
	runs, _ := s.queryRuns(`WHERE started_at >= datetime('now', '-1 day')`)
	pending, _ := s.queryApprovals("pending")
	flags, _ := s.queryFlags("open")
	budgets, _ := s.queryBudgets()
	board, _ := s.leaderboard()
	s.render(w, r, "standup", map[string]any{
		"Page": "standup", "KillOn": s.Store.Setting("kill_switch_global") == "1",
		"Runs": runs, "Pending": pending, "Flags": flags,
		"Budgets": budgets, "Board": board, "User": s.Cfg.UserName,
	})
}

// handleDashOrg: fleet KPIs, leaderboard, cost trend, grade distribution.
func (s *Server) handleDashOrg(w http.ResponseWriter, r *http.Request) {
	board, err := s.leaderboard()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	labels, values, _ := s.costTrend(14)
	var totalCost float64
	var totalRuns int
	for _, row := range board {
		totalCost += row.CostUSD
		totalRuns += row.Runs
	}
	fleetROI, _ := learn.ComputeROI(s.Store, "", s.Cfg.LearnWindowDays, avgAcceptance(board))
	dora, _ := learn.ComputeDORALite(s.Store, s.Cfg.LearnWindowDays)
	chart, _ := json.Marshal(map[string]any{
		"labels": labels, "values": values, "dist": learn.GradeDistribution(board),
	})
	s.render(w, r, "dash_org", map[string]any{
		"Page": "org", "KillOn": s.Store.Setting("kill_switch_global") == "1",
		"Board": board, "TotalCost": totalCost, "TotalRuns": totalRuns,
		"ROI": fleetROI, "DORA": dora, "ChartJSON": string(chart), "Window": s.Cfg.LearnWindowDays,
		"AgentBands": s.queryBands(),
	})
}

func avgAcceptance(board []learn.LeaderboardRow) float64 {
	if len(board) == 0 {
		return 100
	}
	var sum float64
	for _, r := range board {
		sum += r.Metrics.Acceptance.Value
	}
	return sum / float64(len(board))
}

// handleDashProject: runs/cost/flags for one project.
func (s *Server) handleDashProject(w http.ResponseWriter, r *http.Request) {
	p := chi.URLParam(r, "project")
	runs, _ := s.queryRuns(`WHERE project = ?`, p)
	var cost float64
	for _, run := range runs {
		cost += run.CostUSD
	}
	flags, _ := s.queryFlags("open")
	s.render(w, r, "dash_project", map[string]any{
		"Page": "project", "Project": p, "Runs": runs, "Cost": cost, "Flags": flags,
	})
}

// handleDashAgent: the performance-review page for one agent.
func (s *Server) handleDashAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "agent")
	m, err := learn.Compute(s.Store, id, s.Cfg.LearnWindowDays)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	composites, _ := learn.FleetComposites(s.Store, s.Cfg.LearnWindowDays)
	fleet := make([]float64, 0, len(composites))
	for _, v := range composites {
		fleet = append(fleet, v)
	}
	var humans []float64
	if s.Cfg.CalibrateWithHumans {
		humans, _ = learn.HumanBaseline(s.Store, s.Cfg.LearnWindowDays)
		fleet = append(fleet, humans...)
	}
	grade := learn.GradeFor(m.Composite, fleet)
	grade.Humans = len(humans)
	grade.LowConfidence = m.Runs < 5
	roi, _ := learn.ComputeROI(s.Store, id, s.Cfg.LearnWindowDays, m.Acceptance.Value)
	runs, _ := s.queryRuns(`WHERE agent_id = ?`, id)
	s.render(w, r, "dash_agent", map[string]any{
		"Page": "agent", "M": m, "Grade": grade,
		"ROI": roi, "Runs": runs, "Band": govern.BandFor(s.Store, id),
		"Bands": []string{govern.BandSupervised, govern.BandGated, govern.BandTrusted},
	})
}
