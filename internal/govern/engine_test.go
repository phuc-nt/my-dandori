package govern

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

func testEngine(t testing.TB) *Engine {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "no.yaml"))
	cfg.GateWaitSeconds = 0 // gates time out immediately in tests
	return NewEngine(cfg, st)
}

func seedRun(t testing.TB, e *Engine, id string, cost float64) {
	t.Helper()
	_, err := e.St.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a1','a1',?)
		ON CONFLICT(name) DO NOTHING`, store.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.St.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, cwd, started_at, cost_usd)
		VALUES(?, ?, 'a1', 'proj', '/work/proj', ?, ?)`, id, id, store.Now(), cost)
	if err != nil {
		t.Fatal(err)
	}
}

func bashCall(runID, command string) ToolCall {
	input, _ := json.Marshal(map[string]string{"command": command})
	return ExtractToolCall(runID, "a1", "proj", "/work/proj", "Bash", input)
}

func TestBlockRules(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "r1", 0)
	cases := []struct {
		command string
		want    Verdict
	}{
		{"ls -la", Allow},
		{"rm -rf /", Deny},
		{"rm -rf ~/work", Deny},
		{"rm -fr /etc", Deny},
		{"cat .env", Deny},
		{"cat ../other/.env.production", Deny},
		{"git push --force origin main", Deny},
		{"echo DROP TABLE users", Deny},
		{"go test ./...", Allow},
	}
	for _, c := range cases {
		d := e.Evaluate(context.Background(), bashCall("r1", c.command))
		if d.Verdict != c.want {
			t.Errorf("%q → %s (%s), want %s", c.command, d.Verdict, d.Reason, c.want)
		}
	}
}

// TestBlockRuleProtectsDandoriHome proves an agent cannot self-disarm the
// guardrail engine by editing or deleting its own config/db under
// ~/.dandori/ — the seeded rule must deny both the config-tamper and the
// db-delete attack, on every home-path spelling an agent might try, while
// leaving unrelated home-dir commands untouched.
func TestBlockRuleProtectsDandoriHome(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "r1", 0)
	cases := []struct {
		command string
		want    Verdict
	}{
		{"echo secrets_guard_enabled:false >> ~/.dandori/config.yaml", Deny},
		{"rm ~/.dandori/dandori.db", Deny},
		{"rm -rf ~/.dandori", Deny},
		{"cat $HOME/.dandori/config.yaml", Deny},
		{"cat /Users/someone/.dandori/config.yaml", Deny},
		{"cat /home/someone/.dandori/config.yaml", Deny},
		{"cat ~/.dandori-backup/notes.txt", Allow},
		{"ls ~/projects", Allow},
	}
	for _, c := range cases {
		d := e.Evaluate(context.Background(), bashCall("r1", c.command))
		if d.Verdict != c.want {
			t.Errorf("%q → %s (%s), want %s", c.command, d.Verdict, d.Reason, c.want)
		}
	}
}

func TestSandboxWriteOutsideCwd(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "r2", 0)
	input, _ := json.Marshal(map[string]string{"file_path": "/etc/passwd"})
	tc := ExtractToolCall("r2", "a1", "proj", "/work/proj", "Write", input)
	if d := e.Evaluate(context.Background(), tc); d.Verdict != Deny {
		t.Errorf("write outside cwd must deny, got %s", d.Verdict)
	}
	input, _ = json.Marshal(map[string]string{"file_path": "/work/proj/main.go"})
	tc = ExtractToolCall("r2", "a1", "proj", "/work/proj", "Write", input)
	if d := e.Evaluate(context.Background(), tc); d.Verdict != Allow {
		t.Errorf("write inside cwd must allow, got %s: %s", d.Verdict, d.Reason)
	}
	input, _ = json.Marshal(map[string]string{"file_path": "/tmp/scratch.txt"})
	tc = ExtractToolCall("r2", "a1", "proj", "/work/proj", "Write", input)
	if d := e.Evaluate(context.Background(), tc); d.Verdict != Allow {
		t.Errorf("allowlisted /tmp must allow, got %s: %s", d.Verdict, d.Reason)
	}

	// Harness memory dir under ~/.claude/projects/ is writable from any run…
	input, _ = json.Marshal(map[string]string{"file_path": "~/.claude/projects/-work-proj/memory/note.md"})
	tc = ExtractToolCall("r2", "a1", "proj", "/work/proj", "Write", input)
	if d := e.Evaluate(context.Background(), tc); d.Verdict != Allow {
		t.Errorf("memory dir must allow, got %s: %s", d.Verdict, d.Reason)
	}
	// …but the rest of ~/.claude (settings.json, hooks/) is the guardrail
	// wiring itself and stays out of scope, including via ../ traversal.
	for _, p := range []string{"~/.claude/settings.json", "~/.claude/hooks/x.cjs",
		"~/.claude/projects/../settings.json"} {
		input, _ = json.Marshal(map[string]string{"file_path": p})
		tc = ExtractToolCall("r2", "a1", "proj", "/work/proj", "Write", input)
		if d := e.Evaluate(context.Background(), tc); d.Verdict != Deny {
			t.Errorf("%s must stay denied, got %s", p, d.Verdict)
		}
	}
}

func TestBudgetBreaker(t *testing.T) {
	e := testEngine(t)
	e.Cfg.Budget.GlobalMonthlyUSD = 10
	seedRun(t, e, "r3", 11) // over the $10 global budget
	e.St.DB.Exec(`UPDATE runs SET model = 'claude-opus-4-8' WHERE id = 'r3'`)
	d := e.Evaluate(context.Background(), bashCall("r3", "ls"))
	if d.Verdict != Deny {
		t.Fatalf("over budget on an expensive model must deny, got %s", d.Verdict)
	}

	// Warn events: fresh engine under budget crosses 75% once.
	e2 := testEngine(t)
	e2.Cfg.Budget.GlobalMonthlyUSD = 10
	seedRun(t, e2, "r4", 8)
	e2.Evaluate(context.Background(), bashCall("r4", "ls"))
	e2.Evaluate(context.Background(), bashCall("r4", "ls"))
	var warns int
	e2.St.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='budget_warn'`).Scan(&warns)
	if warns != 2 { // 50% and 75% crossed once each, no duplicates
		t.Errorf("budget_warn events: %d, want 2", warns)
	}
}

func TestKillSwitch(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "r5", 0)
	if err := SetGlobalKill(e.St, true, "tester", "test"); err != nil {
		t.Fatal(err)
	}
	if d := e.Evaluate(context.Background(), bashCall("r5", "ls")); d.Verdict != Deny {
		t.Errorf("global kill must deny, got %s", d.Verdict)
	}
	SetGlobalKill(e.St, false, "tester", "test")
	if d := e.Evaluate(context.Background(), bashCall("r5", "ls")); d.Verdict != Allow {
		t.Errorf("after lift must allow, got %s", d.Verdict)
	}
	if err := KillRun(e.St, "r5", "tester", "loop"); err != nil {
		t.Fatal(err)
	}
	if d := e.Evaluate(context.Background(), bashCall("r5", "ls")); d.Verdict != Deny {
		t.Errorf("killed run must deny, got %s", d.Verdict)
	}
}

func TestGateTimeoutAndApprove(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "r6", 0)
	// Timeout path (GateWaitSeconds=0): pending → deny with approval id.
	d := e.Evaluate(context.Background(), bashCall("r6", "git push origin main"))
	if d.Verdict != Deny {
		t.Fatalf("gate timeout must deny, got %s (%s)", d.Verdict, d.Reason)
	}
	var id int64
	e.St.DB.QueryRow(`SELECT id FROM approvals WHERE status='pending' ORDER BY id DESC LIMIT 1`).Scan(&id)
	if id == 0 {
		t.Fatal("approval row missing")
	}
	// Pre-approve then re-evaluate: the new approval created for the retry is
	// separate; simulate the operator approving DURING the wait instead.
	won, err := Decide(e.St, id, true, "phucnt", "looks fine")
	if err != nil || !won {
		t.Fatalf("decide: won=%v err=%v", won, err)
	}
	var status string
	e.St.DB.QueryRow(`SELECT status FROM approvals WHERE id=?`, id).Scan(&status)
	if status != "approved" {
		t.Errorf("status: %s", status)
	}
	// Double-decide must lose.
	if won, _ := Decide(e.St, id, false, "other", "late"); won {
		t.Error("second decision must not win")
	}
}

func TestAuditChainVerify(t *testing.T) {
	e := testEngine(t)
	a := &Audit{St: e.St, Actor: "tester"}
	for i := 0; i < 3; i++ {
		if _, err := a.Append("act", "subj", "detail"); err != nil {
			t.Fatal(err)
		}
	}
	if broken, _ := Verify(e.St); broken != 0 {
		t.Fatalf("fresh chain must verify, broken at %d", broken)
	}
	// Tamper with entry 2 → chain must break there.
	e.St.DB.Exec(`UPDATE audit_log SET detail = 'forged' WHERE id = 2`)
	if broken, _ := Verify(e.St); broken != 2 {
		t.Errorf("tampered chain: broken at %d, want 2", broken)
	}
}
