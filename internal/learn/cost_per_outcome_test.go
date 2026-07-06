package learn

import "testing"

func TestCostPerOutcomeByProject(t *testing.T) {
	st := insightTestStore(t)
	// proj-a: 2 done ($4), 1 failed ($1), 1 running ($1). Total $6 / 2 done = $3.
	seedInsightRun(t, st, "a1", "proj-a", "ag", "m", "done", 2, 0, 0)
	seedInsightRun(t, st, "a2", "proj-a", "ag", "m", "done", 2, 0, 0)
	seedInsightRun(t, st, "a3", "proj-a", "ag", "m", "failed", 1, 0, 0)
	seedInsightRun(t, st, "a4", "proj-a", "ag", "m", "running", 1, 0, 0)

	rows, err := CostPerOutcome(st, 0, "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 project, got %d", len(rows))
	}
	o := rows[0]
	if o.Group != "proj-a" || o.Runs != 4 || o.Done != 2 || o.Failed != 1 || o.Running != 1 {
		t.Errorf("row wrong: %+v", o)
	}
	if o.TotalCost != 6 || o.CostPerDone != 3 {
		t.Errorf("cost = %v, per-done = %v; want 6 / 3", o.TotalCost, o.CostPerDone)
	}
	if o.Insufficient() {
		t.Error("n=4 >= 3 should be sufficient")
	}
}

func TestCostPerOutcomeNoDoneNoDivZero(t *testing.T) {
	st := insightTestStore(t)
	// A project with cost but zero done runs must not divide by zero.
	seedInsightRun(t, st, "r1", "proj-x", "ag", "m", "running", 5, 0, 0)
	rows, err := CostPerOutcome(st, 0, "project")
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].CostPerDone != 0 {
		t.Errorf("no done runs → cost/done must be 0, got %v", rows[0].CostPerDone)
	}
	if got := rows[0].FormatCostPerDone(); got != "— (chưa có run xong)" {
		t.Errorf("format = %q", got)
	}
}

func TestCostPerOutcomeByAgent(t *testing.T) {
	st := insightTestStore(t)
	seedInsightRun(t, st, "b1", "p", "agent-x", "m", "done", 3, 0, 0)
	rows, err := CostPerOutcome(st, 0, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Group != "agent-x" {
		t.Errorf("group-by agent wrong: %+v", rows)
	}
}
