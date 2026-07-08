package govern

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// seedToolUseEvents inserts n raw tool_use events for runID, with a
// deterministic increasing timestamp (well in the past, one second apart)
// so window ordering is stable regardless of wall-clock speed.
func seedToolUseEvents(t testing.TB, e *Engine, runID, tool string, n int) {
	t.Helper()
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		if _, err := e.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
			VALUES(?, ?, 'tool_use', ?, 1, '{}')`, runID, ts, tool); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRiskScoreSlidingWindow(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rw1", 0)
	// 100 Bash tool_use events, but WindowN default is 40 → only the last 40
	// count. Bash=2pts each → max 40*2=80, well under the default threshold
	// of 100 — a long clean run must not cross the gate purely from length.
	seedToolUseEvents(t, e, "rw1", "Bash", 100)

	score, err := e.RiskScore("rw1")
	if err != nil {
		t.Fatal(err)
	}
	if score != 80 {
		t.Errorf("score = %d, want 80 (40 events * 2pts, window caps out old events)", score)
	}
	if score >= e.Cfg.RiskScore.ThresholdValue() {
		t.Errorf("score %d must stay under default threshold %d — long clean run must not gate", score, e.Cfg.RiskScore.ThresholdValue())
	}
}

func TestRiskScoreSelfExclusion(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rw2", 0)
	seedToolUseEvents(t, e, "rw2", "Bash", 5) // 10pts base

	a := &Audit{St: e.St, Actor: "tester"}
	// A real denial: +25pts.
	if _, err := a.Append("guardrail_block", "rw2", "rm -rf /"); err != nil {
		t.Fatal(err)
	}
	scoreAfterRealDenial, err := e.RiskScore("rw2")
	if err != nil {
		t.Fatal(err)
	}
	if scoreAfterRealDenial != 10+25 {
		t.Fatalf("score after 1 real denial = %d, want 35", scoreAfterRealDenial)
	}

	// engine_error, budget_block, risk_gate must NOT add points.
	for _, action := range []string{"engine_error", "budget_block", "risk_gate"} {
		if _, err := a.Append(action, "rw2", "noise"); err != nil {
			t.Fatal(err)
		}
	}
	scoreAfterNoise, err := e.RiskScore("rw2")
	if err != nil {
		t.Fatal(err)
	}
	if scoreAfterNoise != scoreAfterRealDenial {
		t.Errorf("score changed after excluded actions: %d -> %d, want unchanged", scoreAfterRealDenial, scoreAfterNoise)
	}
}

// TestRiskScoreApprovingEscalationDoesNotRatchet proves the core anti-ratchet
// fix end-to-end: approving a risk escalation writes a risk_gate audit entry
// (via engine.record), and that entry must not feed the next score.
func TestRiskScoreApprovingEscalationDoesNotRatchet(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rw3", 0)
	e.Cfg.RiskScore.Mode = "gate"
	e.Cfg.RiskScore.Threshold = 5 // low, so 3 Bash calls (6pts) trip it
	seedToolUseEvents(t, e, "rw3", "Bash", 3)

	before, err := e.RiskScore("rw3")
	if err != nil {
		t.Fatal(err)
	}

	// Directly record a risk_gate decision the way engine.Evaluate does,
	// simulating an approved escalation.
	e.record(bashCall("rw3", "ls"), "risk_gate", Decision{Allow, "risk approval #1 granted by tester"})

	after, err := e.RiskScore("rw3")
	if err != nil {
		t.Fatal(err)
	}
	// The recorded event is kind=guardrail_block (per engine.record's kind
	// mapping) but ok=1 (Allow) and NOT kind=tool_use, so it must not add
	// tool_use points; and the audit_log action is "risk_gate", excluded
	// from denial counting. Net risk delta from the approval itself must be 0.
	if after != before {
		t.Errorf("score changed after approved risk_gate: %d -> %d, want unchanged (anti-ratchet)", before, after)
	}
}

func TestRiskScoreLogModeDoesNotBlock(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rw4", 0)
	e.Cfg.RiskScore.Threshold = 5 // default mode is "" == log
	seedToolUseEvents(t, e, "rw4", "Bash", 3)

	d := e.Evaluate(context.Background(), bashCall("rw4", "ls"))
	if d.Verdict != Allow {
		t.Fatalf("log mode over threshold must still allow, got %s: %s", d.Verdict, d.Reason)
	}
	var kind string
	err := e.St.DB.QueryRow(`SELECT kind FROM events WHERE run_id='rw4' AND kind='risk_would_gate' ORDER BY id DESC LIMIT 1`).Scan(&kind)
	if err != nil {
		t.Fatalf("risk_would_gate event missing: %v", err)
	}
}

func TestRiskScoreGateModeEscalates(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rw5", 0)
	e.Cfg.RiskScore.Mode = "gate"
	e.Cfg.RiskScore.Threshold = 5
	seedToolUseEvents(t, e, "rw5", "Bash", 3)

	// GateWaitSeconds=0 in testEngine → immediate timeout-deny, same pattern
	// as TestGateTimeoutAndApprove.
	d := e.Evaluate(context.Background(), bashCall("rw5", "ls"))
	if d.Verdict != Deny {
		t.Fatalf("gate mode over threshold must escalate (deny-on-timeout in test harness), got %s", d.Verdict)
	}
	var id int64
	e.St.DB.QueryRow(`SELECT id FROM approvals WHERE run_id='rw5' AND status='pending' ORDER BY id DESC LIMIT 1`).Scan(&id)
	if id == 0 {
		t.Fatal("risk approval row missing")
	}

	won, err := Decide(e.St, id, true, "phucnt", "looks fine")
	if err != nil || !won {
		t.Fatalf("approve: won=%v err=%v", won, err)
	}
	var status string
	e.St.DB.QueryRow(`SELECT status FROM approvals WHERE id=?`, id).Scan(&status)
	if status != "approved" {
		t.Errorf("status = %s, want approved", status)
	}

	rejD := e.Evaluate(context.Background(), bashCall("rw5", "ls -la"))
	if rejD.Verdict != Deny {
		t.Fatalf("second call (new approval) must deny-on-timeout too, got %s", rejD.Verdict)
	}
	var id2 int64
	e.St.DB.QueryRow(`SELECT id FROM approvals WHERE run_id='rw5' AND status='pending' ORDER BY id DESC LIMIT 1`).Scan(&id2)
	if id2 == 0 || id2 == id {
		t.Fatal("expected a distinct new pending approval for the second call")
	}
	if won, _ := Decide(e.St, id2, false, "phucnt", "reject this one"); !won {
		t.Fatal("reject decide must win")
	}
}

func TestRiskScoreGateModeNonGuardedToolNoOp(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rw6", 0)
	e.Cfg.RiskScore.Mode = "gate"
	e.Cfg.RiskScore.Threshold = 1 // trivially over threshold
	seedToolUseEvents(t, e, "rw6", "Read", 10)

	input, err := json.Marshal(map[string]string{"file_path": "/work/proj/f.go"})
	if err != nil {
		t.Fatal(err)
	}
	tc := ExtractToolCall("rw6", "a1", "proj", "/work/proj", "Read", input)
	d := e.Evaluate(context.Background(), tc)
	if d.Verdict != Allow {
		t.Errorf("non-guarded tool must not be gated by risk score, got %s: %s", d.Verdict, d.Reason)
	}
	var approvals int
	e.St.DB.QueryRow(`SELECT count(*) FROM approvals WHERE run_id='rw6'`).Scan(&approvals)
	if approvals != 0 {
		t.Errorf("non-guarded tool must not create an approval, got %d", approvals)
	}
}

func TestRiskScoreQueryErrorFailMode(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rw7", 0)
	// Corrupt the events table to force a scan error inside RiskScore.
	e.St.DB.Exec(`DROP TABLE events`)

	// LOG mode (default): query error must allow (nothing to gate yet).
	d := e.Evaluate(context.Background(), bashCall("rw7", "ls"))
	if d.Verdict != Allow {
		t.Errorf("log mode + scoring error must allow, got %s: %s", d.Verdict, d.Reason)
	}

	e.Cfg.RiskScore.Mode = "gate"
	d2 := e.Evaluate(context.Background(), bashCall("rw7", "ls"))
	if d2.Verdict != Deny {
		t.Errorf("gate mode + scoring error must fail-closed deny, got %s: %s", d2.Verdict, d2.Reason)
	}
}

// TestRiskScoreTrustedBandDoesNotSkip proves G5 is NOT exempted by the
// trusted autonomy band the way checkGate's non-critical rules are (G4) —
// risk accrual reflects recent behavior, which a trust band earned on
// cost/edit history does not vouch for.
func TestRiskScoreTrustedBandDoesNotSkip(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "rw8", 0)
	if err := SetBand(e.St, "a1", BandTrusted, "tester", "trusted for test"); err != nil {
		t.Fatal(err)
	}
	e.Cfg.RiskScore.Mode = "gate"
	e.Cfg.RiskScore.Threshold = 5
	seedToolUseEvents(t, e, "rw8", "Bash", 3)

	d := e.Evaluate(context.Background(), bashCall("rw8", "ls"))
	if d.Verdict != Deny {
		t.Fatalf("trusted band must still be escalated by G5, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestSnapshotEvaluateHasNoRiskBranch proves the central-mode offline
// evaluator's behavior is unchanged by G5 — it has no local events/audit_log
// to score against, so it must never gain a risk branch.
func TestSnapshotEvaluateHasNoRiskBranch(t *testing.T) {
	snap := &PolicySnapshot{Bands: map[string]string{}}
	tc := ToolCall{RunID: "r", AgentID: "a", ToolName: "Bash", Command: "ls"}
	d := snap.Evaluate(tc)
	if d.Verdict != Allow {
		t.Errorf("snapshot Evaluate with no rules/kill/budget must allow, got %s: %s", d.Verdict, d.Reason)
	}
}
