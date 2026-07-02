package learn

import (
	"sort"

	"github.com/phuc-nt/dandori/internal/store"
)

// LeaderboardRow is one fleet entry, ready for display.
type LeaderboardRow struct {
	AgentID    string
	AgentName  string
	Grade      Grade
	Composite  float64
	Runs       int
	CostUSD    float64
	ROI        *ROI
	TrendDelta float64 // composite last 7d minus previous 7d
	Metrics    *AgentMetrics
}

// Leaderboard evaluates every active agent, calibrates grades against the
// fleet, and sorts by composite descending. Each agent is Computed exactly
// once for the main window — the metrics feed both the calibration
// distribution and the display row (no N+1 recompute).
func Leaderboard(st *store.Store, windowDays int) ([]LeaderboardRow, error) {
	agentIDs, err := activeAgents(st, windowDays)
	if err != nil {
		return nil, err
	}
	metrics := make(map[string]*AgentMetrics, len(agentIDs))
	fleet := make([]float64, 0, len(agentIDs))
	for _, id := range agentIDs {
		m, err := Compute(st, id, windowDays)
		if err != nil {
			return nil, err
		}
		metrics[id] = m
		fleet = append(fleet, m.Composite)
	}
	rows := make([]LeaderboardRow, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		m := metrics[agentID]
		roi, err := ComputeROI(st, agentID, windowDays, m.Acceptance.Value)
		if err != nil {
			return nil, err
		}
		rows = append(rows, LeaderboardRow{
			AgentID:    agentID,
			AgentName:  m.AgentName,
			Grade:      GradeFor(m.Composite, fleet),
			Composite:  m.Composite,
			Runs:       m.Runs,
			CostUSD:    m.CostUSD,
			ROI:        roi,
			TrendDelta: trendDelta(st, agentID),
			Metrics:    m,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Composite > rows[j].Composite })
	return rows, nil
}

// trendDelta compares the last 7 days' composite with the 7 days before.
// Errors degrade to 0 (trend is decorative, never blocks the board).
func trendDelta(st *store.Store, agentID string) float64 {
	last, err1 := Compute(st, agentID, 7)
	if err1 != nil || last.Runs == 0 {
		return 0
	}
	prev14, err2 := Compute(st, agentID, 14)
	if err2 != nil || prev14.Runs <= last.Runs {
		return 0 // no activity in the earlier half → no trend
	}
	// Approximation: previous-week composite from the 14d window minus the
	// 7d window (documented; exact windowing is [Sau]).
	return last.Composite - prev14.Composite
}

// GradeDistribution counts letters across the board (for the org chart).
func GradeDistribution(rows []LeaderboardRow) map[string]int {
	dist := map[string]int{"A": 0, "B": 0, "C": 0, "D": 0, "F": 0}
	for _, r := range rows {
		dist[r.Grade.Letter]++
	}
	return dist
}
