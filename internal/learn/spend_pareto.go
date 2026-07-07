package learn

import (
	"github.com/phuc-nt/dandori/internal/store"
)

// paretoTopN is how many highest-cost runs the drill-down table shows.
const paretoTopN = 10

// ParetoTier buckets runs by cumulative cost share: A = runs that together
// account for the first 80% of spend, B = next slice up to 95%, C = the long
// tail. Mirrors the classic 80/20 framing but reported against the fleet's
// actual data rather than assumed.
type ParetoTier struct {
	Name            string // "A", "B", or "C"
	RunCount        int
	RunPct, CostPct float64
}

// ParetoRun is one row in the top-N cost drill-down.
type ParetoRun struct {
	RunID, Agent, Project string
	Cost                  float64
	CumCostPct            float64
}

// ParetoResult is the spend concentration analysis over the window.
type ParetoResult struct {
	TotalCost       float64
	Runs            int
	ExcludedRunning int     // runs still 'running', not counted (F11)
	ExcludedCost    float64 // their cost, so the reader sees what's missing
	Tiers           []ParetoTier
	Top             []ParetoRun
}

// SpendPareto ranks finished runs by cost (desc), computes a Go-side
// cumulative sum, and buckets them into A/B/C tiers by cumulative cost share
// (A <=80%, B <=95%, C rest). Running runs are excluded (F11): their cost is
// still accruing, so including them would make the cumulative denominator and
// the top-N list jump between refreshes independent of any completed work.
// ExcludedRunning/ExcludedCost report what was left out so the analysis isn't
// silently partial.
func SpendPareto(st *store.Store, days int) (ParetoResult, error) {
	var res ParetoResult

	if err := st.DB.QueryRow(`
		SELECT count(*), COALESCE(sum(cost_usd),0) FROM runs
		WHERE status='running' AND cost_usd>0`+insightWindowClauseCol("started_at", days)).
		Scan(&res.ExcludedRunning, &res.ExcludedCost); err != nil {
		return res, err
	}

	rows, err := st.DB.Query(`
		SELECT id, COALESCE(agent_id,''), COALESCE(project,''), cost_usd
		FROM runs
		WHERE cost_usd>0 AND status<>'running'` + insightWindowClauseCol("started_at", days) + `
		ORDER BY cost_usd DESC`)
	if err != nil {
		return res, err
	}
	defer rows.Close()

	type row struct {
		id, agent, project string
		cost               float64
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.agent, &r.project, &r.cost); err != nil {
			return res, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return res, err
	}

	res.Runs = len(all)
	for _, r := range all {
		res.TotalCost += r.cost
	}
	if res.TotalCost <= 0 {
		return res, nil
	}

	tierCount := map[string]int{}
	tierCost := map[string]float64{}
	cum := 0.0
	for i, r := range all {
		cum += r.cost
		cumPct := cum / res.TotalCost * 100
		tier := "C"
		switch {
		case cumPct <= 80:
			tier = "A"
		case cumPct <= 95:
			tier = "B"
		}
		tierCount[tier]++
		tierCost[tier] += r.cost
		if i < paretoTopN {
			res.Top = append(res.Top, ParetoRun{
				RunID:      r.id,
				Agent:      r.agent,
				Project:    r.project,
				Cost:       r.cost,
				CumCostPct: cumPct,
			})
		}
	}

	for _, name := range []string{"A", "B", "C"} {
		n := tierCount[name]
		if n == 0 {
			continue
		}
		res.Tiers = append(res.Tiers, ParetoTier{
			Name:     name,
			RunCount: n,
			RunPct:   float64(n) / float64(res.Runs) * 100,
			CostPct:  tierCost[name] / res.TotalCost * 100,
		})
	}

	return res, nil
}
