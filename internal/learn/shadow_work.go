package learn

import (
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// CoverageStat reports how many done runs actually carry a task_key linking
// them to a work_items row. Post-run activity is only meaningful over the
// runs that have a linkable key — a rate computed on a near-empty coverage
// set reads like a clean bill when it is really "we have no data" (F3iii).
type CoverageStat struct {
	RunsTotal       int // all done runs in the window (synthetic fixtures excluded)
	RunsWithTaskKey int // of those, how many joined a work_items row
}

// Insufficient reports whether coverage is too thin to show a rate as a
// conclusion (F3iii) — mirrors the context-ROI low-coverage empty-state.
func (c CoverageStat) Insufficient() bool { return c.RunsWithTaskKey < MinSampleForInsight }

// ActivityRow is one drillable instance of post-run human activity on a task
// the agent reported done. LinkedAssignee is shown for RBAC drill context but
// is deliberately NOT aggregated per person anywhere in this package (F14) —
// this is a signal about task handoff, not a person scorecard.
type ActivityRow struct {
	RunID          string
	TaskKey        string
	Project        string
	RunEndedAt     string
	LinkedAssignee string
	ItemUpdatedAt  string
	ItemStatus     string
}

// ProjectActivity aggregates post-run human activity per project: of the done
// runs with a linkable task_key, how many saw further human (non-agent)
// activity on that same work item after the run ended.
type ProjectActivity struct {
	Project      string
	DoneRuns     int
	ActivityRuns int
	ActivityRate float64 // ActivityRuns / DoneRuns; 0 when DoneRuns==0
}

// ActivityResult is the full post-run-activity analysis: coverage first (so
// callers can gate on it), then the per-project breakdown, then raw drill
// rows.
type ActivityResult struct {
	Coverage   CoverageStat
	PerProject []ProjectActivity
	Rows       []ActivityRow
}

// rawActivityJoin is what the SQL query returns before any Go-side timestamp
// parsing or done-transition filtering — both jobs must happen in Go (F3i,
// F3ii), never in SQL.
type rawActivityJoin struct {
	Project       string
	RunID         string
	TaskKey       string
	RunEndedAt    string
	Assignee      string
	ItemUpdatedAt string
	ItemStatus    string
}

// flexTSLayouts are the timestamp layouts observed in this schema. Different
// writers (hook hydration vs. Jira sync) produce different offset styles —
// notably "+0700" (no colon in the offset), which SQLite's julianday() cannot
// parse (returns NULL, silently dropping the row — F3i verified live). We
// parse in Go instead of comparing in SQL so no layout silently fails closed.
var flexTSLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05.000-0700",
}

// parseFlexTS parses a timestamp trying each known layout in turn. Returns
// ok=false if none match (caller treats as "cannot compare, skip" rather than
// panicking or silently zero-valuing).
func parseFlexTS(s string) (t time.Time, ok bool) {
	for _, layout := range flexTSLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// PostRunActivity finds work items that a done agent run reported finished,
// but which a human (is_agent=0) subsequently updated. This is a handoff
// signal (finish vs. review are NOT distinguished in v11 — Q7 tracked for
// v12), not proof of incomplete work: the label must say so.
func PostRunActivity(st *store.Store, days int) (ActivityResult, error) {
	raws, err := queryActivityJoin(st, days)
	if err != nil {
		return ActivityResult{}, err
	}

	coverage, err := activityCoverage(st, days)
	if err != nil {
		return ActivityResult{}, err
	}

	perProject := map[string]*ProjectActivity{}
	var rows []ActivityRow

	for _, r := range raws {
		pa, exists := perProject[r.Project]
		if !exists {
			pa = &ProjectActivity{Project: r.Project}
			perProject[r.Project] = pa
		}
		pa.DoneRuns++

		endedAt, endOK := parseFlexTS(r.RunEndedAt)
		updatedAt, updOK := parseFlexTS(r.ItemUpdatedAt)
		if !endOK || !updOK {
			continue // cannot compare safely — skip rather than guess (F3i)
		}
		if !updatedAt.After(endedAt) {
			continue // updated before/at run end — no post-run activity
		}
		if isDoneTransitionOnly(r.ItemStatus) {
			// F3ii: updated_at is item-level last-update; the PO's own
			// Done-transition (the event Success reads) always lands after
			// run-end. Without a changelog we cannot separate "did more
			// work" from "clicked Done"/"reviewed" — so an item whose only
			// post-run evidence is landing on status=Done is excluded
			// rather than false-flagged as shadow work.
			continue
		}

		pa.ActivityRuns++
		rows = append(rows, ActivityRow{
			RunID:          r.RunID,
			TaskKey:        r.TaskKey,
			Project:        r.Project,
			RunEndedAt:     r.RunEndedAt,
			LinkedAssignee: r.Assignee,
			ItemUpdatedAt:  r.ItemUpdatedAt,
			ItemStatus:     r.ItemStatus,
		})
	}

	out := make([]ProjectActivity, 0, len(perProject))
	for _, pa := range perProject {
		if pa.DoneRuns > 0 {
			pa.ActivityRate = float64(pa.ActivityRuns) / float64(pa.DoneRuns)
		}
		out = append(out, *pa)
	}

	return ActivityResult{Coverage: coverage, PerProject: out, Rows: rows}, nil
}

// isDoneTransitionOnly reports whether the item's current status IS the
// terminal Done state — the schema has no changelog, so "status is Done" is
// the only observable proxy for "the only post-run update was the
// done-transition" (F3ii). This is intentionally conservative: it also
// excludes items a human genuinely re-closed after touching them, but that
// tradeoff is the honest one available without changelog data.
func isDoneTransitionOnly(status string) bool {
	return status == "Done"
}

func queryActivityJoin(st *store.Store, days int) ([]rawActivityJoin, error) {
	rows, err := st.DB.Query(`
		SELECT COALESCE(NULLIF(r.project,''),'unknown'), r.id, r.task_key, r.ended_at,
		       COALESCE(w.assignee,''), w.updated_at, COALESCE(w.status,'')
		FROM runs r
		JOIN work_items w ON w.key = r.task_key AND w.is_agent = 0
		WHERE r.status='done' AND r.task_key IS NOT NULL AND r.task_key<>''
		  AND r.ended_at IS NOT NULL
		  AND r.id NOT LIKE 'g2-verify%' AND r.id NOT LIKE 'gate-verify%'
		` + insightWindowClauseCol("r.started_at", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []rawActivityJoin
	for rows.Next() {
		var r rawActivityJoin
		if err := rows.Scan(&r.Project, &r.RunID, &r.TaskKey, &r.RunEndedAt, &r.Assignee, &r.ItemUpdatedAt, &r.ItemStatus); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// activityCoverage measures, over all done runs in the window (synthetic
// fixtures excluded), how many carry a task_key that actually joins a
// work_items row — the denominator PostRunActivity's rate is meaningful over
// (F3iii).
func activityCoverage(st *store.Store, days int) (CoverageStat, error) {
	var c CoverageStat
	err := st.DB.QueryRow(`
		SELECT count(*),
		       COALESCE(sum(CASE WHEN task_key IS NOT NULL AND task_key<>'' AND EXISTS(
		             SELECT 1 FROM work_items w WHERE w.key = runs.task_key AND w.is_agent = 0
		           ) THEN 1 ELSE 0 END),0)
		FROM runs
		WHERE status='done'
		  AND id NOT LIKE 'g2-verify%' AND id NOT LIKE 'gate-verify%'
		`+insightWindowClauseCol("started_at", days)).Scan(&c.RunsTotal, &c.RunsWithTaskKey)
	if err != nil {
		return CoverageStat{}, err
	}
	return c, nil
}
