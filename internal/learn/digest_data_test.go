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
