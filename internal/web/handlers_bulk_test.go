package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Bulk-kill of runs with no live process reports "marked", not "signaled" —
// the honest tally that prevents a false "all killed" (M4).
func TestBulkKillHonestTally(t *testing.T) {
	s := testServer(t)
	wireLauncher(t, s)
	// Two console runs with no live process (not in registry).
	seedConsoleRun(t, s, "b1", "running")
	seedConsoleRun(t, s, "b2", "running")

	rec := postForm(t, s, "/runs/bulk-kill", url.Values{"run_ids": {"b1", "b2"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk kill: %d", rec.Code)
	}
	body := rec.Body.String()
	// No live registry entry → both marked-only, zero signaled.
	if !strings.Contains(body, "0 đã dừng process") || !strings.Contains(body, "2 chỉ đánh dấu") {
		t.Errorf("dishonest tally (should be 0 signaled / 2 marked): %s", body)
	}
	// Both flipped to killed with an audit each.
	var killed, audits int
	s.Store.DB.QueryRow(`SELECT count(*) FROM runs WHERE id IN ('b1','b2') AND status='killed'`).Scan(&killed)
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='kill_run'`).Scan(&audits)
	if killed != 2 || audits != 2 {
		t.Errorf("killed=%d audits=%d, want 2/2 (per-item audit)", killed, audits)
	}
}

func TestBulkKillEmptySelection(t *testing.T) {
	s := testServer(t)
	wireLauncher(t, s)
	rec := postForm(t, s, "/runs/bulk-kill", url.Values{})
	if !strings.Contains(rec.Body.String(), "Chưa chọn") {
		t.Errorf("empty selection should be a gentle message: %s", rec.Body.String())
	}
}

func TestBulkBudgetSetsPerAgent(t *testing.T) {
	s := testServer(t)
	wireLauncher(t, s)
	s.Store.DB.Exec(`INSERT INTO agents(id,name,created_at) VALUES('ag','ag','now') ON CONFLICT DO NOTHING`)
	s.Store.DB.Exec(`INSERT INTO runs(id,session_id,agent_id,source,status,started_at) VALUES('r1','r1','ag','console','done','now')`)
	s.Store.DB.Exec(`INSERT INTO runs(id,session_id,agent_id,source,status,started_at) VALUES('r2','r2','ag','console','done','now')`)

	rec := postForm(t, s, "/runs/bulk-budget", url.Values{"run_ids": {"r1", "r2"}, "amount": {"25"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk budget: %d", rec.Code)
	}
	// Two runs share one agent → one budget row.
	var limit float64
	if err := s.Store.DB.QueryRow(`SELECT limit_usd FROM budgets WHERE scope_type='agent' AND scope_id='ag'`).Scan(&limit); err != nil {
		t.Fatal(err)
	}
	if limit != 25 {
		t.Errorf("budget = %v, want 25", limit)
	}
	if !strings.Contains(rec.Body.String(), "1 agent") {
		t.Errorf("summary should report 1 agent set: %s", rec.Body.String())
	}
}

func TestBulkBudgetInvalidAmount(t *testing.T) {
	s := testServer(t)
	wireLauncher(t, s)
	rec := postForm(t, s, "/runs/bulk-budget", url.Values{"run_ids": {"x"}, "amount": {"-5"}})
	if !strings.Contains(rec.Body.String(), "không hợp lệ") {
		t.Errorf("negative amount should be rejected: %s", rec.Body.String())
	}
}
