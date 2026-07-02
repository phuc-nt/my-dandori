// Package learn computes agent evaluation from raw captured data: four
// metrics → composite → fleet-calibrated grade, ROI, leaderboard. Every
// number carries provenance (source run/event ids) — no hidden thresholds.
package learn

import (
	"fmt"

	"github.com/phuc-nt/dandori/internal/store"
)

// Metric is one 0–100 score with full provenance.
type Metric struct {
	Name     string
	Value    float64
	Formula  string // human-readable derivation, e.g. "4/5 ended runs done"
	RunIDs   []string
	EventIDs []int64
}

// AgentMetrics is the full evaluation of one agent over a window (days).
type AgentMetrics struct {
	AgentID     string
	AgentName   string
	WindowDays  int
	Acceptance  Metric
	Success     Metric
	Autonomy    Metric
	Reliability Metric
	Composite   float64
	Runs        int
	CostUSD     float64
}

func windowClause(days int) string {
	return fmt.Sprintf("datetime('now', '-%d days')", days)
}

// Compute evaluates one agent. Pure function of the store — deterministic.
func Compute(st *store.Store, agentID string, windowDays int) (*AgentMetrics, error) {
	m := &AgentMetrics{AgentID: agentID, WindowDays: windowDays}
	_ = st.DB.QueryRow(`SELECT name FROM agents WHERE id = ?`, agentID).Scan(&m.AgentName)
	if err := st.DB.QueryRow(`SELECT count(*), COALESCE(sum(cost_usd),0) FROM runs
		WHERE agent_id = ? AND started_at >= `+windowClause(windowDays), agentID).
		Scan(&m.Runs, &m.CostUSD); err != nil {
		return nil, err
	}
	var err error
	if m.Acceptance, err = acceptance(st, agentID, windowDays); err != nil {
		return nil, err
	}
	if m.Success, err = success(st, agentID, windowDays); err != nil {
		return nil, err
	}
	if m.Autonomy, err = autonomy(st, agentID, windowDays); err != nil {
		return nil, err
	}
	if m.Reliability, err = reliability(st, agentID, windowDays); err != nil {
		return nil, err
	}
	m.Composite = (m.Acceptance.Value + m.Success.Value + m.Autonomy.Value + m.Reliability.Value) / 4
	return m, nil
}

// agentRunIDs lists this agent's runs inside the window.
func agentRunIDs(st *store.Store, agentID string, days int) ([]string, error) {
	rows, err := st.DB.Query(`SELECT id FROM runs WHERE agent_id = ?
		AND started_at >= `+windowClause(days), agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// eventIDs collects event ids of given kinds across the agent's window runs.
func eventIDs(st *store.Store, agentID string, days int, kinds string, extra string) ([]int64, error) {
	q := `SELECT e.id FROM events e JOIN runs r ON r.id = e.run_id
		WHERE r.agent_id = ? AND r.started_at >= ` + windowClause(days) +
		` AND e.kind IN (` + kinds + `)` + extra
	rows, err := st.DB.Query(q, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
