package learn

import (
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedShadowRun inserts a done run with a task_key linking to a work item,
// mirroring the runs columns PostRunActivity reads (F3i/F3ii fields:
// ended_at drives the comparison, task_key drives the join).
func seedShadowRun(t *testing.T, st *store.Store, id, project, taskKey, endedAt string) {
	t.Helper()
	_, err := st.DB.Exec(`INSERT INTO runs
		(id, session_id, project, status, started_at, ended_at, task_key)
		VALUES(?,?,?,'done',datetime('now','-1 hour'),?,?)`,
		id, id, project, endedAt, taskKey)
	if err != nil {
		t.Fatal(err)
	}
}

// seedShadowWorkItem inserts a work_items row with explicit is_agent and
// updated_at — the fields the F3i/F3ii/F14 logic branches on.
func seedShadowWorkItem(t *testing.T, st *store.Store, key, assignee, status, updatedAt string, isAgent int) {
	t.Helper()
	_, err := st.DB.Exec(`INSERT INTO work_items(source, key, title, status, assignee, is_agent, updated_at)
		VALUES('jira', ?, ?, ?, ?, ?, ?)`,
		key, key, status, assignee, isAgent, updatedAt)
	if err != nil {
		t.Fatal(err)
	}
}

// TestPostRunActivityZFormatCountedAfterEnd verifies the base case: a human
// update strictly after run end, both timestamps in RFC3339 "Z" form, counts
// as post-run activity.
func TestPostRunActivityZFormatCountedAfterEnd(t *testing.T) {
	st := insightTestStore(t)
	seedShadowRun(t, st, "r1", "proj-a", "SCRUM-1", "2026-06-20T10:00:00Z")
	seedShadowWorkItem(t, st, "SCRUM-1", "alice", "In Review", "2026-06-20T12:00:00Z", 0)

	res, err := PostRunActivity(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (Z-format post-run update should count)", len(res.Rows))
	}
	if res.Rows[0].LinkedAssignee != "alice" {
		t.Errorf("LinkedAssignee = %q, want alice", res.Rows[0].LinkedAssignee)
	}
	if len(res.PerProject) != 1 || res.PerProject[0].ActivityRuns != 1 || res.PerProject[0].DoneRuns != 1 {
		t.Errorf("PerProject = %+v, want 1 project with DoneRuns=1 ActivityRuns=1", res.PerProject)
	}
}

// TestPostRunActivityOffsetFormatStillCounted is F3i: SQLite's julianday()
// cannot parse a "+0700" (no-colon) offset and would silently drop this row
// if timestamps were compared in SQL. parseFlexTS must handle this layout so
// the row is still counted.
func TestPostRunActivityOffsetFormatStillCounted(t *testing.T) {
	st := insightTestStore(t)
	seedShadowRun(t, st, "r1", "proj-a", "SCRUM-1", "2026-06-20T10:00:00Z")
	seedShadowWorkItem(t, st, "SCRUM-1", "alice", "In Review", "2026-06-21T22:51:17.581+0700", 0)

	res, err := PostRunActivity(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (+0700-format must parse, not silently drop — F3i)", len(res.Rows))
	}
}

// TestPostRunActivityUpdatedBeforeEndNotCounted: an update timestamped before
// run end is not post-run activity.
func TestPostRunActivityUpdatedBeforeEndNotCounted(t *testing.T) {
	st := insightTestStore(t)
	seedShadowRun(t, st, "r1", "proj-a", "SCRUM-1", "2026-06-20T10:00:00Z")
	seedShadowWorkItem(t, st, "SCRUM-1", "alice", "In Review", "2026-06-20T09:00:00Z", 0)

	res, err := PostRunActivity(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("rows = %d, want 0 (update before run end)", len(res.Rows))
	}
	if res.PerProject[0].ActivityRuns != 0 || res.PerProject[0].DoneRuns != 1 {
		t.Errorf("PerProject = %+v, want DoneRuns=1 ActivityRuns=0", res.PerProject[0])
	}
}

// TestPostRunActivityIsAgentExcluded: the join requires is_agent=0 — an
// agent-attributed work item update is not human post-run activity.
func TestPostRunActivityIsAgentExcluded(t *testing.T) {
	st := insightTestStore(t)
	seedShadowRun(t, st, "r1", "proj-a", "SCRUM-1", "2026-06-20T10:00:00Z")
	seedShadowWorkItem(t, st, "SCRUM-1", "agent-bot", "In Review", "2026-06-20T12:00:00Z", 1)

	res, err := PostRunActivity(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("rows = %d, want 0 (is_agent=1 must not join)", len(res.Rows))
	}
	if len(res.PerProject) != 0 {
		t.Errorf("PerProject = %+v, want empty (no human-joined done run)", res.PerProject)
	}
}

// TestPostRunActivityDoneTransitionOnlyExcluded is F3ii: an item whose
// current status is Done (the only observable evidence of a post-run touch
// being the PO's own done-transition) must be excluded — otherwise every
// correctly finished task would false-flag as shadow work.
func TestPostRunActivityDoneTransitionOnlyExcluded(t *testing.T) {
	st := insightTestStore(t)
	seedShadowRun(t, st, "r1", "proj-a", "SCRUM-1", "2026-06-20T10:00:00Z")
	seedShadowWorkItem(t, st, "SCRUM-1", "alice", "Done", "2026-06-20T12:00:00Z", 0)

	res, err := PostRunActivity(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("rows = %d, want 0 (done-transition-only excluded — F3ii)", len(res.Rows))
	}
	// DoneRuns still counts the run (coverage/denominator unaffected).
	if len(res.PerProject) != 1 || res.PerProject[0].DoneRuns != 1 {
		t.Errorf("PerProject = %+v, want DoneRuns=1", res.PerProject)
	}
}

// TestPostRunActivityLowCoverageFlag is F3iii: with fewer than
// MinSampleForInsight done runs carrying a linkable task_key, coverage must
// flag Insufficient so callers do not render a rate as a conclusion.
func TestPostRunActivityLowCoverageFlag(t *testing.T) {
	st := insightTestStore(t)
	seedShadowRun(t, st, "r1", "proj-a", "SCRUM-1", "2026-06-20T10:00:00Z")
	seedShadowWorkItem(t, st, "SCRUM-1", "alice", "In Review", "2026-06-20T12:00:00Z", 0)
	// Plenty of done runs with NO task_key — these count toward RunsTotal but
	// not RunsWithTaskKey, so coverage stays below MinSampleForInsight.
	for i := 0; i < 5; i++ {
		_, err := st.DB.Exec(`INSERT INTO runs(id, session_id, project, status, started_at, ended_at)
			VALUES(?,?,?,'done',datetime('now','-1 hour'),datetime('now'))`,
			"nokey"+string(rune('a'+i)), "nokey"+string(rune('a'+i)), "proj-a")
		if err != nil {
			t.Fatal(err)
		}
	}

	res, err := PostRunActivity(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Coverage.RunsWithTaskKey != 1 {
		t.Errorf("RunsWithTaskKey = %d, want 1", res.Coverage.RunsWithTaskKey)
	}
	if res.Coverage.RunsTotal != 6 {
		t.Errorf("RunsTotal = %d, want 6", res.Coverage.RunsTotal)
	}
	if !res.Coverage.Insufficient() {
		t.Errorf("coverage n=1 should be Insufficient (< MinSampleForInsight=%d)", MinSampleForInsight)
	}
}

// TestPostRunActivityExcludesSyntheticFixtures ensures g2-verify*/gate-verify*
// runs never contribute to the analysis (fixture pollution guard).
func TestPostRunActivityExcludesSyntheticFixtures(t *testing.T) {
	st := insightTestStore(t)
	seedShadowRun(t, st, "g2-verify-1", "proj-a", "SCRUM-1", "2026-06-20T10:00:00Z")
	seedShadowWorkItem(t, st, "SCRUM-1", "alice", "In Review", "2026-06-20T12:00:00Z", 0)

	res, err := PostRunActivity(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Coverage.RunsTotal != 0 {
		t.Errorf("RunsTotal = %d, want 0 (synthetic fixture excluded)", res.Coverage.RunsTotal)
	}
	if len(res.Rows) != 0 {
		t.Errorf("rows = %d, want 0", len(res.Rows))
	}
}

func TestParseFlexTS(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"rfc3339-z", "2026-06-20T10:00:00Z", true},
		{"rfc3339nano", "2026-06-20T10:00:00.123456789Z", true},
		{"offset-no-colon", "2026-06-21T22:51:17.581+0700", true},
		{"garbage", "not-a-timestamp", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := parseFlexTS(c.in)
			if ok != c.ok {
				t.Errorf("parseFlexTS(%q) ok = %v, want %v", c.in, ok, c.ok)
			}
		})
	}
}
