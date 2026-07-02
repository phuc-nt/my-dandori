package learn

import (
	"strconv"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedBehaviorRun creates a run with the numeric events P2 reads. Inputs are
// exactly what central mode stores — no transcript exists anywhere here.
func seedBehaviorRun(t *testing.T, st *store.Store, runID, operator, agent, status string, steering int, proxy string, toolErrs int) {
	t.Helper()
	st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?, ?, ?) ON CONFLICT(name) DO NOTHING`,
		agent, agent, store.Now())
	if operator != "" {
		if _, err := st.ResolveOperator(operator); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, status, started_at, ended_at, cost_usd, input_tokens, operator_id, lines_added)
		VALUES(?, ?, ?, 'proj', ?, ?, ?, 1.0, 400000, ?, 100)`,
		runID, runID, agent, status, store.Now(), store.Now(), nullIf(operator)); err != nil {
		t.Fatal(err)
	}
	st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, ?, 'user_msg', ?)`, runID, store.Now(), itoa(steering))
	st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, ?, 'prompt_proxy', ?)`, runID, store.Now(), proxy)
	for i := 0; i < toolErrs; i++ {
		st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload) VALUES(?, ?, 'tool_result', 'Edit', 0, 'err')`,
			runID, store.Now())
	}
	st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload) VALUES(?, ?, 'permission_ask', 'Bash', 0, '')`,
		runID, store.Now())
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestComputeBehavior(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "r1", "alice@mac", "agent-a", "done", 2, `{"w":120,"spec":3}`, 4)

	b, err := ComputeBehavior(st, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Steering != 2 {
		t.Errorf("steering: %d", b.Steering)
	}
	if b.PromptWords != 120 || b.PromptBand != "good" || b.PromptSpec != 3 {
		t.Errorf("prompt proxy: %dw band=%s spec=%d", b.PromptWords, b.PromptBand, b.PromptSpec)
	}
	if b.ToolErrors != 4 || b.RetryLoops != 1 {
		t.Errorf("errors=%d loops=%d, want 4/1 (streak of 4 = one ≥3 loop)", b.ToolErrors, b.RetryLoops)
	}
	if b.PermAsks != 1 {
		t.Errorf("perm asks: %d", b.PermAsks)
	}
	if b.TaskSize != "M" {
		t.Errorf("task size: %s", b.TaskSize)
	}
	if b.Abandoned {
		t.Error("finished run flagged abandoned")
	}
	if !strings.Contains(b.Formula, "proxy") {
		t.Errorf("formula must self-declare as proxy: %q", b.Formula)
	}
	if len(b.EventIDs) == 0 {
		t.Error("no drillable source event ids")
	}
}

func TestComputeBehaviorPromptBands(t *testing.T) {
	st := testStore(t)
	cases := []struct {
		run, proxy, want string
	}{
		{"b1", `{"w":5,"spec":0}`, "short"},
		{"b2", `{"w":100,"spec":1}`, "good"},
		{"b3", `{"w":900,"spec":7}`, "long"},
		{"b4", ``, "unknown"},
	}
	for _, c := range cases {
		seedBehaviorRun(t, st, c.run, "", "agent-a", "done", 0, c.proxy, 0)
		b, err := ComputeBehavior(st, c.run)
		if err != nil {
			t.Fatal(err)
		}
		if b.PromptBand != c.want {
			t.Errorf("%s: band %s, want %s", c.run, b.PromptBand, c.want)
		}
	}
}

func TestOperatorAggregateIsPrivateAndScoped(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "o1", "alice@mac", "agent-a", "done", 3, `{"w":50,"spec":1}`, 2)
	seedBehaviorRun(t, st, "o2", "alice@mac", "agent-a", "failed", 1, `{"w":50,"spec":1}`, 0)
	seedBehaviorRun(t, st, "x1", "bob@dev", "agent-a", "done", 9, `{"w":10,"spec":0}`, 9)

	ob, err := OperatorBehaviorAgg(st, "alice@mac", 30)
	if err != nil {
		t.Fatal(err)
	}
	if ob.Runs != 2 {
		t.Fatalf("alice runs: %d (bob leaked in?)", ob.Runs)
	}
	if ob.SteeringAvg != 2 { // (3+1)/2
		t.Errorf("steering avg: %v", ob.SteeringAvg)
	}
	if ob.DoneRate != 0.5 {
		t.Errorf("done rate: %v", ob.DoneRate)
	}
	if !ob.Private || OperatorCaveat == "" {
		t.Error("operator rollup must be marked private with a caveat")
	}
}

func TestTeamCompareUnionsMembers(t *testing.T) {
	st := testStore(t)
	// Team A: operator alice + agent bot-a. Team B: agent bot-b only.
	seedBehaviorRun(t, st, "t1", "alice@mac", "bot-x", "done", 1, `{"w":50,"spec":1}`, 0) // alice drives a NON-member agent
	seedBehaviorRun(t, st, "t2", "carol@dev", "bot-a", "done", 0, `{"w":50,"spec":1}`, 1) // member agent, non-member operator
	seedBehaviorRun(t, st, "t3", "dave@dev", "bot-b", "failed", 0, `{"w":50,"spec":1}`, 0)

	a, _ := st.CreateTeam("Alpha")
	b, _ := st.CreateTeam("Beta")
	if err := st.AssignMember(a, "operator", "alice@mac"); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignMember(a, "agent", "bot-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignMember(b, "agent", "bot-b"); err != nil {
		t.Fatal(err)
	}

	cmp, err := TeamCompare(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmp) != 2 {
		t.Fatalf("teams: %d", len(cmp))
	}
	byName := map[string]TeamBehavior{}
	for _, tb := range cmp {
		byName[tb.Name] = tb
	}
	// Alpha = union: alice's run (t1) + bot-a's run (t2) = 2 runs.
	if byName["Alpha"].Runs != 2 {
		t.Errorf("Alpha runs: %d, want union of operator+agent runs = 2", byName["Alpha"].Runs)
	}
	if byName["Beta"].Runs != 1 || byName["Beta"].DoneRate != 0 {
		t.Errorf("Beta: %+v", byName["Beta"])
	}
	if byName["Alpha"].Operators != 1 || byName["Alpha"].Agents != 1 {
		t.Errorf("Alpha member counts: %+v", byName["Alpha"])
	}
}
