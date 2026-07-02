package govern

import (
	"database/sql"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedGradedAgent creates an agent whose runs produce roughly the target
// grade: bad=true → all failed runs with tool errors (F territory).
func seedGradedAgent(t *testing.T, e *Engine, agent string, bad bool, runs int) {
	t.Helper()
	_, err := e.St.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?, ?, ?)
		ON CONFLICT(name) DO NOTHING`, agent, agent, store.Now())
	if err != nil {
		t.Fatal(err)
	}
	status := "done"
	if bad {
		status = "failed"
	}
	for i := 0; i < runs; i++ {
		id := agent + "-r" + string(rune('0'+i))
		if _, err := e.St.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, status, started_at, ended_at, cost_usd)
			VALUES(?, ?, ?, 'proj', ?, ?, ?, 1)`, id, id, agent, status, store.Now(), store.Now()); err != nil {
			t.Fatal(err)
		}
		if bad {
			// tool errors + permission asks → reliability and autonomy tank
			e.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload) VALUES(?, ?, 'tool_use', 'Edit', NULL, '')`, id, store.Now())
			e.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload) VALUES(?, ?, 'tool_result', 'Edit', 0, 'err')`, id, store.Now())
			e.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload) VALUES(?, ?, 'permission_ask', 'Bash', 0, '')`, id, store.Now())
		}
	}
}

func TestClosedLoopFlagsAndDemotesF(t *testing.T) {
	e := testEngine(t)
	// Calibrated-ish fleet: 5 good agents + 1 disaster → disaster lands F/D.
	for _, a := range []string{"g1", "g2", "g3", "g4", "g5"} {
		seedGradedAgent(t, e, a, false, 3)
	}
	seedGradedAgent(t, e, "bad", true, 5) // ≥ minRunsForLoop — the loop ignores smaller samples

	var sunk []int64
	res, err := RunClosedLoop(e.St, e.Cfg, func(id int64) { sunk = append(sunk, id) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Flagged != 1 || len(sunk) != 1 {
		t.Fatalf("flagged=%d sink=%d, want 1/1 — tied good agents must NOT grade F (details %v)",
			res.Flagged, len(sunk), res.Details)
	}
	if res.Demoted+res.Proposed != 1 {
		t.Fatalf("expected exactly one band action, got demoted=%d proposed=%d", res.Demoted, res.Proposed)
	}
	// Dedup: second cycle does nothing new.
	res2, _ := RunClosedLoop(e.St, e.Cfg, nil)
	if res2.Flagged != 0 {
		t.Errorf("re-run flagged: %d, want 0", res2.Flagged)
	}
	// Audit chain carries the loop's actions.
	var audits int
	e.St.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE actor='dandori-closed-loop'`).Scan(&audits)
	if audits == 0 {
		t.Error("closed loop must audit its actions")
	}
}

func TestClosedLoopProposalApplyAndRecovery(t *testing.T) {
	e := testEngine(t)
	seedGradedAgent(t, e, "meh", false, 3)
	// Force a pending demote proposal as the loop would for a D agent.
	if err := proposeDemote(e.St, "meh", "meh-r0", "test"); err != nil {
		t.Fatal(err)
	}
	// Human approves in the review queue.
	var id int64
	e.St.DB.QueryRow(`SELECT id FROM approvals WHERE action LIKE 'band-demote:%'`).Scan(&id)
	if won, err := Decide(e.St, id, true, "phucnt", "agreed"); err != nil || !won {
		t.Fatalf("decide: %v %v", won, err)
	}
	res, err := RunClosedLoop(e.St, e.Cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 {
		t.Fatalf("applied: %d", res.Applied)
	}
	if BandFor(e.St, "meh") != BandSupervised {
		t.Errorf("band not applied: %s", BandFor(e.St, "meh"))
	}
	// Applying twice is impossible (consume-once).
	res2, _ := RunClosedLoop(e.St, e.Cfg, nil)
	if res2.Applied != 0 {
		t.Errorf("re-applied: %d", res2.Applied)
	}

	// Recovery: give the agent an open low-grade flag, agent grades fine → resolved.
	var flagID sql.NullInt64
	e.St.DB.Exec(`INSERT INTO flags(run_id, reason, created_at) VALUES('meh-r0', 'low grade D: test', ?)`, store.Now())
	res3, err := RunClosedLoop(e.St, e.Cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res3.Resolved != 1 {
		t.Errorf("resolved: %d (flag %v)", res3.Resolved, flagID)
	}
}
