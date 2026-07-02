package learn

import (
	"strings"
	"testing"
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

// The published card must describe the PATTERN — and must never contain an
// operator identity (publishing human comparisons teaches proxy-gaming).
func TestPlaybookCardHasNoOperatorIdentity(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "gold", "alice@mac", "agent-a", "done", 0, `{"w":120,"spec":7}`, 0)
	cands, _ := DetectCandidates(st, 30)
	id, err := PromoteCandidate(st, cands[0], "phucnt")
	if err != nil {
		t.Fatal(err)
	}
	var name, notes string
	st.DB.QueryRow(`SELECT name, notes FROM playbooks WHERE id = ?`, id).Scan(&name, &notes)
	card := name + " " + notes
	if strings.Contains(card, "alice@mac") {
		t.Errorf("card leaks operator identity: %s", card)
	}
	if !strings.Contains(notes, "mẫu đáng nhân bản") {
		t.Errorf("card lacks pattern description: %s", notes)
	}
}

func TestAdoptionBeforeAfter(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "gold", "coach@mac", "agent-a", "done", 0, `{"w":120,"spec":7}`, 0)
	cands, _ := DetectCandidates(st, 30)
	pbID, err := PromoteCandidate(st, cands[0], "phucnt")
	if err != nil {
		t.Fatal(err)
	}
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
