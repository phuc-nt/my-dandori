package learn

import (
	"fmt"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedLedgerRun inserts a run with the given final status for ledger tests.
func seedLedgerRun(t *testing.T, st *store.Store, id, status string) {
	t.Helper()
	_, err := st.DB.Exec(`INSERT INTO runs(id, session_id, project, status, started_at)
		VALUES(?,?,?,?,datetime('now','-1 hour'))`, id, id, "proj-a", status)
	if err != nil {
		t.Fatal(err)
	}
}

// seedGuardrailBlock inserts one guardrail_block event on a run.
func seedGuardrailBlock(t *testing.T, st *store.Store, runID, payload string) {
	t.Helper()
	_, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, datetime('now'), 'guardrail_block', 'Bash', 0, ?)`, runID, payload)
	if err != nil {
		t.Fatal(err)
	}
}

// seedGuardrailRule inserts a guardrail_rules row and returns its assigned
// id. Migration 002_seed_guardrails.sql pre-seeds ids 1-7 in every fresh
// store, so tests must not assume a specific id — they insert without one
// and read back what autoincrement assigned.
func seedGuardrailRule(t *testing.T, st *store.Store, description string) int {
	t.Helper()
	res, err := st.DB.Exec(`INSERT INTO guardrail_rules(kind, pattern, description, enabled)
		VALUES('regex', 'x', ?, 1)`, description)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return int(id)
}

// TestGuardrailLedgerRuleSuffixJoinsById: a block payload with "(rule #7)"
// must land in PerRule joined to guardrail_rules id=7 — the rule-suffix
// parse must be tried BEFORE the bare class-token parse (F1).
func TestGuardrailLedgerRuleSuffixJoinsById(t *testing.T) {
	st := insightTestStore(t)
	ruleID := seedGuardrailRule(t, st, "no rm -rf")
	seedLedgerRun(t, st, "r1", "done")
	seedGuardrailBlock(t, st, "r1", fmt.Sprintf("[dandori G1] blocked: no rm -rf (rule #%d)", ruleID))

	result, err := GuardrailLedger(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PerRule) != 1 {
		t.Fatalf("PerRule = %+v, want 1 row", result.PerRule)
	}
	if result.PerRule[0].RuleID != ruleID || result.PerRule[0].Description != "no rm -rf" {
		t.Errorf("PerRule[0] = %+v, want RuleID=%d Description='no rm -rf'", result.PerRule[0], ruleID)
	}
	if len(result.PerClass) != 0 {
		t.Errorf("PerClass = %+v, want 0 (rule-suffixed block must not also appear as class)", result.PerClass)
	}
}

// TestGuardrailLedgerG2ThreeBlocksOneRunClassNotRule is F1+F10: G2 (sandbox)
// never carries a rule suffix, so 3 blocks from the SAME run must land in
// PerClass "sandbox" with Blocks=3 but RunsDone counted once (distinct run,
// not block count), and must NEVER be joined to guardrail_rules — even
// though a rule with id matching some digit in the payload might exist.
func TestGuardrailLedgerG2ThreeBlocksOneRunClassNotRule(t *testing.T) {
	st := insightTestStore(t)
	// A rule row exists (any id) — must NOT be attributed to it regardless.
	seedGuardrailRule(t, st, "unrelated rule")
	seedLedgerRun(t, st, "r1", "done")
	for i := 0; i < 3; i++ {
		seedGuardrailBlock(t, st, "r1", "[dandori G2] path outside run scope: /etc/passwd (scope: /work)")
	}

	result, err := GuardrailLedger(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PerRule) != 0 {
		t.Fatalf("PerRule = %+v, want 0 (G2 must never join guardrail_rules — F1)", result.PerRule)
	}
	if len(result.PerClass) != 1 {
		t.Fatalf("PerClass = %+v, want 1 row", result.PerClass)
	}
	c := result.PerClass[0]
	if c.Class != "sandbox" {
		t.Errorf("Class = %q, want sandbox", c.Class)
	}
	if c.Blocks != 3 {
		t.Errorf("Blocks = %d, want 3", c.Blocks)
	}
	if c.RunsDone != 1 {
		t.Errorf("RunsDone = %d, want 1 (distinct run, not block count — F10)", c.RunsDone)
	}
}

// TestGuardrailLedgerG3RunningGoesToUnfinishedBucket is F10: a run still
// running when a G3 budget block fired must bucket into RunsUnfinished, not
// RunsDone/RunsKilledOrFailed, and excluded from the outcome denominator.
func TestGuardrailLedgerG3RunningGoesToUnfinishedBucket(t *testing.T) {
	st := insightTestStore(t)
	seedGuardrailRule(t, st, "should not be joined")
	seedLedgerRun(t, st, "r1", "running")
	seedGuardrailBlock(t, st, "r1", "[dandori G3] budget exceeded for agent x: $10.00 / $5.00 this month — hard stop")

	result, err := GuardrailLedger(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PerRule) != 0 {
		t.Fatalf("PerRule = %+v, want 0 (G3 must never join guardrail_rules — F1)", result.PerRule)
	}
	if len(result.PerClass) != 1 {
		t.Fatalf("PerClass = %+v, want 1 row", result.PerClass)
	}
	c := result.PerClass[0]
	if c.Class != "budget" {
		t.Errorf("Class = %q, want budget", c.Class)
	}
	if c.RunsUnfinished != 1 {
		t.Errorf("RunsUnfinished = %d, want 1", c.RunsUnfinished)
	}
	if c.RunsDone != 0 || c.RunsKilledOrFailed != 0 {
		t.Errorf("RunsDone/RunsKilledOrFailed = %d/%d, want 0/0", c.RunsDone, c.RunsKilledOrFailed)
	}
	if !c.Insufficient() {
		t.Error("outcome sample (0 finished runs) should be Insufficient")
	}
}

// TestGuardrailLedgerNeverJoinsClassLevelBlocks is an explicit cross-check
// across G1(no-suffix)/G2/G3/G4(no-suffix): none of these class-level tokens
// may ever produce a PerRule row, regardless of guardrail_rules contents.
func TestGuardrailLedgerNeverJoinsClassLevelBlocks(t *testing.T) {
	st := insightTestStore(t)
	seedGuardrailRule(t, st, "rule one")
	seedGuardrailRule(t, st, "rule two")
	seedGuardrailRule(t, st, "rule three")
	seedGuardrailRule(t, st, "rule four")

	seedLedgerRun(t, st, "r1", "done")
	seedGuardrailBlock(t, st, "r1", "[dandori G2] path outside run scope: /etc (scope: /work)")
	seedLedgerRun(t, st, "r2", "done")
	seedGuardrailBlock(t, st, "r2", "[dandori G3] monthly budget exhausted — mutating tool calls are blocked until the budget is raised")
	seedLedgerRun(t, st, "r3", "done")
	seedGuardrailBlock(t, st, "r3", "[dandori G4] supervised band: edits and shell commands require approval")

	result, err := GuardrailLedger(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PerRule) != 0 {
		t.Fatalf("PerRule = %+v, want 0 (all class-level, no rule suffix)", result.PerRule)
	}
	classes := map[string]bool{}
	for _, c := range result.PerClass {
		classes[c.Class] = true
	}
	for _, want := range []string{"sandbox", "budget", "gate"} {
		if !classes[want] {
			t.Errorf("PerClass missing %q: %+v", want, result.PerClass)
		}
	}
}

// TestGuardrailLedgerExcludesSyntheticFixtures: g2-verify*/gate-verify* runs
// must not contribute blocks to either tier.
func TestGuardrailLedgerExcludesSyntheticFixtures(t *testing.T) {
	st := insightTestStore(t)
	seedLedgerRun(t, st, "g2-verify-1", "running")
	seedGuardrailBlock(t, st, "g2-verify-1", "[dandori G2] path outside run scope: /etc (scope: /work)")
	seedLedgerRun(t, st, "gate-verify-v10", "running")
	seedGuardrailBlock(t, st, "gate-verify-v10", "[dandori G4] approval #1 still pending after 10s — ask an operator")

	result, err := GuardrailLedger(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PerRule) != 0 || len(result.PerClass) != 0 {
		t.Errorf("result = %+v, want empty (synthetic fixtures excluded)", result)
	}
}

func TestGuardrailLedgerEmpty(t *testing.T) {
	st := insightTestStore(t)
	result, err := GuardrailLedger(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PerRule) != 0 || len(result.PerClass) != 0 {
		t.Errorf("result = %+v, want empty store to yield no rows", result)
	}
}
