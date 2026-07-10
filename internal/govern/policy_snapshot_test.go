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
		name       string
		tc         ToolCall
		want       Verdict
		wantAction string // "" also matches Allow
	}{
		{"clean command allows", bash("go test ./..."), Allow, ""},
		{"block rule denies", bash("rm -rf / --no-preserve-root"), Deny, ActionGuardrailBlock},
		{"gate rule asks", bash("git push origin main"), Ask, ActionPermissionAsk},
		{"out-of-scope rule ignored", bash("DROP TABLE users"), Allow, ""},
		{"read tool allows", ToolCall{RunID: "r", AgentID: "a", ToolName: "Read"}, Allow, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, action := snap.Evaluate(c.tc)
			if d.Verdict != c.want {
				t.Errorf("verdict %s (%s), want %s", d.Verdict, d.Reason, c.want)
			}
			if action != c.wantAction {
				t.Errorf("action %q, want %q", action, c.wantAction)
			}
		})
	}
}

func TestSnapshotKillAndBands(t *testing.T) {
	tc := ToolCall{RunID: "run-1", AgentID: "a", ToolName: "Bash", Command: "ls"}

	kill := &PolicySnapshot{KillGlobal: true}
	if d, action := kill.Evaluate(tc); d.Verdict != Deny {
		t.Errorf("global kill: %s", d.Verdict)
	} else if action != ActionKillBlock {
		t.Errorf("global kill action = %q, want %q", action, ActionKillBlock)
	}
	killed := &PolicySnapshot{KilledRuns: []string{"run-1"}}
	if d, action := killed.Evaluate(tc); d.Verdict != Deny {
		t.Errorf("killed run: %s", d.Verdict)
	} else if action != ActionKillBlock {
		t.Errorf("killed run action = %q, want %q", action, ActionKillBlock)
	}
	if d, _ := killed.Evaluate(ToolCall{RunID: "other", ToolName: "Bash", Command: "ls"}); d.Verdict != Allow {
		t.Errorf("other run hit by kill list: %s", d.Verdict)
	}

	supervised := &PolicySnapshot{Bands: map[string]string{"a": BandSupervised}}
	if d, action := supervised.Evaluate(tc); d.Verdict != Ask {
		t.Errorf("supervised Bash: %s", d.Verdict)
	} else if action != ActionPermissionAsk {
		t.Errorf("supervised Bash action = %q, want %q", action, ActionPermissionAsk)
	}
	if d, _ := supervised.Evaluate(ToolCall{RunID: "r", AgentID: "a", ToolName: "Read"}); d.Verdict != Allow {
		t.Errorf("supervised Read: %s", d.Verdict)
	}

	trusted := &PolicySnapshot{
		Bands: map[string]string{"a": BandTrusted},
		Rules: []SnapshotRule{
			{ID: 1, Kind: "gate", Pattern: `git push`, ScopeType: "global"},
			{ID: 2, Kind: "gate", Pattern: `deploy`, Critical: true, ScopeType: "global"},
		},
	}
	if d, _ := trusted.Evaluate(ToolCall{RunID: "r", AgentID: "a", ToolName: "Bash", Command: "git push"}); d.Verdict != Allow {
		t.Errorf("trusted skips normal gate: %s", d.Verdict)
	}
	if d, _ := trusted.Evaluate(ToolCall{RunID: "r", AgentID: "a", ToolName: "Bash", Command: "deploy prod"}); d.Verdict != Ask {
		t.Errorf("critical gates trusted too: %s", d.Verdict)
	}
}

// TestSnapshotBudgetExceededAsksInDowngradeMode: central mode has no per-run
// model to downgrade against (see Evaluate's budget-branch doc comment), so
// the default ("" / "downgrade") mode escalates to Ask rather than Deny or
// silently downgrading.
func TestSnapshotBudgetExceededAsksInDowngradeMode(t *testing.T) {
	snap := &PolicySnapshot{BudgetExceeded: true}
	if d, action := snap.Evaluate(ToolCall{ToolName: "Bash", Command: "ls"}); d.Verdict != Ask {
		t.Errorf("budget-exceeded Bash, downgrade mode: %s, want Ask", d.Verdict)
	} else if action != ActionPermissionAsk {
		t.Errorf("action = %q, want %q", action, ActionPermissionAsk)
	}
	if d, _ := snap.Evaluate(ToolCall{ToolName: "Read"}); d.Verdict != Allow {
		t.Errorf("budget-exceeded Read must still allow: %s", d.Verdict)
	}
}

// TestSnapshotBudgetExceededHardModeDenies preserves the pre-v14 hard-stop
// for operators who explicitly opt into budget.mode: hard.
func TestSnapshotBudgetExceededHardModeDenies(t *testing.T) {
	snap := &PolicySnapshot{BudgetExceeded: true, BudgetMode: "hard"}
	if d, action := snap.Evaluate(ToolCall{ToolName: "Bash", Command: "ls"}); d.Verdict != Deny {
		t.Errorf("budget-exceeded Bash, hard mode: %s, want Deny", d.Verdict)
	} else if action != ActionBudgetBlock {
		t.Errorf("action = %q, want %q", action, ActionBudgetBlock)
	}
	if d, _ := snap.Evaluate(ToolCall{ToolName: "Read"}); d.Verdict != Allow {
		t.Errorf("budget-exceeded Read must still allow: %s", d.Verdict)
	}
}

// TestSnapshotBudgetUnderLimitAllows: no scope over budget must never gate.
func TestSnapshotBudgetUnderLimitAllows(t *testing.T) {
	snap := &PolicySnapshot{}
	if d, action := snap.Evaluate(ToolCall{ToolName: "Bash", Command: "ls"}); d.Verdict != Allow {
		t.Errorf("under budget: %s, want Allow", d.Verdict)
	} else if action != "" {
		t.Errorf("action = %q, want empty", action)
	}
}

// TestSnapshotBudgetExceededPerAgentScope proves per-scope budget: an
// agent-scoped over-limit gates a call under that agent even when the global
// budget itself is fine, and leaves an unrelated agent's calls untouched.
func TestSnapshotBudgetExceededPerAgentScope(t *testing.T) {
	snap := &PolicySnapshot{BudgetExceededAgents: []string{"over-agent"}}
	if d, action := snap.Evaluate(ToolCall{AgentID: "over-agent", ToolName: "Bash", Command: "ls"}); d.Verdict != Ask {
		t.Errorf("agent-scoped over budget: %s, want Ask", d.Verdict)
	} else if action != ActionPermissionAsk {
		t.Errorf("action = %q, want %q", action, ActionPermissionAsk)
	}
	if d, _ := snap.Evaluate(ToolCall{AgentID: "fine-agent", ToolName: "Bash", Command: "ls"}); d.Verdict != Allow {
		t.Errorf("unrelated agent must not be gated by another agent's budget: %s", d.Verdict)
	}

	hardSnap := &PolicySnapshot{BudgetExceededAgents: []string{"over-agent"}, BudgetMode: "hard"}
	if d, _ := hardSnap.Evaluate(ToolCall{AgentID: "over-agent", ToolName: "Bash", Command: "ls"}); d.Verdict != Deny {
		t.Errorf("agent-scoped over budget, hard mode: %s, want Deny", d.Verdict)
	}
}

// Bad patterns fail CLOSED, exactly like the engine's loadRules error path.
func TestSnapshotBadPatternFailsClosed(t *testing.T) {
	snap := &PolicySnapshot{Rules: []SnapshotRule{{ID: 1, Kind: "block", Pattern: `([`, ScopeType: "global"}}}
	d, _ := snap.Evaluate(ToolCall{ToolName: "Bash", Command: "anything"})
	if d.Verdict != Deny {
		t.Errorf("bad pattern: %s, want deny", d.Verdict)
	}
}

// TestSnapshotSecretDeny proves the central-mode snapshot denies strict
// secrets without any DB access (regex-only half of G1.5), while a
// PII-bearing call is NOT gated centrally — PII-gate needs findOrCreateApproval
// (a DB row + wait), which is local-mode only; central falls through to
// whatever gate/band rule would otherwise apply.
func TestSnapshotSecretDeny(t *testing.T) {
	snap := &PolicySnapshot{Bands: map[string]string{}}
	secretCall := ToolCall{RunID: "r", AgentID: "a", ToolName: "Bash", Command: "export KEY=AKIAIOSFODNN7EXAMPLE"}
	if d, action := snap.Evaluate(secretCall); d.Verdict != Deny {
		t.Errorf("snapshot secret-Deny: %s, want Deny", d.Verdict)
	} else if action != ActionSecretsBlock {
		t.Errorf("action = %q, want %q", action, ActionSecretsBlock)
	}

	piiCall := ToolCall{RunID: "r", AgentID: "a", ToolName: "Write", Content: "card 4111111111111111"}
	if d, _ := snap.Evaluate(piiCall); d.Verdict == Deny {
		t.Errorf("PII must not be denied centrally (local-only gate), got Deny: %s", d.Reason)
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

	e.Cfg.Budget.GlobalMonthlyUSD = 50
	snap, err := BuildPolicySnapshot(e.St, e.Cfg.Budget, "", e.Cfg.RiskScore)
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
	if d, _ := snap.Evaluate(tc); d.Verdict != Deny {
		t.Errorf("snapshot kill verdict: %s", d.Verdict)
	}
	if d := NewEngine(e.Cfg, e.St).Evaluate(context.Background(), tc); d.Verdict != Deny {
		t.Errorf("engine kill verdict: %s", d.Verdict)
	}
}

// TestBuildPolicySnapshotPerScopeBudget is THE per-scope test: an agent-scoped
// budget over its monthly limit must surface in BudgetExceededAgents even
// when the global budget is untouched — and an unrelated agent/project must
// not be flagged. Uses seedCentralRun (status='running', has agent_id/project)
// so populateBudgetExceeded's active-run scope query has something to find.
func TestBuildPolicySnapshotPerScopeBudget(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 0 // global budget off; only the agent scope is set below
	seedCentralRun(t, e, "run-over", "operator-x", 0)
	e.St.DB.Exec(`UPDATE runs SET cost_usd = 100 WHERE id = 'run-over'`)
	e.St.DB.Exec(`INSERT INTO budgets(scope_type, scope_id, limit_usd) VALUES('agent', 'a1', 50)`)

	snap, err := BuildPolicySnapshot(e.St, e.Cfg.Budget, "operator-x", e.Cfg.RiskScore)
	if err != nil {
		t.Fatal(err)
	}
	if snap.BudgetExceeded {
		t.Error("global budget must not be exceeded (unset)")
	}
	found := false
	for _, a := range snap.BudgetExceededAgents {
		found = found || a == "a1"
	}
	if !found {
		t.Errorf("agent a1 must be in BudgetExceededAgents: %+v", snap.BudgetExceededAgents)
	}
	if len(snap.BudgetExceededProjects) != 0 {
		t.Errorf("no project budget was set, must stay empty: %+v", snap.BudgetExceededProjects)
	}

	overCall := ToolCall{RunID: "run-over", AgentID: "a1", ToolName: "Bash", Command: "ls"}
	if d, _ := snap.Evaluate(overCall); d.Verdict != Ask {
		t.Errorf("agent a1 over its own budget must Ask (default mode): %s", d.Verdict)
	}
	otherAgentCall := ToolCall{RunID: "run-over", AgentID: "a2", ToolName: "Bash", Command: "ls"}
	if d, _ := snap.Evaluate(otherAgentCall); d.Verdict != Allow {
		t.Errorf("unrelated agent a2 must not be gated by agent a1's budget: %s", d.Verdict)
	}
}
