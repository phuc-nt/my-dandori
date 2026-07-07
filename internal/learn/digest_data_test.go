package learn

import "testing"

func TestBuildDigestDataSeededFleet(t *testing.T) {
	st := testStore(t)
	seedFleet(t, st)

	data, err := BuildDigestData(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Board) != 3 {
		t.Fatalf("board rows: got %d, want 3", len(data.Board))
	}
	if data.TotalRuns == 0 {
		t.Error("total runs should be non-zero for a seeded fleet")
	}
	if data.TotalCost <= 0 {
		t.Error("total cost should be non-zero for a seeded fleet")
	}
	if data.FleetROI == nil {
		t.Fatal("fleet ROI must not be nil")
	}
	if data.WindowDays != 30 {
		t.Errorf("window days: got %d, want 30", data.WindowDays)
	}
	// CFR / spikes are computed but may legitimately be zero/empty on this
	// fixture — just assert the call didn't error (already checked above).
}

// TestKnowledgePublishedThisWeekCountAndTitlesNoActorField: verifies the
// digest source query returns count+titles only — KnowledgePublishedThisWeek
// has no return value carrying an actor/contributor breakdown by
// construction (its signature is (count int, titles []string, err error)),
// so this test locks that shape plus the actual count/title correctness.
func TestKnowledgePublishedThisWeekCountAndTitlesNoActorField(t *testing.T) {
	st := testStore(t)
	id, err := NominateUnit(st, NominateParams{
		Kind: KindSkill, Name: "checkout-flow-fix", Title: "Checkout flow fix skill",
		Body: "# steps", NominatedBy: "dandori-observer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := transition(st, id, StateNominated, StateInReview, "admin", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := transition(st, id, StateInReview, StatePublished, "admin", "auto"); err != nil {
		t.Fatal(err)
	}

	count, titles, err := KnowledgePublishedThisWeek(st, 7)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if len(titles) != 1 || titles[0] != "Checkout flow fix skill" {
		t.Errorf("titles = %v, want [\"Checkout flow fix skill\"]", titles)
	}
}

// TestKnowledgePublishedThisWeekExcludesOutsideWindow: a publish transition
// older than the window must not count toward this week's digest line.
func TestKnowledgePublishedThisWeekExcludesOutsideWindow(t *testing.T) {
	st := testStore(t)
	id, err := NominateUnit(st, NominateParams{
		Kind: KindSkill, Name: "old-skill", Title: "Old skill",
		Body: "# steps", NominatedBy: "dandori-observer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := transition(st, id, StateNominated, StateInReview, "admin", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := transition(st, id, StateInReview, StatePublished, "admin", "auto"); err != nil {
		t.Fatal(err)
	}
	// Backdate the transition row itself past the 7-day window.
	if _, err := st.DB.Exec(`UPDATE knowledge_transitions SET at = datetime('now','-30 days')
		WHERE unit_id = ? AND to_state = 'published'`, id); err != nil {
		t.Fatal(err)
	}

	count, titles, err := KnowledgePublishedThisWeek(st, 7)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(titles) != 0 {
		t.Errorf("out-of-window publish must not count: count=%d titles=%v", count, titles)
	}
}

func TestBuildDigestDataEmptyFleetNoPanic(t *testing.T) {
	st := testStore(t)
	data, err := BuildDigestData(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if data == nil {
		t.Fatal("data must not be nil")
	}
	if len(data.Board) != 0 {
		t.Errorf("board: got %d rows, want 0", len(data.Board))
	}
	if data.TotalCost != 0 || data.TotalRuns != 0 {
		t.Errorf("totals should be zero: cost=%f runs=%d", data.TotalCost, data.TotalRuns)
	}
	if data.FleetROI == nil {
		t.Fatal("fleet ROI must not be nil even for an empty fleet")
	}
	if data.FleetROI.TotalUSD != 0 {
		t.Errorf("fleet ROI total should be 0, got %f", data.FleetROI.TotalUSD)
	}
}
