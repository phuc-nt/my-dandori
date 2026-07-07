package learn

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedSkillRun inserts an ended run in the given project/status and, if
// skill != "", a tool_use/Skill event with the EXACT verified payload shape
// {"skill":"<name>","args":"..."} (phase-02 spec: payload shape VERIFIED on
// fleet DB, tests must seed the same shape).
func seedSkillRun(t *testing.T, st *store.Store, id, project, status, skill string) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO runs(id, session_id, project, status, started_at, ended_at, cost_usd)
		VALUES(?,?,?,?,datetime('now'),datetime('now'),1.0)`, id, id, project, status); err != nil {
		t.Fatal(err)
	}
	if skill == "" {
		return
	}
	payload := fmt.Sprintf(`{"skill":"%s","args":"do the thing"}`, skill)
	if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, payload)
		VALUES(?, datetime('now'), 'tool_use', 'Skill', ?)`, id, payload); err != nil {
		t.Fatal(err)
	}
}

// withLocalSkillFile writes .claude/skills/<name>/SKILL.md under the current
// working directory for the duration of the test (detectSkillUsage/
// detectToolPattern read the repo-local skill file to obtain a real body —
// see localSkillBody) and removes it in cleanup.
func withLocalSkillFile(t *testing.T, name, content string) {
	t.Helper()
	dir := filepath.Join(".claude", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.RemoveAll(filepath.Join(".claude", "skills", name))
	})
}

// TestDetectSkillUsageEmptyDB: no events at all → honest empty result, no
// error.
func TestDetectSkillUsageEmptyDB(t *testing.T) {
	st := insightTestStore(t)
	out, err := detectSkillUsage(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("empty DB should yield 0 candidates, got %+v", out)
	}
}

// TestDetectSkillUsageBelowMinSample: fewer than MinSampleForKnowledge on one
// side must not nominate, even with a stark done-rate difference.
func TestDetectSkillUsageBelowMinSample(t *testing.T) {
	st := insightTestStore(t)
	withLocalSkillFile(t, "cook-plan", "# Cook Plan\nsteps...")

	// present: 5 runs (< 10), all done
	for i := 0; i < 5; i++ {
		seedSkillRun(t, st, fmt.Sprintf("p%d", i), "proj-a", "done", "cook-plan")
	}
	// absent: 15 runs, all failed
	for i := 0; i < 15; i++ {
		seedSkillRun(t, st, fmt.Sprintf("a%d", i), "proj-a", "failed", "")
	}

	out, err := detectSkillUsage(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("present n=5 < MinSampleForKnowledge=10, want 0 nominates, got %+v", out)
	}
}

// TestDetectSkillUsageCIOverlapNoNominate: both sides meet MinSample but
// done-rates are close enough that Wilson CIs overlap → no nominate (honest
// "chưa kết luận").
func TestDetectSkillUsageCIOverlapNoNominate(t *testing.T) {
	st := insightTestStore(t)
	withLocalSkillFile(t, "cook-plan", "# Cook Plan\nsteps...")

	// present: 10 runs, 6 done (60%)
	for i := 0; i < 10; i++ {
		status := "failed"
		if i < 6 {
			status = "done"
		}
		seedSkillRun(t, st, fmt.Sprintf("p%d", i), "proj-a", status, "cook-plan")
	}
	// absent: 10 runs, 5 done (50%) — close enough to overlap
	for i := 0; i < 10; i++ {
		status := "failed"
		if i < 5 {
			status = "done"
		}
		seedSkillRun(t, st, fmt.Sprintf("a%d", i), "proj-a", status, "")
	}

	out, err := detectSkillUsage(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("60%% vs 50%% done-rate at n=10 should have overlapping Wilson CIs, want 0 nominates, got %+v", out)
	}
}

// TestDetectSkillUsageDisjointCINominates: both sides meet MinSample and
// done-rates are far enough apart (100% vs 0%) that CIs are disjoint →
// nominate with correct stats.
func TestDetectSkillUsageDisjointCINominates(t *testing.T) {
	st := insightTestStore(t)
	withLocalSkillFile(t, "cook-plan", "# Cook Plan\nsteps...")

	for i := 0; i < 10; i++ {
		seedSkillRun(t, st, fmt.Sprintf("p%d", i), "proj-a", "done", "cook-plan")
	}
	for i := 0; i < 10; i++ {
		seedSkillRun(t, st, fmt.Sprintf("a%d", i), "proj-a", "failed", "")
	}

	out, err := detectSkillUsage(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("100%% vs 0%% done-rate at n=10 should nominate 1 skill, got %+v", out)
	}
	c := out[0]
	if c.Kind != KindSkill || c.Name != "cook-plan" {
		t.Errorf("nominate kind/name = %s/%s, want skill/cook-plan", c.Kind, c.Name)
	}
	if c.Body == "" {
		t.Error("skill nominate must carry a body (pinned from local SKILL.md)")
	}
	if c.Stats.NPresent != 10 || c.Stats.NAbsent != 10 {
		t.Errorf("stats n: present=%d absent=%d, want 10/10", c.Stats.NPresent, c.Stats.NAbsent)
	}
	if c.Stats.DonePresent != 1.0 || c.Stats.DoneAbsent != 0.0 {
		t.Errorf("stats done-rate: present=%v absent=%v, want 1.0/0.0", c.Stats.DonePresent, c.Stats.DoneAbsent)
	}
	if c.Stats.CIPresentLo <= c.Stats.CIAbsentHi {
		t.Errorf("CIs should be disjoint: present lo=%d absent hi=%d", c.Stats.CIPresentLo, c.Stats.CIAbsentHi)
	}
}

// TestDetectSkillUsageNoLocalBodySkipped: CI is disjoint and n sufficient,
// but no local SKILL.md exists for the name → skip (cannot satisfy the
// body-required contract for kind=skill without fabricating content).
func TestDetectSkillUsageNoLocalBodySkipped(t *testing.T) {
	st := insightTestStore(t)
	// deliberately no withLocalSkillFile call
	for i := 0; i < 10; i++ {
		seedSkillRun(t, st, fmt.Sprintf("p%d", i), "proj-a", "done", "ghost-skill")
	}
	for i := 0; i < 10; i++ {
		seedSkillRun(t, st, fmt.Sprintf("a%d", i), "proj-a", "failed", "")
	}
	out, err := detectSkillUsage(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("no local SKILL.md for ghost-skill, want 0 nominates, got %+v", out)
	}
}

// TestDetectToolPatternNominatesContextKind: a tool prominent in done runs of
// a keyword cluster, both meeting MinSample, nominates kind=context (F16 —
// never a first-class "tool" kind).
func TestDetectToolPatternNominatesContextKind(t *testing.T) {
	st := insightTestStore(t)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("r%d", i)
		if _, err := st.DB.Exec(`INSERT INTO runs(id, session_id, project, task_key, status, started_at, ended_at)
			VALUES(?,?,?,?,?,datetime('now'),datetime('now'))`, id, id, "proj-a", "MIGRATION-123 database schema", "done"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok) VALUES(?, datetime('now'), 'tool_use', 'psql', 1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	out, err := detectToolPattern(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 tool-pattern nominate, got %+v", out)
	}
	c := out[0]
	if c.Kind != KindContext {
		t.Errorf("tool-pattern must nominate kind=context (F16), got %s", c.Kind)
	}
	if c.Body == "" {
		t.Error("tool-pattern nominate must carry a body (new context text, no ref yet)")
	}
	if c.RefID != 0 {
		t.Errorf("tool-pattern nominate should carry no ref_id (no existing context_versions row), got %d", c.RefID)
	}
}

// TestDetectToolPatternBelowMinSampleEmpty: fewer than MinSampleForKnowledge
// occurrences → honest empty, no nominate.
func TestDetectToolPatternBelowMinSampleEmpty(t *testing.T) {
	st := insightTestStore(t)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("r%d", i)
		if _, err := st.DB.Exec(`INSERT INTO runs(id, session_id, project, task_key, status, started_at, ended_at)
			VALUES(?,?,?,?,?,datetime('now'),datetime('now'))`, id, id, "proj-a", "migration task", "done"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok) VALUES(?, datetime('now'), 'tool_use', 'psql', 1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	out, err := detectToolPattern(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("n=3 < MinSampleForKnowledge, want 0 nominates, got %+v", out)
	}
}

func TestDetectToolPatternEmptyDB(t *testing.T) {
	st := insightTestStore(t)
	out, err := detectToolPattern(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("empty DB should yield 0 candidates, got %+v", out)
	}
}

// TestDetectRuleLifecycleScopeUp: a team-scoped rule with high block volume
// where runs mostly still finish clean (done) → nominate scope-up.
func TestDetectRuleLifecycleScopeUp(t *testing.T) {
	st := insightTestStore(t)
	ruleID := seedGuardrailRule(t, st, "no direct db write")
	if _, err := st.DB.Exec(`UPDATE guardrail_rules SET scope_type='team', scope_id='team-1' WHERE id=?`, ruleID); err != nil {
		t.Fatal(err)
	}
	// 12 runs blocked by this rule, 10 finish done, 2 fail.
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("r%d", i)
		status := "failed"
		if i < 10 {
			status = "done"
		}
		seedLedgerRun(t, st, id, status)
		seedGuardrailBlock(t, st, id, fmt.Sprintf("[dandori G1] blocked: no direct db write (rule #%d)", ruleID))
	}
	out, err := detectRuleLifecycle(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range out {
		if c.Name == fmt.Sprintf("rule-%d-scope-up", ruleID) {
			found = true
			if c.Kind != KindRule {
				t.Errorf("scope-up nominate kind = %s, want rule", c.Kind)
			}
			if c.RefID != int64(ruleID) {
				t.Errorf("scope-up ref_id = %d, want %d", c.RefID, ruleID)
			}
		}
	}
	if !found {
		t.Errorf("expected a scope-up nominate for rule %d, got %+v", ruleID, out)
	}
}

// TestDetectRuleLifecycleRetire: a rule whose blocked runs mostly end
// failed/killed → nominate retire.
func TestDetectRuleLifecycleRetire(t *testing.T) {
	st := insightTestStore(t)
	ruleID := seedGuardrailRule(t, st, "overly strict pattern")
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("r%d", i)
		status := "done"
		if i < 10 {
			status = "failed"
		}
		seedLedgerRun(t, st, id, status)
		seedGuardrailBlock(t, st, id, fmt.Sprintf("[dandori G1] blocked: overly strict pattern (rule #%d)", ruleID))
	}
	out, err := detectRuleLifecycle(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range out {
		if c.Name == fmt.Sprintf("rule-%d-retire", ruleID) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a retire nominate for rule %d, got %+v", ruleID, out)
	}
}

func TestDetectRuleLifecycleEmptyDB(t *testing.T) {
	st := insightTestStore(t)
	out, err := detectRuleLifecycle(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("empty DB should yield 0 candidates, got %+v", out)
	}
}

// TestDetectContextPromoteReadyPositiveDelta: a team-layer context with a
// version bump that clearly improves done-rate (CIs disjoint) nominates
// promote team->company.
func TestDetectContextPromoteReadyPositiveDelta(t *testing.T) {
	st := insightTestStore(t)
	// team context v1: 15 runs, all failed (0%)
	for i := 0; i < 15; i++ {
		id := fmt.Sprintf("v1-%d", i)
		seedContextRun(t, st, id, "proj-a", "a1", "failed", 1, 0)
		if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, datetime('now'), 'context_injected', '{"team":1}')`, id); err != nil {
			t.Fatal(err)
		}
	}
	// team context v2: 15 runs, all done (100%) — CIs must be disjoint at this n.
	for i := 0; i < 15; i++ {
		id := fmt.Sprintf("v2-%d", i)
		seedContextRun(t, st, id, "proj-a", "a1", "done", 1, 0)
		if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, datetime('now'), 'context_injected', '{"team":2}')`, id); err != nil {
			t.Fatal(err)
		}
	}
	// Real context_versions rows for v1/v2 so contextVersionRefID resolves.
	if _, err := st.DB.Exec(`INSERT INTO contexts(layer, target_id, created_at, updated_at) VALUES('team','proj-a',datetime('now'),datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	var ctxID int64
	if err := st.DB.QueryRow(`SELECT id FROM contexts WHERE layer='team' AND target_id='proj-a'`).Scan(&ctxID); err != nil {
		t.Fatal(err)
	}
	for _, v := range []int{1, 2} {
		if _, err := st.DB.Exec(`INSERT INTO context_versions(context_id, version_n, content, author, note, created_at)
			VALUES(?, ?, 'body', 'tester', '', datetime('now'))`, ctxID, v); err != nil {
			t.Fatal(err)
		}
	}

	out, err := detectContextPromote(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 promote nominate, got %+v", out)
	}
	c := out[0]
	if c.Kind != KindContext || c.Layer != "company" || c.LayerTarget != "*" {
		t.Errorf("promote nominate shape wrong: %+v", c)
	}
	if c.RefID == 0 {
		t.Error("promote nominate must carry a real ref_id to the v2 context_versions row")
	}
}

func TestDetectContextPromoteEmptyDB(t *testing.T) {
	st := insightTestStore(t)
	out, err := detectContextPromote(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("empty DB should yield 0 candidates, got %+v", out)
	}
}

// TestDetectKnowledgeUnitsEmptyDB: fully empty store → orchestrator returns
// 0 nominated, 0 skipped, no error (honest empty, not a crash).
func TestDetectKnowledgeUnitsEmptyDB(t *testing.T) {
	st := insightTestStore(t)
	nominated, skipped, err := DetectKnowledgeUnits(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if nominated != 0 || skipped != 0 {
		t.Errorf("empty DB: nominated=%d skipped=%d, want 0/0", nominated, skipped)
	}
}

// TestDetectKnowledgeUnitsDedupOnSecondSweep: running the sweep twice on the
// same qualifying data must not create a duplicate draft — the second
// sweep's NominateUnit call is rejected by P1's one-draft-at-a-time dedup and
// counted as skipped, not a failure.
func TestDetectKnowledgeUnitsDedupOnSecondSweep(t *testing.T) {
	st := insightTestStore(t)
	withLocalSkillFile(t, "cook-plan", "# Cook Plan\nsteps...")
	for i := 0; i < 10; i++ {
		seedSkillRun(t, st, fmt.Sprintf("p%d", i), "proj-a", "done", "cook-plan")
	}
	for i := 0; i < 10; i++ {
		seedSkillRun(t, st, fmt.Sprintf("a%d", i), "proj-a", "failed", "")
	}

	n1, s1, err := DetectKnowledgeUnits(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n1 != 1 {
		t.Fatalf("first sweep: nominated=%d, want 1", n1)
	}

	n2, s2, err := DetectKnowledgeUnits(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 || s2 == 0 {
		t.Errorf("second sweep on same data: nominated=%d skipped=%d, want 0 nominated, >0 skipped", n2, s2)
	}
	_ = s1

	units, err := ListUnits(st, StateNominated)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, u := range units {
		if u.Kind == KindSkill && u.Name == "cook-plan" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 nominated draft for skill cook-plan, found %d", count)
	}
}
