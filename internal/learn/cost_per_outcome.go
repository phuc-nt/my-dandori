package learn

import (
	"fmt"

	"github.com/phuc-nt/dandori/internal/store"
)

// OutcomeStat is descriptive cost-vs-outcome for one group (project or agent)
// over the window. Pure arithmetic — total cost over finished runs — with no
// weighting or calibration. This is deliberately NOT a grade: it answers "what
// did this cost per completed run", not "how good is this agent".
type OutcomeStat struct {
	Group       string // project name or agent id
	Runs        int    // all runs (any status)
	Done        int
	Failed      int
	Killed      int
	Running     int // excluded from cost-per-done denominator
	TotalCost   float64
	CostPerDone float64 // TotalCost / Done; 0 when Done==0
}

// Insufficient flags a group with too few runs to read into.
func (o OutcomeStat) Insufficient() bool { return o.Runs < MinSampleForInsight }

// CostPerOutcome groups runs by project or agent and computes cost per
// completed run. groupBy must be "project" or "agent"; anything else defaults
// to project. Running runs count toward Runs/TotalCost but not the done
// denominator — an in-flight run is not an outcome yet.
func CostPerOutcome(st *store.Store, days int, groupBy string) ([]OutcomeStat, error) {
	col := "COALESCE(NULLIF(project,''),'unknown')"
	if groupBy == "agent" {
		col = "COALESCE(NULLIF(agent_id,''),'unknown')"
	}
	rows, err := st.DB.Query(`
		SELECT ` + col + ` AS g,
		       count(*),
		       sum(CASE WHEN status='done'    THEN 1 ELSE 0 END),
		       sum(CASE WHEN status='failed'  THEN 1 ELSE 0 END),
		       sum(CASE WHEN status='killed'  THEN 1 ELSE 0 END),
		       sum(CASE WHEN status='running' THEN 1 ELSE 0 END),
		       COALESCE(sum(cost_usd),0)
		FROM runs
		WHERE 1=1` + insightWindowClause(days) + `
		GROUP BY g
		ORDER BY COALESCE(sum(cost_usd),0) DESC, g`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutcomeStat
	for rows.Next() {
		var o OutcomeStat
		if err := rows.Scan(&o.Group, &o.Runs, &o.Done, &o.Failed, &o.Killed, &o.Running, &o.TotalCost); err != nil {
			return nil, err
		}
		if o.Done > 0 {
			o.CostPerDone = o.TotalCost / float64(o.Done)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// FormatCostPerDone renders "$X / N done" so the UI shows numerator and
// denominator together — a reader sees a $2 cost-per-done over 1 run is not
// the same as over 50.
func (o OutcomeStat) FormatCostPerDone() string {
	if o.Done == 0 {
		return "— (chưa có run xong)"
	}
	return fmt.Sprintf("$%.2f / %d done", o.CostPerDone, o.Done)
}
