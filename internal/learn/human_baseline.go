package learn

import (
	"github.com/phuc-nt/dandori/internal/store"
)

// HumanBaseline returns anonymous composite proxies for humans in the same
// organization, computed from Jira work items — the vision's "phân vị 85,
// kể cả của người". Only the success dimension is honestly comparable, so
// the proxy is 100 × done/total per assignee (≥ minItems items). No names
// are stored or returned — this is a calibration baseline, not a person
// scoreboard.
const minHumanItems = 3

func HumanBaseline(st *store.Store, windowDays int) ([]float64, error) {
	rows, err := st.DB.Query(`SELECT COALESCE(assignee,''), status FROM work_items
		WHERE source = 'jira' AND assignee != '' AND is_agent = 0
		AND updated_at >= ` + windowClause(windowDays))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type tally struct{ done, total int }
	byPerson := map[string]*tally{}
	for rows.Next() {
		var assignee, status string
		if err := rows.Scan(&assignee, &status); err != nil {
			return nil, err
		}
		t, ok := byPerson[assignee]
		if !ok {
			t = &tally{}
			byPerson[assignee] = t
		}
		t.total++
		if isDoneStatus(status) {
			t.done++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []float64
	for _, t := range byPerson {
		if t.total < minHumanItems {
			continue
		}
		out = append(out, 100*float64(t.done)/float64(t.total))
	}
	return out, nil
}
