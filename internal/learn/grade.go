package learn

import (
	"sort"

	"github.com/phuc-nt/dandori/internal/store"
)

// Grade is a fleet-calibrated letter with its provenance.
type Grade struct {
	Letter       string
	Percentile   float64 // agent's percentile within the fleet composite distribution
	Uncalibrated bool    // true when fleet < minFleet → static bands used
	FleetSize    int
}

// minFleet is the minimum number of scored entities for percentile
// calibration to be meaningful; below it we fall back to static bands.
const minFleet = 5

// GradeFor calibrates one composite against the fleet distribution:
// A ≥ p80, B ≥ p60, C ≥ p40, D ≥ p20, F below. No hand-tuned thresholds.
func GradeFor(composite float64, fleet []float64) Grade {
	g := Grade{FleetSize: len(fleet)}
	if len(fleet) < minFleet {
		g.Uncalibrated = true
		g.Letter = staticBand(composite)
		return g
	}
	sorted := append([]float64(nil), fleet...)
	sort.Float64s(sorted)
	below := 0
	for _, v := range sorted {
		if v < composite {
			below++
		}
	}
	g.Percentile = 100 * float64(below) / float64(len(sorted))
	switch {
	case g.Percentile >= 80:
		g.Letter = "A"
	case g.Percentile >= 60:
		g.Letter = "B"
	case g.Percentile >= 40:
		g.Letter = "C"
	case g.Percentile >= 20:
		g.Letter = "D"
	default:
		g.Letter = "F"
	}
	return g
}

func staticBand(v float64) string {
	switch {
	case v >= 90:
		return "A"
	case v >= 80:
		return "B"
	case v >= 70:
		return "C"
	case v >= 60:
		return "D"
	}
	return "F"
}

// activeAgents lists agents with runs in the window (calibration population).
func activeAgents(st *store.Store, windowDays int) ([]string, error) {
	rows, err := st.DB.Query(`SELECT DISTINCT agent_id FROM runs
		WHERE agent_id IS NOT NULL AND started_at >= ` + windowClause(windowDays) + ` ORDER BY agent_id`)
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

// FleetComposites computes composite scores for every active agent — the
// calibration population (used by the agent detail page; the leaderboard
// computes the same thing in a single pass instead).
func FleetComposites(st *store.Store, windowDays int) (map[string]float64, error) {
	ids, err := activeAgents(st, windowDays)
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, id := range ids {
		m, err := Compute(st, id, windowDays)
		if err != nil {
			return nil, err
		}
		out[id] = m.Composite
	}
	return out, nil
}
