package learn

import (
	"fmt"

	"github.com/phuc-nt/dandori/internal/store"
)

// ProvenanceRow is one raw record behind a metric value.
type ProvenanceRow struct {
	Kind    string // run | event
	ID      string
	TS      string
	Summary string
}

// Provenance resolves a metric back to the raw rows that produced it —
// the "where did this number come from" answer, straight from the store.
func Provenance(st *store.Store, agentID, metricName string, windowDays int) (Metric, []ProvenanceRow, error) {
	m, err := Compute(st, agentID, windowDays)
	if err != nil {
		return Metric{}, nil, err
	}
	var metric Metric
	switch metricName {
	case "acceptance":
		metric = m.Acceptance
	case "success":
		metric = m.Success
	case "autonomy":
		metric = m.Autonomy
	case "reliability":
		metric = m.Reliability
	default:
		return Metric{}, nil, fmt.Errorf("unknown metric %q", metricName)
	}
	rows := make([]ProvenanceRow, 0, len(metric.RunIDs)+len(metric.EventIDs))
	for _, id := range metric.RunIDs {
		var status, ts string
		var cost float64
		if err := st.DB.QueryRow(`SELECT status, COALESCE(started_at,''), cost_usd FROM runs WHERE id = ?`, id).
			Scan(&status, &ts, &cost); err != nil {
			continue
		}
		rows = append(rows, ProvenanceRow{Kind: "run", ID: id, TS: ts,
			Summary: fmt.Sprintf("status=%s cost=$%.4f", status, cost)})
	}
	for _, id := range metric.EventIDs {
		var kind, tool, ts string
		var runID string
		if err := st.DB.QueryRow(`SELECT kind, COALESCE(tool_name,''), ts, COALESCE(run_id,'') FROM events WHERE id = ?`, id).
			Scan(&kind, &tool, &ts, &runID); err != nil {
			continue
		}
		rows = append(rows, ProvenanceRow{Kind: "event", ID: fmt.Sprint(id), TS: ts,
			Summary: fmt.Sprintf("%s %s (run %s)", kind, tool, runID)})
	}
	return metric, rows, nil
}
