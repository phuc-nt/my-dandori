package learn

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// A clean, well-prompted, unsteered run is a candidate; noisy runs are not.
func TestDetectCandidates(t *testing.T) {
	st := testStore(t)
	// Golden run: no errors, spec flags set, zero steering.
	seedBehaviorRun(t, st, "gold", "alice@mac", "agent-a", "done", 0, `{"w":120,"spec":7}`, 0)
	// Disqualified: tool errors.
	seedBehaviorRun(t, st, "errs", "alice@mac", "agent-a", "done", 0, `{"w":120,"spec":7}`, 3)
	// Disqualified: vague prompt (spec=0).
	seedBehaviorRun(t, st, "vague", "alice@mac", "agent-a", "done", 0, `{"w":8,"spec":0}`, 0)
	// Disqualified: heavy steering.
	seedBehaviorRun(t, st, "steer", "alice@mac", "agent-a", "done", 6, `{"w":120,"spec":7}`, 0)

	cands, err := DetectCandidates(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].RunID != "gold" {
		t.Fatalf("candidates: %+v, want only gold", cands)
	}
	why := cands[0].Why
	for _, want := range []string{"file", "task", "tiêu chí", "can thiệp"} {
		if !strings.Contains(why, want) {
			t.Errorf("why lacks %q: %s", want, why)
		}
	}
}

func TestPromoteOnceAndNotRedetected(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "gold", "alice@mac", "agent-a", "done", 0, `{"w":120,"spec":7}`, 0)
	cands, _ := DetectCandidates(st, 30)
	if len(cands) != 1 {
		t.Fatal("setup: no candidate")
	}
	id, err := PromoteCandidate(st, cands[0], "phucnt")
	if err != nil || id == 0 {
		t.Fatal(err)
	}
	if _, err := PromoteCandidate(st, cands[0], "phucnt"); err == nil {
		t.Error("double promote must fail")
	}
	again, _ := DetectCandidates(st, 30)
	if len(again) != 0 {
		t.Errorf("promoted run re-detected: %+v", again)
	}
}

// PromoteCandidate must nominate a knowledge_units row, not write playbooks
// directly (the bug this phase fixes: promote used to bypass review).
func TestPromoteCandidateCreatesNoDirectPlaybookRow(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "gold", "alice@mac", "agent-a", "done", 0, `{"w":120,"spec":7}`, 0)
	cands, _ := DetectCandidates(st, 30)
	id, err := PromoteCandidate(st, cands[0], "phucnt")
	if err != nil {
		t.Fatal(err)
	}
	var pbCount int
	st.DB.QueryRow(`SELECT count(*) FROM playbooks`).Scan(&pbCount)
	if pbCount != 0 {
		t.Errorf("PromoteCandidate must not write playbooks directly, found %d rows", pbCount)
	}
	u, err := GetUnit(st, id)
	if err != nil || u == nil {
		t.Fatalf("GetUnit(%d): %+v err=%v", id, u, err)
	}
	if u.Kind != KindPlaybook || u.State != StateNominated {
		t.Errorf("unit kind/state: %+v", u)
	}
}

// The nominated card must describe the PATTERN — and must never contain an
// operator identity (publishing human comparisons teaches proxy-gaming).
func TestPlaybookCardHasNoOperatorIdentity(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "gold", "alice@mac", "agent-a", "done", 0, `{"w":120,"spec":7}`, 0)
	cands, _ := DetectCandidates(st, 30)
	id, err := PromoteCandidate(st, cands[0], "phucnt")
	if err != nil {
		t.Fatal(err)
	}
	u, err := GetUnit(st, id)
	if err != nil || u == nil {
		t.Fatalf("GetUnit(%d): %+v err=%v", id, u, err)
	}
	if strings.Contains(u.Title, "alice@mac") {
		t.Errorf("card leaks operator identity: %s", u.Title)
	}
	if !strings.Contains(cands[0].Why, "mẫu đáng nhân bản") {
		t.Errorf("candidate lacks pattern description: %s", cands[0].Why)
	}
}

func TestAdoptionBeforeAfter(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "gold", "coach@mac", "agent-a", "done", 0, `{"w":120,"spec":7}`, 0)
	// RecordAdoption/ComputeAdoptionOutcomes/AdoptionReport operate on a real
	// playbooks row (P6 owns generalizing them to unit_id). PromoteCandidate
	// itself no longer creates that row — the applier does, post-approval —
	// so this test seeds the playbook directly to exercise the (unchanged)
	// adoption-tracking behavior.
	res, err := st.DB.Exec(`INSERT INTO playbooks(name, run_id, agent_id, created_at, created_by)
		VALUES('Pattern: agent-a', 'gold', 'agent-a', ?, 'phucnt')`, store.Now())
	if err != nil {
		t.Fatal(err)
	}
	pbID, _ := res.LastInsertId()
	// Adopter with a weak history: 1 done / 2 ended → before = 0.5.
	seedBehaviorRun(t, st, "h1", "bob@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "h2", "bob@dev", "agent-a", "failed", 0, ``, 0)
	adoptID, err := RecordAdoption(st, pbID, "bob@dev", "", 30)
	if err != nil {
		t.Fatal(err)
	}
	var before float64
	st.DB.QueryRow(`SELECT metric_before FROM adoptions WHERE id = ?`, adoptID).Scan(&before)
	if before != 0.5 {
		t.Errorf("metric_before: %v, want 0.5", before)
	}
	// Seeded runs share one RFC3339 second — backdate the adoption so the
	// "after adoption" comparison is unambiguous.
	st.DB.Exec(`UPDATE adoptions SET adopted_at = '2026-07-01T00:00:00Z' WHERE id = ?`, adoptID)
	st.DB.Exec(`UPDATE runs SET started_at = '2026-06-30T00:00:00Z' WHERE id IN ('h1','h2')`)
	// Not enough post-adoption runs yet → no outcome.
	if n, _ := ComputeAdoptionOutcomes(st); n != 0 {
		t.Errorf("premature outcome: %d", n)
	}
	// Three clean runs later → outcome computes, improved.
	seedBehaviorRun(t, st, "p1", "bob@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "p2", "bob@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "p3", "bob@dev", "agent-a", "done", 0, ``, 0)
	if n, err := ComputeAdoptionOutcomes(st); err != nil || n != 1 {
		t.Fatalf("outcomes: n=%d err=%v", n, err)
	}
	report, err := AdoptionReport(st, pbID)
	if err != nil || len(report) != 1 {
		t.Fatalf("report: %+v err=%v", report, err)
	}
	r := report[0]
	if r.Before == nil || r.After == nil || r.Improved == nil || !*r.Improved {
		t.Errorf("before/after/improved: %+v", r)
	}
	// Outcome recompute is idempotent.
	if n, _ := ComputeAdoptionOutcomes(st); n != 0 {
		t.Error("outcome recomputed twice")
	}
}

// nominateUnitForTest creates a published knowledge_units row directly (skip
// the review pipeline — flywheel tests only need a live unit id to attach
// adoptions to).
func nominateUnitForTest(t *testing.T, st *store.Store, kind, name string) int64 {
	t.Helper()
	id, err := NominateUnit(st, NominateParams{
		Kind: kind, Name: name, Title: "unit " + name, Body: nonEmptyBodyFor(kind),
		NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET state = 'published' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func nonEmptyBodyFor(kind string) string {
	if kind == KindSkill || kind == KindContext {
		return "body"
	}
	return ""
}

// RecordUnitAdoption must freeze metric_before the same way RecordAdoption
// does, write unit_id, and persist the installed flag verbatim (F4).
func TestRecordUnitAdoptionFreezesBeforeAndInstalled(t *testing.T) {
	st := testStore(t)
	unitID := nominateUnitForTest(t, st, KindSkill, "unit-skill-a")
	seedBehaviorRun(t, st, "h1", "carol@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "h2", "carol@dev", "agent-a", "failed", 0, ``, 0)

	adoptID, err := RecordUnitAdoption(st, unitID, "carol@dev", "", false, 30)
	if err != nil {
		t.Fatal(err)
	}
	var before float64
	var gotUnit sql.NullInt64
	var installed int
	st.DB.QueryRow(`SELECT metric_before, unit_id, installed FROM adoptions WHERE id = ?`, adoptID).
		Scan(&before, &gotUnit, &installed)
	if before != 0.5 {
		t.Errorf("metric_before=%v, want 0.5", before)
	}
	if !gotUnit.Valid || gotUnit.Int64 != unitID {
		t.Errorf("unit_id=%v, want %d", gotUnit, unitID)
	}
	if installed != 0 {
		t.Errorf("installed=%d, want 0 (suggest-only)", installed)
	}

	adoptID2, err := RecordUnitAdoption(st, unitID, "carol@dev", "", true, 30)
	if err != nil {
		t.Fatal(err)
	}
	st.DB.QueryRow(`SELECT installed FROM adoptions WHERE id = ?`, adoptID2).Scan(&installed)
	if installed != 1 {
		t.Errorf("installed=%d, want 1 (pulled)", installed)
	}
}

// TestRecordUnitAdoptionDedupsRepeatPull covers M5: repeat-pulling the SAME
// unit as the SAME operator must refresh the one existing adoptions row
// (adopted_at/installed/metric_before) rather than insert a second one — a
// single operator pulling N times must never look like N adopters, and must
// never let NominateRetireProposals' avg(before/after) double-count them.
func TestRecordUnitAdoptionDedupsRepeatPull(t *testing.T) {
	st := testStore(t)
	unitID := nominateUnitForTest(t, st, KindSkill, "unit-skill-dedup")
	seedBehaviorRun(t, st, "h1", "dee@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "h2", "dee@dev", "agent-a", "failed", 0, ``, 0)

	firstID, err := RecordUnitAdoption(st, unitID, "dee@dev", "", true, 30)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the first adoption having already been measured, to prove a
	// repeat pull clears the stale outcome rather than leaving it pinned to
	// the OLD adopted_at.
	if _, err := st.DB.Exec(`UPDATE adoptions SET metric_after = 0.9, computed_at = ? WHERE id = ?`,
		store.Now(), firstID); err != nil {
		t.Fatal(err)
	}

	secondID, err := RecordUnitAdoption(st, unitID, "dee@dev", "", true, 30)
	if err != nil {
		t.Fatal(err)
	}
	if secondID != firstID {
		t.Errorf("repeat pull must reuse the same row: first=%d second=%d", firstID, secondID)
	}

	var count int
	st.DB.QueryRow(`SELECT count(*) FROM adoptions WHERE unit_id = ? AND operator_id = ?`, unitID, "dee@dev").Scan(&count)
	if count != 1 {
		t.Errorf("adoptions rows for (unit,operator): %d, want 1", count)
	}

	var metricAfter sql.NullFloat64
	var computedAt sql.NullString
	st.DB.QueryRow(`SELECT metric_after, computed_at FROM adoptions WHERE id = ?`, firstID).
		Scan(&metricAfter, &computedAt)
	if metricAfter.Valid || computedAt.Valid {
		t.Errorf("repeat pull must clear the stale outcome: metric_after=%v computed_at=%v", metricAfter, computedAt)
	}
}

// A skill adoption that never shows a tool_use/Skill event after adopted_at
// is installed-not-active — ComputeAdoptionOutcomes must leave it pending
// forever rather than farm an outcome from the adopt-click alone.
func TestComputeAdoptionOutcomesSkipsInstalledNotActiveSkill(t *testing.T) {
	st := testStore(t)
	unitID := nominateUnitForTest(t, st, KindSkill, "unit-skill-b")
	seedBehaviorRun(t, st, "h1", "dan@dev", "agent-a", "done", 0, ``, 0)

	adoptID, err := RecordUnitAdoption(st, unitID, "dan@dev", "", true, 30)
	if err != nil {
		t.Fatal(err)
	}
	st.DB.Exec(`UPDATE adoptions SET adopted_at = '2026-07-01T00:00:00Z' WHERE id = ?`, adoptID)
	st.DB.Exec(`UPDATE runs SET started_at = '2026-06-30T00:00:00Z' WHERE id = 'h1'`)

	// Enough subsequent runs, but NO Skill tool_use event anywhere.
	seedBehaviorRun(t, st, "p1", "dan@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "p2", "dan@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "p3", "dan@dev", "agent-a", "done", 0, ``, 0)

	if n, err := ComputeAdoptionOutcomes(st); err != nil || n != 0 {
		t.Fatalf("outcomes: n=%d err=%v, want 0 (installed-not-active must skip)", n, err)
	}
	var computedAt sql.NullString
	st.DB.QueryRow(`SELECT computed_at FROM adoptions WHERE id = ?`, adoptID).Scan(&computedAt)
	if computedAt.Valid {
		t.Error("computed_at must stay NULL for installed-not-active skill")
	}
}

// The same skill adoption, once the operator actually invokes the skill
// (tool_use/Skill $.skill=name) after adopted_at, must compute normally.
func TestComputeAdoptionOutcomesComputesForActiveSkill(t *testing.T) {
	st := testStore(t)
	unitID := nominateUnitForTest(t, st, KindSkill, "unit-skill-c")
	seedBehaviorRun(t, st, "h1", "erin@dev", "agent-a", "done", 0, ``, 0)

	adoptID, err := RecordUnitAdoption(st, unitID, "erin@dev", "", true, 30)
	if err != nil {
		t.Fatal(err)
	}
	st.DB.Exec(`UPDATE adoptions SET adopted_at = '2026-07-01T00:00:00Z' WHERE id = ?`, adoptID)
	st.DB.Exec(`UPDATE runs SET started_at = '2026-06-30T00:00:00Z' WHERE id = 'h1'`)

	seedBehaviorRun(t, st, "p1", "erin@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "p2", "erin@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "p3", "erin@dev", "agent-a", "done", 0, ``, 0)
	st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload) VALUES('p1', ?, 'tool_use', 'Skill', 1, ?)`,
		store.Now(), `{"skill":"unit-skill-c"}`)

	n, err := ComputeAdoptionOutcomes(st)
	if err != nil || n != 1 {
		t.Fatalf("outcomes: n=%d err=%v, want 1 (active skill must compute)", n, err)
	}
	var computedAt sql.NullString
	st.DB.QueryRow(`SELECT computed_at FROM adoptions WHERE id = ?`, adoptID).Scan(&computedAt)
	if !computedAt.Valid {
		t.Error("computed_at must be set once active")
	}
}

// measured-worse must nominate a retire-proposal draft (never auto-retire)
// and never double-nominate while one is still pending.
func TestNominateRetireProposalsFiresOnMeasuredWorse(t *testing.T) {
	st := testStore(t)
	unitID := nominateUnitForTest(t, st, KindPlaybook, "unit-playbook-worse")
	now := store.Now()
	// Directly seed a measured adoption: before=0.9, after=0.5 (well past the
	// retireProposalMargin floor) — ComputeAdoptionOutcomes has already run
	// its course for this row, so seed computed_at too.
	if _, err := st.DB.Exec(`INSERT INTO adoptions(unit_id, installed, operator_id, adopted_at, metric_before, metric_after, computed_at)
		VALUES(?, 1, 'fay@dev', ?, 0.9, 0.5, ?)`, unitID, now, now); err != nil {
		t.Fatal(err)
	}

	n, err := NominateRetireProposals(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("nominated=%d, want 1", n)
	}
	var title, state string
	if err := st.DB.QueryRow(`SELECT title, state FROM knowledge_units WHERE name = ?`, "unit-playbook-worse-retire-proposal").
		Scan(&title, &state); err != nil {
		t.Fatal(err)
	}
	if state != StateNominated {
		t.Errorf("retire-proposal state=%q, want nominated (NOT auto-retire)", state)
	}
	if !strings.Contains(title, "hồi quy về trung bình") {
		t.Errorf("retire-proposal title lacks F10 regression-to-mean caveat: %s", title)
	}

	// Second pass while the draft is still pending must not double-nominate.
	n2, err := NominateRetireProposals(st)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("second pass nominated=%d, want 0 (draft already pending)", n2)
	}
}

// A modest improvement/decline under the margin must never trigger a
// retire-proposal (F10: don't fire on noise).
func TestNominateRetireProposalsSkipsSmallDelta(t *testing.T) {
	st := testStore(t)
	unitID := nominateUnitForTest(t, st, KindPlaybook, "unit-playbook-ok")
	now := store.Now()
	if _, err := st.DB.Exec(`INSERT INTO adoptions(unit_id, installed, operator_id, adopted_at, metric_before, metric_after, computed_at)
		VALUES(?, 1, 'gil@dev', ?, 0.7, 0.65, ?)`, unitID, now, now); err != nil {
		t.Fatal(err)
	}
	n, err := NominateRetireProposals(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("nominated=%d, want 0 (delta under margin)", n)
	}
}
