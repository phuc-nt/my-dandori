package govern

import (
	"context"
	"testing"
)

// These tests prove that the guardrail events red-team found at 0/0/0 in
// production actually land when the engine runs — the production path, not a
// seeded row. A zero in production means the fleet never triggered a rule
// (honest zero), not that capture is broken.

func TestGuardrailBlockEmitsEvent(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rb", 0)

	d := e.Evaluate(context.Background(), bashCall("rb", "rm -rf /"))
	if d.Verdict != Deny {
		t.Fatalf("expected deny, got %s", d.Verdict)
	}
	var n int
	e.St.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='rb' AND kind='guardrail_block' AND ok=0`).Scan(&n)
	if n == 0 {
		t.Error("block produced no guardrail_block event — capture path broken")
	}
	// The decision is also recorded in the hash-chained audit log.
	var audits int
	e.St.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE subject='rb'`).Scan(&audits)
	if audits == 0 {
		t.Error("block produced no audit entry")
	}
}

func TestPermissionAskEmitsEvent(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rg", 0)
	// A gate rule asks for permission; GateWaitSeconds=0 → times out to a
	// deny, but the permission_ask event must still be recorded.
	if _, err := CreateRule(e.St, "gate", "curl", "gated network call", "global", "", false, "tester"); err != nil {
		t.Fatal(err)
	}
	d := e.Evaluate(context.Background(), bashCall("rg", "curl http://example.com"))
	if d.Verdict == Allow {
		t.Fatalf("gate should not allow on timeout, got %s", d.Verdict)
	}
	var n int
	e.St.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='rg' AND kind='permission_ask'`).Scan(&n)
	if n == 0 {
		t.Error("gate produced no permission_ask event — capture path broken")
	}
}

func TestKillEmitsEvent(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rk", 0)

	if err := KillRun(e.St, "rk", "tester", "audit-kill"); err != nil {
		t.Fatal(err)
	}
	var kind, status string
	e.St.DB.QueryRow(`SELECT status FROM runs WHERE id='rk'`).Scan(&status)
	e.St.DB.QueryRow(`SELECT kind FROM events WHERE run_id='rk' AND kind='kill'`).Scan(&kind)
	if status != "killed" {
		t.Errorf("run status = %s, want killed", status)
	}
	if kind != "kill" {
		t.Error("KillRun produced no kill event — capture path broken")
	}
}
