package govern

import (
	"context"
	"strings"
	"testing"
)

// setModel sets runs.model directly, simulating a transcript reconcile
// having already run (or not, for "").
func setModel(t testing.TB, e *Engine, runID, model string) {
	t.Helper()
	if _, err := e.St.DB.Exec(`UPDATE runs SET model = ? WHERE id = ?`, model, runID); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetHardModeRegression(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.Mode = "hard"
	e.Cfg.Budget.GlobalMonthlyUSD = 10
	seedRun(t, e, "r1", 11)
	setModel(t, e, "r1", "claude-haiku-4-5") // even a cheap model must be denied in hard mode
	d := e.Evaluate(context.Background(), bashCall("r1", "ls"))
	if d.Verdict != Deny {
		t.Fatalf("hard mode over budget must deny regardless of model, got %s", d.Verdict)
	}
}

func TestBudgetDowngradeExpensiveModelDenies(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 10
	seedRun(t, e, "r1", 11)
	setModel(t, e, "r1", "claude-opus-4-8")
	d := e.Evaluate(context.Background(), bashCall("r1", "ls"))
	if d.Verdict != Deny {
		t.Fatalf("downgrade mode + expensive model must deny, got %s", d.Verdict)
	}
	if !containsAll(d.Reason, "opus", "/model") {
		t.Errorf("deny reason must name the model and hint /model, got %q", d.Reason)
	}
}

func TestBudgetDowngradeCheapModelAllows(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 10
	seedRun(t, e, "r1", 11)
	setModel(t, e, "r1", "claude-haiku-4-5")
	d := e.Evaluate(context.Background(), bashCall("r1", "ls"))
	if d.Verdict != Allow {
		t.Fatalf("downgrade mode + cheap model must allow, got %s (%s)", d.Verdict, d.Reason)
	}
	var n int
	e.St.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='budget_downgrade_allow' AND run_id='r1'`).Scan(&n)
	if n != 1 {
		t.Errorf("budget_downgrade_allow events for r1: %d, want 1", n)
	}

	// Calling again this run/month must not emit a second event (dedup via
	// events query, not a settings tombstone).
	e.Evaluate(context.Background(), bashCall("r1", "ls"))
	e.St.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='budget_downgrade_allow' AND run_id='r1'`).Scan(&n)
	if n != 1 {
		t.Errorf("budget_downgrade_allow events after second call: %d, want 1 (dedup)", n)
	}
}

func TestBudgetNullModelAgentHistoryExpensiveDenies(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 10
	// Agent's prior run this month settled on an expensive model.
	seedRun(t, e, "prev", 1)
	setModel(t, e, "prev", "claude-opus-4-8")
	// Current run over budget, model still NULL/unreconciled.
	seedRun(t, e, "r1", 11)
	d := e.Evaluate(context.Background(), bashCall("r1", "ls"))
	if d.Verdict != Deny {
		t.Fatalf("NULL model with expensive agent history must deny, got %s", d.Verdict)
	}
}

func TestBudgetNullModelNoHistoryUnderCapAllows(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 10
	seedRun(t, e, "r1", 11) // no prior run for this agent, model NULL
	d := e.Evaluate(context.Background(), bashCall("r1", "ls"))
	if d.Verdict != Allow {
		t.Fatalf("NULL model, no expensive history, under cap must allow, got %s (%s)", d.Verdict, d.Reason)
	}
	var n int
	e.St.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='budget_downgrade_allow' AND run_id='r1'`).Scan(&n)
	if n != 1 {
		t.Errorf("budget_downgrade_allow event missing for null-allow path: %d", n)
	}
}

func TestBudgetNullModelOverCapDenies(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 10
	e.Cfg.Budget.NullAllowCap = 2
	seedRun(t, e, "r1", 11)

	// First two calls consume the cap (each is a distinct evaluation of the
	// same run; the counter is per agent+month, not per run, matching the
	// restart-session evasion this gate defends against).
	for i := 0; i < 2; i++ {
		d := e.Evaluate(context.Background(), bashCall("r1", "ls"))
		if d.Verdict != Allow {
			t.Fatalf("call %d under cap must allow, got %s (%s)", i, d.Verdict, d.Reason)
		}
	}
	// Third call exceeds the cap of 2.
	d := e.Evaluate(context.Background(), bashCall("r1", "ls"))
	if d.Verdict != Deny {
		t.Fatalf("call over null_allow_cap must deny, got %s", d.Verdict)
	}
}

func TestBudgetRunModelQueryErrorDenies(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 10
	// Force spend over budget without seeding a runs row for "ghost" — the
	// runModel point-query then finds no matching row (sql.ErrNoRows),
	// which must fail closed.
	seedRun(t, e, "other", 11)
	d := e.Evaluate(context.Background(), bashCall("ghost", "ls"))
	if d.Verdict != Deny {
		t.Fatalf("runModel query error must deny (fail-closed), got %s", d.Verdict)
	}
}

func TestBudgetExpensiveModelsEmptyDisablesHardLimit(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 10
	e.Cfg.Budget.ExpensiveModels = []string{} // explicit empty = disable hard limit
	seedRun(t, e, "r1", 11)
	setModel(t, e, "r1", "claude-opus-4-8") // would normally deny
	d := e.Evaluate(context.Background(), bashCall("r1", "ls"))
	if d.Verdict != Allow {
		t.Fatalf("expensive_models=[] must always allow over budget, got %s (%s)", d.Verdict, d.Reason)
	}

	// Warn thresholds still fire even with the hard limit disabled.
	e2 := testEngine(t)
	e2.Cfg.Budget.GlobalMonthlyUSD = 10
	e2.Cfg.Budget.ExpensiveModels = []string{}
	seedRun(t, e2, "r2", 8)
	e2.Evaluate(context.Background(), bashCall("r2", "ls"))
	var warns int
	e2.St.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='budget_warn'`).Scan(&warns)
	if warns == 0 {
		t.Error("warn thresholds must still fire when expensive_models is disabled")
	}
}

func TestBudgetWarnThresholdsUnderLimitRegression(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 10
	seedRun(t, e, "r1", 8) // 80% — crosses 50 and 75, not 90
	e.Evaluate(context.Background(), bashCall("r1", "ls"))
	e.Evaluate(context.Background(), bashCall("r1", "ls"))
	var warns int
	e.St.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='budget_warn'`).Scan(&warns)
	if warns != 2 {
		t.Errorf("budget_warn events under 100%%: %d, want 2 (50%% and 75%%, deduped)", warns)
	}
}

// TestSnapshotBudgetBranchDivergesFromLocal proves the central Evaluate path
// (this file's own concern: does the LOCAL downgrade-gate — Engine.checkBudget
// below, ExpensiveModels/runModel/nullAllowGate — stay untouched by this
// phase) no longer hard-denies by default; it now asks a human instead, since
// central has no per-run model to downgrade against. budget.mode: hard is the
// only central path that still denies outright.
func TestSnapshotBudgetBranchDivergesFromLocal(t *testing.T) {
	snap := &PolicySnapshot{BudgetExceeded: true}
	if d, action := snap.Evaluate(ToolCall{ToolName: "Bash", Command: "ls"}); d.Verdict != Ask {
		t.Errorf("central budget-exceeded Bash, default mode: %s, want Ask", d.Verdict)
	} else if action != ActionPermissionAsk {
		t.Errorf("budget ask action = %q, want %q", action, ActionPermissionAsk)
	}
	if d, _ := snap.Evaluate(ToolCall{ToolName: "Write", Paths: []string{"/x"}}); d.Verdict != Ask {
		t.Errorf("central budget-exceeded Write, default mode: %s, want Ask", d.Verdict)
	}
	if d, action := snap.Evaluate(ToolCall{ToolName: "Read"}); d.Verdict != Allow {
		t.Errorf("central budget-exceeded Read must still allow: %s", d.Verdict)
	} else if action != "" {
		t.Errorf("Allow must carry no action, got %q", action)
	}

	hardSnap := &PolicySnapshot{BudgetExceeded: true, BudgetMode: "hard"}
	if d, action := hardSnap.Evaluate(ToolCall{ToolName: "Bash", Command: "ls"}); d.Verdict != Deny {
		t.Errorf("central budget-exceeded Bash, hard mode: %s, want Deny", d.Verdict)
	} else if action != ActionBudgetBlock {
		t.Errorf("budget deny action = %q, want %q", action, ActionBudgetBlock)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
