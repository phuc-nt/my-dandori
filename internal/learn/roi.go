package learn

import (
	"fmt"

	"github.com/phuc-nt/dandori/internal/store"
)

// ROI splits an agent's (or the fleet's, agentID="") spend into useful vs
// wasted, with per-bucket run ids so every dollar is traceable.
type ROI struct {
	TotalUSD      float64
	WastedUSD     float64
	UsefulPct     float64
	FailedUSD     float64 // runs that failed or were killed
	FlaggedUSD    float64 // done runs still carrying open flags
	RejectedUSD   float64 // acceptance-weighted share of clean done runs
	FailedRunIDs  []string
	FlaggedRunIDs []string
	Formula       string
}

// ComputeROI buckets are mutually exclusive: failed/killed first, then
// open-flagged, then the acceptance share of the remaining clean runs —
// a run's cost is never counted twice.
func ComputeROI(st *store.Store, agentID string, windowDays int, acceptancePct float64) (*ROI, error) {
	q := `SELECT id, status, cost_usd,
			COALESCE((SELECT count(*) FROM flags f WHERE f.run_id = runs.id AND f.status='open'), 0)
		FROM runs WHERE started_at >= ` + windowClause(windowDays)
	args := []any{}
	if agentID != "" {
		q += ` AND agent_id = ?`
		args = append(args, agentID)
	}
	rows, err := st.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	r := &ROI{}
	var cleanUSD float64
	for rows.Next() {
		var id, status string
		var cost float64
		var openFlags int
		if err := rows.Scan(&id, &status, &cost, &openFlags); err != nil {
			return nil, err
		}
		r.TotalUSD += cost
		switch {
		case status == "failed" || status == "killed":
			r.FailedUSD += cost
			r.FailedRunIDs = append(r.FailedRunIDs, id)
		case openFlags > 0:
			r.FlaggedUSD += cost
			r.FlaggedRunIDs = append(r.FlaggedRunIDs, id)
		default:
			cleanUSD += cost
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	r.RejectedUSD = cleanUSD * (1 - acceptancePct/100)
	r.WastedUSD = r.FailedUSD + r.FlaggedUSD + r.RejectedUSD
	r.UsefulPct = 100
	if r.TotalUSD > 0 {
		r.UsefulPct = 100 * (1 - r.WastedUSD/r.TotalUSD)
	}
	r.Formula = fmt.Sprintf(
		"wasted = failed/killed $%.2f + open-flagged $%.2f + (clean $%.2f × (1 − acceptance %.0f%%)) = $%.2f of $%.2f",
		r.FailedUSD, r.FlaggedUSD, cleanUSD, acceptancePct, r.WastedUSD, r.TotalUSD)
	return r, nil
}
