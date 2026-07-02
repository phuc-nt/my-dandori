package learn

import (
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// seedFleet: a1 perfect, a2 mixed, a3 poor — hand-checkable numbers.
func seedFleet(t *testing.T, st *store.Store) {
	for _, a := range []string{"a1", "a2", "a3"} {
		testseed.Agent(t, st, a)
	}
	// a1: 2 clean done runs, 2 accepted edits, no interventions.
	testseed.Run(t, st, "a1-r1", "a1", "done", 2, 1.0)
	testseed.Run(t, st, "a1-r2", "a1", "done", 3, 1.0)
	testseed.Event(t, st, "a1-r1", "tool_use", "Edit", -1, "")
	testseed.Event(t, st, "a1-r1", "tool_result", "Edit", 1, "")
	testseed.Event(t, st, "a1-r2", "tool_use", "Write", -1, "")
	testseed.Event(t, st, "a1-r2", "tool_result", "Write", 1, "")

	// a2: 1 done + 1 failed; 2 edits 1 rejected; 1 permission ask.
	testseed.Run(t, st, "a2-r1", "a2", "done", 2, 2.0)
	testseed.Run(t, st, "a2-r2", "a2", "failed", 4, 3.0)
	testseed.Event(t, st, "a2-r1", "tool_use", "Edit", -1, "")
	testseed.Event(t, st, "a2-r1", "tool_result", "Edit", 0, "")
	testseed.Event(t, st, "a2-r1", "tool_use", "Edit", -1, "")
	testseed.Event(t, st, "a2-r1", "tool_result", "Edit", 1, "")
	testseed.Event(t, st, "a2-r1", "permission_ask", "Bash", 0, "")

	// a3: killed run + flagged done run + Jira task not done.
	testseed.Run(t, st, "a3-r1", "a3", "killed", 1, 4.0)
	testseed.Run(t, st, "a3-r2", "a3", "done", 2, 2.0)
	testseed.Flag(t, st, "a3-r2", "quality gate failed")
	st.DB.Exec(`UPDATE runs SET task_key='SCRUM-9' WHERE id='a3-r2'`)
	testseed.WorkItem(t, st, "jira", "SCRUM-9", "In Progress")
}

func TestComputeHandChecked(t *testing.T) {
	st := testStore(t)
	seedFleet(t, st)

	a1, err := Compute(st, "a1", 30)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Acceptance.Value != 100 || a1.Success.Value != 100 ||
		a1.Autonomy.Value != 100 || a1.Reliability.Value != 100 {
		t.Errorf("a1 must be perfect: %+v", a1)
	}
	if a1.Composite != 100 {
		t.Errorf("a1 composite: %f", a1.Composite)
	}

	a2, _ := Compute(st, "a2", 30)
	if a2.Acceptance.Value != 50 { // 1 rejected / 2 edits
		t.Errorf("a2 acceptance: %f (%s)", a2.Acceptance.Value, a2.Acceptance.Formula)
	}
	if a2.Success.Value != 50 { // 1 done / 2 ended
		t.Errorf("a2 success: %f", a2.Success.Value)
	}
	if a2.Autonomy.Value != 50 { // 1 of 2 runs intervened (permission ask)
		t.Errorf("a2 autonomy: %f", a2.Autonomy.Value)
	}

	a3, _ := Compute(st, "a3", 30)
	if a3.Success.Value != 0 { // killed + jira-not-done
		t.Errorf("a3 success: %f (%s)", a3.Success.Value, a3.Success.Formula)
	}
	// kill rate 1/2 → reliability = 100*(1-(0+0+0.5)/3)
	if want := 100 * (1 - 0.5/3); abs(a3.Reliability.Value-want) > 0.01 {
		t.Errorf("a3 reliability: %f want %f", a3.Reliability.Value, want)
	}

	// Determinism.
	again, _ := Compute(st, "a2", 30)
	if again.Composite != a2.Composite {
		t.Error("Compute must be deterministic")
	}
}

func TestProvenanceNonEmpty(t *testing.T) {
	st := testStore(t)
	seedFleet(t, st)
	metric, rows, err := Provenance(st, "a2", "acceptance", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(metric.EventIDs) == 0 || len(rows) == 0 {
		t.Errorf("provenance must expose source rows: ids=%d rows=%d", len(metric.EventIDs), len(rows))
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
