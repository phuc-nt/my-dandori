package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

func TestWallboardZeroStateNoPanic(t *testing.T) {
	s := testServer(t)
	s.registerPhase06Routes()

	rec := get(t, s, "/wallboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wallboard → %d body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "Dandori Fleet") {
		t.Error("wallboard shell did not render")
	}

	rec = get(t, s, "/wallboard/fragment")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wallboard/fragment (zero state) → %d body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "không có run nào đang chạy") {
		t.Errorf("zero-state running section missing: %s", rec.Body)
	}
}

func TestWallboardFragmentRunningCountAndQueueDepth(t *testing.T) {
	s := testServer(t)
	s.registerPhase06Routes()

	seedRun(t, s, "r1", "running")
	seedRun(t, s, "r2", "running")
	seedRun(t, s, "r3", "done")

	if _, err := s.Store.DB.Exec(`INSERT INTO approvals(action, status, requested_at) VALUES('kill','pending',?)`,
		store.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO flags(run_id, reason, status, created_at) VALUES('r1','oops','open',?)`,
		store.Now()); err != nil {
		t.Fatal(err)
	}

	data := s.buildWallboardData()
	if len(data.Running) != 2 {
		t.Errorf("Running = %d, want 2", len(data.Running))
	}
	if data.QueueDepth != 2 {
		t.Errorf("QueueDepth = %d, want 2 (1 pending approval + 1 open flag)", data.QueueDepth)
	}
}

func TestWallboardFragmentSpendVsBudget(t *testing.T) {
	s := testServer(t)
	s.registerPhase06Routes()

	if _, err := s.Store.DB.Exec(`INSERT INTO budgets(scope_type, scope_id, limit_usd) VALUES('global','',100)`); err != nil {
		t.Fatal(err)
	}
	seedRunWithCost(t, s, "r1", "running", 25.0)

	data := s.buildWallboardData()
	if !data.HasBudget {
		t.Fatal("HasBudget = false, want true")
	}
	if data.BudgetUSD != 100 {
		t.Errorf("BudgetUSD = %v, want 100", data.BudgetUSD)
	}
	if data.SpendPct < 24.9 || data.SpendPct > 25.1 {
		t.Errorf("SpendPct = %v, want ~25", data.SpendPct)
	}
}

func seedRun(t *testing.T, s *Server, id, status string) {
	t.Helper()
	seedRunWithCost(t, s, id, status, 0)
}

func seedRunWithCost(t *testing.T, s *Server, id, status string, cost float64) {
	t.Helper()
	_, err := s.Store.DB.Exec(
		`INSERT INTO runs(id, session_id, status, started_at, source, runtime, cost_usd) VALUES(?, ?, ?, ?, 'hook', 'claude-code', ?)`,
		id, id+"-sess", status, store.Now(), cost)
	if err != nil {
		t.Fatal(err)
	}
}
