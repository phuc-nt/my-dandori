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
// fleet — including anonymous human baselines when enabled (the vision's
// "kể cả của người") — and sorts by composite descending. Each agent is
// Computed exactly once for the main window.
func Leaderboard(st *store.Store, windowDays int) ([]LeaderboardRow, error) {
	return LeaderboardCalibrated(st, windowDays, true)
}

// LeaderboardCalibrated lets callers exclude the human baseline
// (config `calibrate_with_humans: false`).
func LeaderboardCalibrated(st *store.Store, windowDays int, withHumans bool) ([]LeaderboardRow, error) {
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
	var humans []float64
	if withHumans {
		if humans, err = HumanBaseline(st, windowDays); err != nil {
			return nil, err
		}
		fleet = append(fleet, humans...)
	}
	rows := make([]LeaderboardRow, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		m := metrics[agentID]
		roi, err := ComputeROI(st, agentID, windowDays, m.Acceptance.Value)
		if err != nil {
			return nil, err
		}
		grade := GradeFor(m.Composite, fleet)
		grade.Humans = len(humans)
		grade.LowConfidence = m.Runs < 5
		rows = append(rows, LeaderboardRow{
			AgentID:    agentID,
			AgentName:  m.AgentName,
			Grade:      grade,
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
