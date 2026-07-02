package learn

import (
	"github.com/phuc-nt/dandori/internal/store"
)

// AttributionRow is one agent's code-volume contribution in the window.
type AttributionRow struct {
	AgentID      string
	LinesAdded   int64
	LinesDeleted int64
	Runs         int
	RevertedRuns int // runs with at least one revert_detected event
	RunIDs       []string
}

// Attribution aggregates per-run git deltas (recorded by hooks/wrap) by
// agent — who is actually producing the code, and how much of it gets
// reverted. Pure store read, full provenance via RunIDs.
func Attribution(st *store.Store, windowDays int) ([]AttributionRow, error) {
	rows, err := st.DB.Query(`SELECT COALESCE(agent_id,''), id, lines_added, lines_deleted,
			COALESCE((SELECT count(*) FROM events e WHERE e.run_id = runs.id AND e.kind = 'revert_detected'), 0)
		FROM runs WHERE started_at >= ` + windowClause(windowDays) + ` ORDER BY agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byAgent := map[string]*AttributionRow{}
	var order []string
	for rows.Next() {
		var agent, runID string
		var added, deleted int64
		var reverts int
		if err := rows.Scan(&agent, &runID, &added, &deleted, &reverts); err != nil {
			return nil, err
		}
		r, ok := byAgent[agent]
		if !ok {
			r = &AttributionRow{AgentID: agent}
			byAgent[agent] = r
			order = append(order, agent)
		}
		r.LinesAdded += added
		r.LinesDeleted += deleted
		r.Runs++
		r.RunIDs = append(r.RunIDs, runID)
		if reverts > 0 {
			r.RevertedRuns++
		}
	}
	out := make([]AttributionRow, 0, len(order))
	for _, a := range order {
		out = append(out, *byAgent[a])
	}
	return out, rows.Err()
}
