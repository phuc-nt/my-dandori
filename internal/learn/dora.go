package learn

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// DORALite is the honest subset of DORA computable from synced data.
// Deploy frequency has no data source yet and is reported as such — no
// invented numbers.
type DORALite struct {
	LeadTimeP50     time.Duration
	LeadTimeCount   int
	LeadTimeFormula string
	CFR             Metric
	PRCycle         *PRCycleResult
	DeployFreqNote  string
}

// ComputeDORALite assembles the panel from Jira lead time + GitHub CFR and
// PR cycle. Every component carries its own formula/provenance.
func ComputeDORALite(st *store.Store, windowDays int) (*DORALite, error) {
	d := &DORALite{DeployFreqNote: "n/a — no deploy events captured (honest gap, not zero)"}
	var err error
	if d.CFR, err = CFR(st, windowDays); err != nil {
		return nil, err
	}
	if d.PRCycle, err = PRCycle(st, windowDays); err != nil {
		return nil, err
	}
	if err := d.leadTime(st, windowDays); err != nil {
		return nil, err
	}
	return d, nil
}

// leadTime: Jira issue created → last update while in a done status (proxy
// for resolved date — stated in the formula).
func (d *DORALite) leadTime(st *store.Store, windowDays int) error {
	rows, err := st.DB.Query(`SELECT COALESCE(payload,''), COALESCE(status,'')
		FROM work_items WHERE source = 'jira' AND updated_at >= ` + windowClause(windowDays))
	if err != nil {
		return err
	}
	defer rows.Close()
	var durations []time.Duration
	for rows.Next() {
		var payload, status string
		if err := rows.Scan(&payload, &status); err != nil {
			return err
		}
		if !isDoneStatus(status) {
			continue
		}
		var p struct{ Created, Updated string }
		if json.Unmarshal([]byte(payload), &p) != nil || p.Created == "" || p.Updated == "" {
			continue
		}
		c, err1 := parseJiraTime(p.Created)
		u, err2 := parseJiraTime(p.Updated)
		if err1 != nil || err2 != nil || u.Before(c) {
			continue
		}
		durations = append(durations, u.Sub(c))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	d.LeadTimeCount = len(durations)
	if len(durations) == 0 {
		d.LeadTimeFormula = "no done Jira issues with timestamps in window"
		return nil
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	d.LeadTimeP50 = percentileDur(durations, 50)
	d.LeadTimeFormula = fmt.Sprintf("p50 of created→last-update over %d done issues (update date is a proxy for resolution)", len(durations))
	return nil
}

// parseJiraTime handles Jira's RFC3339-with-milliseconds format.
func parseJiraTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000-0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable time %q", s)
}
