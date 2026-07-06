package learn

import (
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

func impactStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// seedApproval inserts a run + an approved approval of the given action, with a
// cost and a number of Edit file-touch events.
func seedApproval(t *testing.T, st *store.Store, agent, runID, action string, cost float64, files int) {
	t.Helper()
	st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?, ?, ?) ON CONFLICT(name) DO NOTHING`, agent, agent, store.Now())
	if _, err := st.DB.Exec(`INSERT INTO runs(id, agent_id, status, started_at, cost_usd) VALUES(?, ?, 'done', ?, ?)`,
		runID, agent, store.Now(), cost); err != nil {
		t.Fatal(err)
	}
	st.DB.Exec(`INSERT INTO approvals(run_id, action, status, requested_at) VALUES(?, ?, 'approved', ?)`, runID, action, store.Now())
	for i := 0; i < files; i++ {
		st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok) VALUES(?, ?, 'tool_use', 'Edit', 1)`, runID, store.Now())
	}
}

func TestEstimateImpactExcludesSyntheticActions(t *testing.T) {
	st := impactStore(t)
	for _, a := range []string{"band-demote:x:supervised", "observer:context-import:1"} {
		if _, ok := EstimateImpact(st, "agent", a); ok {
			t.Errorf("synthetic action %q produced an estimate", a)
		}
	}
}

func TestEstimateImpactNeedsThreeSamples(t *testing.T) {
	st := impactStore(t)
	// Two-sample agent → no estimate.
	seedApproval(t, st, "few", "f1", "Edit /main.go", 1.0, 2)
	seedApproval(t, st, "few", "f2", "Edit /util.go", 3.0, 4)
	if _, ok := EstimateImpact(st, "few", "Edit /new.go"); ok {
		t.Error("estimate returned with < 3 samples")
	}

	// A separate three-sample agent → estimate appears with correct averages.
	// (Distinct agent avoids the hour-bucketed cache masking the change, which
	// is the correct production behavior: an agent's history is stable within
	// an hour.)
	seedApproval(t, st, "a1", "r1", "Edit /main.go", 1.0, 2)
	seedApproval(t, st, "a1", "r2", "Edit /util.go", 3.0, 4)
	seedApproval(t, st, "a1", "r3", "Edit /third.go", 5.0, 6)
	im, ok := EstimateImpact(st, "a1", "Edit /new.go")
	if !ok {
		t.Fatal("no estimate with 3 samples")
	}
	if im.Samples != 3 {
		t.Errorf("samples = %d, want 3", im.Samples)
	}
	if im.AvgCost != 3.0 { // (1+3+5)/3
		t.Errorf("avg cost = %v, want 3.0", im.AvgCost)
	}
	if im.AvgFiles != 4.0 { // (2+4+6)/3
		t.Errorf("avg files = %v, want 4.0", im.AvgFiles)
	}
}

func TestEstimateImpactSeparatesActionTypes(t *testing.T) {
	st := impactStore(t)
	// 3 Edit approvals and 1 Bash approval; a Bash query must not borrow Edit
	// samples.
	seedApproval(t, st, "a1", "r1", "Edit /a", 1, 1)
	seedApproval(t, st, "a1", "r2", "Edit /b", 1, 1)
	seedApproval(t, st, "a1", "r3", "Edit /c", 1, 1)
	seedApproval(t, st, "a1", "r4", "git push origin main", 9, 0)
	if _, ok := EstimateImpact(st, "a1", "git status"); ok {
		t.Error("git action estimated from a single sample (should need 3 of its own type)")
	}
	if _, ok := EstimateImpact(st, "a1", "Edit /new"); !ok {
		t.Error("Edit action should have 3 samples")
	}
}
