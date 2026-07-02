package learn

import (
	"fmt"
	"sort"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// spike thresholds: today's spend must exceed spikeFactor × the median of
// the previous 7 days AND the absolute floor (tiny fleets shouldn't alarm).
const (
	spikeFactor   = 3.0
	spikeFloorUSD = 1.0
)

// DetectSpikes compares each agent's spend today against its own recent
// history and records one cost_spike event per agent per day. Returns the
// agents that spiked. Deterministic: same data, same result.
func DetectSpikes(st *store.Store) ([]string, error) {
	today := time.Now().UTC().Format("2006-01-02")
	rows, err := st.DB.Query(`SELECT COALESCE(agent_id,''), substr(started_at,1,10) d, COALESCE(sum(cost_usd),0)
		FROM runs WHERE started_at >= datetime('now','-8 days') GROUP BY agent_id, d`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := map[string]map[string]float64{} // agent → day → cost
	for rows.Next() {
		var agent, day string
		var cost float64
		if err := rows.Scan(&agent, &day, &cost); err != nil {
			return nil, err
		}
		if history[agent] == nil {
			history[agent] = map[string]float64{}
		}
		history[agent][day] = cost
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var spiked []string
	for agent, days := range history {
		todayCost := days[today]
		if todayCost < spikeFloorUSD {
			continue
		}
		var prev []float64
		for d, c := range days {
			if d != today {
				prev = append(prev, c)
			}
		}
		med := median(prev)
		if med <= 0 || todayCost < spikeFactor*med {
			continue
		}
		dedup := fmt.Sprintf("cost_spike:%s:%s", agent, today)
		if st.Setting(dedup) != "" {
			continue
		}
		_ = st.SetSetting(dedup, store.Now())
		detail := fmt.Sprintf("agent %s spent $%.2f today — %.1f× its 7-day median $%.2f", agent, todayCost, todayCost/med, med)
		_, _ = st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
			VALUES(NULL, ?, 'cost_spike', ?, 0, ?)`, store.Now(), agent, detail)
		spiked = append(spiked, agent)
	}
	sort.Strings(spiked)
	return spiked, nil
}

// SpikeContributors explains a day's cost: top runs by spend for that date.
func SpikeContributors(st *store.Store, date, agentID string) ([]RunCost, error) {
	q := `SELECT id, COALESCE(agent_id,''), cost_usd FROM runs
		WHERE substr(started_at,1,10) = ?`
	args := []any{date}
	if agentID != "" {
		q += ` AND agent_id = ?`
		args = append(args, agentID)
	}
	q += ` ORDER BY cost_usd DESC LIMIT 20`
	rows, err := st.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunCost
	for rows.Next() {
		var r RunCost
		if err := rows.Scan(&r.RunID, &r.AgentID, &r.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type RunCost struct {
	RunID   string
	AgentID string
	CostUSD float64
}

func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	mid := len(vals) / 2
	if len(vals)%2 == 1 {
		return vals[mid]
	}
	return (vals[mid-1] + vals[mid]) / 2
}
