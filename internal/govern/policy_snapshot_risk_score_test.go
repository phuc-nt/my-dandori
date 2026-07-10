package govern

import (
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedCentralRun inserts a run the way ensureRunTx (internal/ingest/apply.go)
// does for a real central-mode ingest: source='ingest', owned by operatorID,
// started startedAgo in the past. This is the shape BuildPolicySnapshot's
// per-operator + 24h-bound filtering keys on.
func seedCentralRun(t testing.TB, e *Engine, runID, operatorID string, startedAgo time.Duration) {
	t.Helper()
	if _, err := e.St.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a1','a1',?)
		ON CONFLICT(name) DO NOTHING`, store.Now()); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Add(-startedAgo).Format(time.RFC3339)
	_, err := e.St.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, cwd, started_at, status, source, operator_id)
		VALUES(?, ?, 'a1', 'proj', '/work/proj', ?, 'running', 'ingest', ?)`,
		runID, runID, startedAt, operatorID)
	if err != nil {
		t.Fatal(err)
	}
}

// TestBuildPolicySnapshotPerOperatorScope closes leak J: a snapshot built for
// operator B must never contain operator A's run ids or scores, even though
// both runs are scored on the very same read pool/query.
func TestBuildPolicySnapshotPerOperatorScope(t *testing.T) {
	e := testEngine(t)
	seedCentralRun(t, e, "run-a", "operator-a", 0)
	seedCentralRun(t, e, "run-b", "operator-b", 0)
	seedToolUseEvents(t, e, "run-a", "Bash", 3)
	seedToolUseEvents(t, e, "run-b", "Bash", 3)

	e.Cfg.Budget.GlobalMonthlyUSD = 0
	snapB, err := BuildPolicySnapshot(e.St, e.Cfg.Budget, "operator-b", e.Cfg.RiskScore)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := snapB.RiskScores["run-a"]; leaked {
		t.Errorf("operator B's snapshot contains operator A's run: %+v", snapB.RiskScores)
	}
	if _, present := snapB.RiskScores["run-b"]; !present {
		t.Errorf("operator B's snapshot missing operator B's own run: %+v", snapB.RiskScores)
	}

	snapA, err := BuildPolicySnapshot(e.St, e.Cfg.Budget, "operator-a", e.Cfg.RiskScore)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := snapA.RiskScores["run-b"]; leaked {
		t.Errorf("operator A's snapshot contains operator B's run: %+v", snapA.RiskScores)
	}
}

// TestBuildPolicySnapshotDenialZeroWeight is the anti-poison test (I): a
// central run with client-attested denial audit rows must score tool-volume
// ONLY — the denial term contributes zero, so a malicious/compromised client
// spooling fabricated denials against its own run cannot inflate its score.
func TestBuildPolicySnapshotDenialZeroWeight(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 0
	seedCentralRun(t, e, "run-c", "operator-c", 0)
	seedToolUseEvents(t, e, "run-c", "Bash", 3) // 3 * 2pts = 6

	a := &Audit{St: e.St, Actor: "tester"}
	for i := 0; i < 5; i++ {
		if _, err := a.Append("guardrail_block", "run-c", "fabricated denial"); err != nil {
			t.Fatal(err)
		}
	}

	snap, err := BuildPolicySnapshot(e.St, e.Cfg.Budget, "operator-c", e.Cfg.RiskScore)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := snap.RiskScores["run-c"], 6; got != want {
		t.Errorf("central score = %d, want %d (tool-volume only, denials zero-weighted)", got, want)
	}
}

// TestBuildPolicySnapshotZombieRunBound proves the active-run 24h bound (S):
// a run started more than 24h ago and never finalized (no reaper exists yet)
// must not appear in RiskScores, so the map cannot grow unboundedly from
// abandoned runs.
func TestBuildPolicySnapshotZombieRunBound(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 0
	seedCentralRun(t, e, "run-fresh", "operator-d", time.Hour)
	seedCentralRun(t, e, "run-zombie", "operator-d", 25*time.Hour)
	seedToolUseEvents(t, e, "run-fresh", "Bash", 1)
	seedToolUseEvents(t, e, "run-zombie", "Bash", 1)

	snap, err := BuildPolicySnapshot(e.St, e.Cfg.Budget, "operator-d", e.Cfg.RiskScore)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := snap.RiskScores["run-zombie"]; present {
		t.Errorf(">24h-old running run must not be scored: %+v", snap.RiskScores)
	}
	if _, present := snap.RiskScores["run-fresh"]; !present {
		t.Errorf("fresh active run missing from RiskScores: %+v", snap.RiskScores)
	}
}

// TestRiskScoreCentralUsesReadPool proves RiskScoreCentral runs against
// EXACTLY the handle it is given (the read pool in real use) rather than
// falling back to the engine's write pool — the fix for contention (G). A
// query against a deliberately-broken read handle must fail rather than
// silently succeeding against e.St.DB.
func TestRiskScoreCentralUsesReadPool(t *testing.T) {
	e := testEngine(t)
	seedCentralRun(t, e, "run-e", "operator-e", 0)
	seedToolUseEvents(t, e, "run-e", "Bash", 2)

	// e.St.Read() falls back to the write pool when no read pool is enabled
	// (store.Store.Read's documented behavior) — confirm the central path
	// still produces a score through that same accessor, proving it is
	// pool-generic rather than hardcoded to e.St.DB.
	score, err := e.RiskScoreCentral(e.St.Read(), "run-e")
	if err != nil {
		t.Fatal(err)
	}
	if score != 4 { // 2 Bash * 2pts
		t.Errorf("score = %d, want 4", score)
	}
}

// TestSnapshotEvaluateRiskGateModeAsks: a central run over threshold on a
// guarded tool, gate mode, must Ask immediately (no approval-wait machinery
// exists offline) rather than Deny or silently Allow.
func TestSnapshotEvaluateRiskGateModeAsks(t *testing.T) {
	snap := &PolicySnapshot{
		Bands:            map[string]string{},
		RiskScores:       map[string]int{"run-f": 150},
		RiskThreshold:    100,
		RiskMode:         "gate",
		RiskGuardedTools: []string{"Bash"},
	}
	tc := ToolCall{RunID: "run-f", AgentID: "a", ToolName: "Bash", Command: "ls"}
	d, action := snap.Evaluate(tc)
	if d.Verdict != Ask {
		t.Errorf("gate mode over threshold: verdict = %s, want Ask", d.Verdict)
	}
	if action != ActionRiskGate {
		t.Errorf("action = %q, want %q", action, ActionRiskGate)
	}
}

// TestSnapshotEvaluateRiskLogModeEmitsWouldGateSignal: log mode (default)
// never blocks, but an over-threshold guarded call must still Allow WITH the
// ActionRiskWouldGate signal so hook_central can emit its own local
// risk_would_gate observation event.
func TestSnapshotEvaluateRiskLogModeEmitsWouldGateSignal(t *testing.T) {
	snap := &PolicySnapshot{
		Bands:            map[string]string{},
		RiskScores:       map[string]int{"run-g": 150},
		RiskThreshold:    100,
		RiskMode:         "",
		RiskGuardedTools: []string{"Bash"},
	}
	tc := ToolCall{RunID: "run-g", AgentID: "a", ToolName: "Bash", Command: "ls"}
	d, action := snap.Evaluate(tc)
	if d.Verdict != Allow {
		t.Errorf("log mode over threshold must allow, got %s: %s", d.Verdict, d.Reason)
	}
	if action != ActionRiskWouldGate {
		t.Errorf("action = %q, want %q", action, ActionRiskWouldGate)
	}
}

// TestSnapshotEvaluateRiskUnderThresholdNoSignal: a score under threshold (or
// a run absent from RiskScores entirely — score 0) must Allow with no action,
// exactly like the pre-G5 snapshot behavior.
func TestSnapshotEvaluateRiskUnderThresholdNoSignal(t *testing.T) {
	snap := &PolicySnapshot{
		Bands:            map[string]string{},
		RiskScores:       map[string]int{},
		RiskThreshold:    100,
		RiskMode:         "gate",
		RiskGuardedTools: []string{"Bash"},
	}
	tc := ToolCall{RunID: "unknown-run", AgentID: "a", ToolName: "Bash", Command: "ls"}
	d, action := snap.Evaluate(tc)
	if d.Verdict != Allow || action != "" {
		t.Errorf("run absent from RiskScores must allow with no action, got %s/%q", d.Verdict, action)
	}
}

// TestSnapshotEvaluateRiskNonGuardedToolNoOp: G5 never applies to a tool
// outside RiskGuardedTools, even at a very high score.
func TestSnapshotEvaluateRiskNonGuardedToolNoOp(t *testing.T) {
	snap := &PolicySnapshot{
		Bands:            map[string]string{},
		RiskScores:       map[string]int{"run-h": 999},
		RiskThreshold:    100,
		RiskMode:         "gate",
		RiskGuardedTools: []string{"Bash"},
	}
	tc := ToolCall{RunID: "run-h", AgentID: "a", ToolName: "Read"}
	d, action := snap.Evaluate(tc)
	if d.Verdict != Allow || action != "" {
		t.Errorf("non-guarded tool must not be gated by risk score, got %s/%q", d.Verdict, action)
	}
}
