package govern

import (
	"context"
	"testing"
)

// The snapshot evaluator is the local (dev-machine) twin of Engine.Evaluate.
// Same inputs must produce the same verdicts — this is the contract that
// makes central-mode pre-tool checks trustworthy.
func TestSnapshotEvaluateParity(t *testing.T) {
	bash := func(cmd string) ToolCall {
		return ToolCall{RunID: "r", AgentID: "a", Project: "p", ToolName: "Bash", Command: cmd}
	}
	snap := &PolicySnapshot{
		Rules: []SnapshotRule{
			{ID: 1, Kind: "block", Pattern: `rm -rf /`, Description: "no nukes", ScopeType: "global"},
			{ID: 2, Kind: "gate", Pattern: `git push`, Description: "pushes need approval", ScopeType: "global"},
			{ID: 3, Kind: "block", Pattern: `drop table`, ScopeType: "agent", ScopeID: "other"},
		},
		Bands: map[string]string{},
	}
	cases := []struct {
		name string
		tc   ToolCall
		want Verdict
	}{
		{"clean command allows", bash("go test ./..."), Allow},
		{"block rule denies", bash("rm -rf / --no-preserve-root"), Deny},
		{"gate rule asks", bash("git push origin main"), Ask},
		{"out-of-scope rule ignored", bash("DROP TABLE users"), Allow},
		{"read tool allows", ToolCall{RunID: "r", AgentID: "a", ToolName: "Read"}, Allow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if d := snap.Evaluate(c.tc); d.Verdict != c.want {
				t.Errorf("verdict %s (%s), want %s", d.Verdict, d.Reason, c.want)
			}
		})
	}
}

func TestSnapshotKillAndBands(t *testing.T) {
	tc := ToolCall{RunID: "run-1", AgentID: "a", ToolName: "Bash", Command: "ls"}

	kill := &PolicySnapshot{KillGlobal: true}
	if d := kill.Evaluate(tc); d.Verdict != Deny {
		t.Errorf("global kill: %s", d.Verdict)
	}
	killed := &PolicySnapshot{KilledRuns: []string{"run-1"}}
	if d := killed.Evaluate(tc); d.Verdict != Deny {
		t.Errorf("killed run: %s", d.Verdict)
	}
	if d := killed.Evaluate(ToolCall{RunID: "other", ToolName: "Bash", Command: "ls"}); d.Verdict != Allow {
		t.Errorf("other run hit by kill list: %s", d.Verdict)
	}

	supervised := &PolicySnapshot{Bands: map[string]string{"a": BandSupervised}}
	if d := supervised.Evaluate(tc); d.Verdict != Ask {
		t.Errorf("supervised Bash: %s", d.Verdict)
	}
	if d := supervised.Evaluate(ToolCall{RunID: "r", AgentID: "a", ToolName: "Read"}); d.Verdict != Allow {
		t.Errorf("supervised Read: %s", d.Verdict)
	}

	trusted := &PolicySnapshot{
		Bands: map[string]string{"a": BandTrusted},
		Rules: []SnapshotRule{
			{ID: 1, Kind: "gate", Pattern: `git push`, ScopeType: "global"},
			{ID: 2, Kind: "gate", Pattern: `deploy`, Critical: true, ScopeType: "global"},
		},
	}
	if d := trusted.Evaluate(ToolCall{RunID: "r", AgentID: "a", ToolName: "Bash", Command: "git push"}); d.Verdict != Allow {
		t.Errorf("trusted skips normal gate: %s", d.Verdict)
	}
	if d := trusted.Evaluate(ToolCall{RunID: "r", AgentID: "a", ToolName: "Bash", Command: "deploy prod"}); d.Verdict != Ask {
		t.Errorf("critical gates trusted too: %s", d.Verdict)
	}
}

func TestSnapshotBudgetExceededBlocksMutations(t *testing.T) {
	snap := &PolicySnapshot{BudgetExceeded: true}
	if d := snap.Evaluate(ToolCall{ToolName: "Bash", Command: "ls"}); d.Verdict != Deny {
		t.Errorf("budget-exceeded Bash: %s", d.Verdict)
	}
	if d := snap.Evaluate(ToolCall{ToolName: "Read"}); d.Verdict != Allow {
		t.Errorf("budget-exceeded Read must still allow: %s", d.Verdict)
	}
}

// Bad patterns fail CLOSED, exactly like the engine's loadRules error path.
func TestSnapshotBadPatternFailsClosed(t *testing.T) {
	snap := &PolicySnapshot{Rules: []SnapshotRule{{ID: 1, Kind: "block", Pattern: `([`, ScopeType: "global"}}}
	d := snap.Evaluate(ToolCall{ToolName: "Bash", Command: "anything"})
	if d.Verdict != Deny {
		t.Errorf("bad pattern: %s, want deny", d.Verdict)
	}
}

// BuildPolicySnapshot pulls real state so the wire snapshot mirrors the DB.
func TestBuildPolicySnapshot(t *testing.T) {
	e := testEngine(t)
	e.St.SetSetting("kill_switch_global", "1")
	seedRun(t, e, "kr", 0)
	e.St.DB.Exec(`UPDATE runs SET status='killed' WHERE id='kr'`)
	e.St.DB.Exec(`INSERT INTO guardrail_rules(kind, pattern, description, enabled, critical, scope_type, scope_id)
		VALUES('block', 'rm -rf', 'nope', 1, 0, 'global', '')`)
	e.St.DB.Exec(`INSERT INTO agent_bands(agent_id, band, updated_at, updated_by) VALUES('a1','trusted', ?, 'test')`, "now")

	snap, err := BuildPolicySnapshot(e.St, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.KillGlobal {
		t.Error("kill_global not reflected")
	}
	found := false
	for _, id := range snap.KilledRuns {
		found = found || id == "kr"
	}
	if !found {
		t.Error("killed run missing from snapshot")
	}
	// Seed migrations ship default rules; the one added above must be present.
	var added bool
	for _, r := range snap.Rules {
		added = added || r.Pattern == "rm -rf"
	}
	if !added {
		t.Errorf("added rule missing from snapshot: %+v", snap.Rules)
	}
	if snap.Bands["a1"] != "trusted" {
		t.Errorf("bands: %+v", snap.Bands)
	}
	// Parity spot-check: engine and snapshot deny the same call.
	tc := ToolCall{RunID: "x", AgentID: "a1", ToolName: "Bash", Command: "ls"}
	if d := snap.Evaluate(tc); d.Verdict != Deny {
		t.Errorf("snapshot kill verdict: %s", d.Verdict)
	}
	if d := NewEngine(e.Cfg, e.St).Evaluate(context.Background(), tc); d.Verdict != Deny {
		t.Errorf("engine kill verdict: %s", d.Verdict)
	}
}
