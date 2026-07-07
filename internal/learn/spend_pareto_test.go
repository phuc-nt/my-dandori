package learn

import (
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedParetoRun inserts a finished (or running) run with a fixed cost, for
// Pareto tier/cumsum tests where duration doesn't matter.
func seedParetoRun(t *testing.T, st *store.Store, id, agent, project, status string, cost float64) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?,?,datetime('now'))
		ON CONFLICT(id) DO NOTHING`, agent, agent); err != nil {
		t.Fatal(err)
	}
	ended := "datetime('now')"
	if status == "running" {
		ended = "NULL"
	}
	_, err := st.DB.Exec(`INSERT INTO runs
		(id, session_id, agent_id, project, status, started_at, ended_at, cost_usd)
		VALUES(?,?,?,?,?, datetime('now'), `+ended+`, ?)`,
		id, id, agent, project, status, cost)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSpendParetoTiersAndCumPct(t *testing.T) {
	st := insightTestStore(t)
	// costs [100,50,30,20] desc, total=200.
	// cum: 100(50%)->A, 150(75%)->A, 180(90%)->B, 200(100%)->C
	seedParetoRun(t, st, "r100", "a", "p", "done", 100)
	seedParetoRun(t, st, "r50", "a", "p", "done", 50)
	seedParetoRun(t, st, "r30", "a", "p", "done", 30)
	seedParetoRun(t, st, "r20", "a", "p", "done", 20)

	res, err := SpendPareto(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCost != 200 || res.Runs != 4 {
		t.Fatalf("res = %+v, want TotalCost=200 Runs=4", res)
	}
	if len(res.Top) != 4 {
		t.Fatalf("want 4 top rows (n<10), got %d", len(res.Top))
	}
	// Cost-descending order preserved.
	wantCosts := []float64{100, 50, 30, 20}
	for i, c := range wantCosts {
		if res.Top[i].Cost != c {
			t.Errorf("top[%d].Cost = %v, want %v", i, res.Top[i].Cost, c)
		}
	}
	// cumCostPct monotonic increasing to 100.
	prev := 0.0
	for i, r := range res.Top {
		if r.CumCostPct < prev {
			t.Errorf("top[%d].CumCostPct=%v not monotonic (prev=%v)", i, r.CumCostPct, prev)
		}
		prev = r.CumCostPct
	}
	if last := res.Top[len(res.Top)-1].CumCostPct; last != 100 {
		t.Errorf("final CumCostPct = %v, want 100", last)
	}

	tierByName := map[string]ParetoTier{}
	for _, tr := range res.Tiers {
		tierByName[tr.Name] = tr
	}
	if tierByName["A"].RunCount != 2 {
		t.Errorf("tier A run count = %d, want 2 (100,50 -> cum 50%%,75%%)", tierByName["A"].RunCount)
	}
	if tierByName["B"].RunCount != 1 {
		t.Errorf("tier B run count = %d, want 1 (30 -> cum 90%%)", tierByName["B"].RunCount)
	}
	if tierByName["C"].RunCount != 1 {
		t.Errorf("tier C run count = %d, want 1 (20 -> cum 100%%)", tierByName["C"].RunCount)
	}
	if tierByName["C"].CostPct != 10 {
		t.Errorf("tier C cost pct = %v, want 10 (20/200)", tierByName["C"].CostPct)
	}
}

func TestSpendParetoExcludesRunning(t *testing.T) {
	st := insightTestStore(t)
	seedParetoRun(t, st, "r1", "a", "p", "done", 60)
	seedParetoRun(t, st, "r2", "a", "p", "done", 40)
	seedParetoRun(t, st, "run1", "a", "p", "running", 41.54) // F11: excluded

	res, err := SpendPareto(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Runs != 2 {
		t.Fatalf("Runs = %d, want 2 (running excluded)", res.Runs)
	}
	if res.TotalCost != 100 {
		t.Fatalf("TotalCost = %v, want 100 (running's cost excluded from denominator)", res.TotalCost)
	}
	if res.ExcludedRunning != 1 {
		t.Errorf("ExcludedRunning = %d, want 1", res.ExcludedRunning)
	}
	if res.ExcludedCost != 41.54 {
		t.Errorf("ExcludedCost = %v, want 41.54", res.ExcludedCost)
	}
	for _, r := range res.Top {
		if r.RunID == "run1" {
			t.Error("running run must not appear in top-N drill")
		}
	}
}

func TestSpendParetoEmpty(t *testing.T) {
	st := insightTestStore(t)
	res, err := SpendPareto(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Runs != 0 || res.TotalCost != 0 {
		t.Fatalf("empty fleet: res = %+v, want zero", res)
	}
	if len(res.Tiers) != 0 || len(res.Top) != 0 {
		t.Error("empty fleet should have no tiers/top rows")
	}
}

func TestSpendParetoTopNCap(t *testing.T) {
	st := insightTestStore(t)
	for i := 0; i < 15; i++ {
		seedParetoRun(t, st, string(rune('a'+i)), "agent", "p", "done", float64(15-i))
	}
	res, err := SpendPareto(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Top) != 10 {
		t.Fatalf("want top-10 cap, got %d", len(res.Top))
	}
	if res.Runs != 15 {
		t.Errorf("Runs = %d, want 15 (all finished runs counted, only top display capped)", res.Runs)
	}
}
