package learn

import (
	"testing"

	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func TestGradeCalibrationAndFallback(t *testing.T) {
	// Small fleet → static bands + uncalibrated flag.
	g := GradeFor(85, []float64{85, 60})
	if !g.Uncalibrated || g.Letter != "B" {
		t.Errorf("small fleet: %+v", g)
	}
	// Calibrated fleet of 10: composite 95 above 9 of 10 → p90 → A.
	fleet := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 95}
	g = GradeFor(95, fleet)
	if g.Uncalibrated || g.Letter != "A" || g.Percentile != 90 {
		t.Errorf("top: %+v", g)
	}
	if g = GradeFor(10, fleet); g.Letter != "F" {
		t.Errorf("bottom: %+v", g)
	}
	if g = GradeFor(65, fleet); g.Letter != "B" { // above 6/10 = p60
		t.Errorf("mid: %+v", g)
	}
}

func TestROINoDoubleCounting(t *testing.T) {
	st := testStore(t)
	seedFleet(t, st)
	// a3: killed $4 (failed bucket) + flagged done $2 (flagged bucket).
	roi, err := ComputeROI(st, "a3", 30, 100) // acceptance 100 → no rejected share
	if err != nil {
		t.Fatal(err)
	}
	if roi.TotalUSD != 6 || roi.FailedUSD != 4 || roi.FlaggedUSD != 2 || roi.RejectedUSD != 0 {
		t.Errorf("roi buckets: %+v", roi)
	}
	if roi.WastedUSD > roi.TotalUSD {
		t.Errorf("waste exceeds total: %+v", roi)
	}
	// With acceptance 50%, clean bucket is empty for a3 → rejected still 0.
	roi2, _ := ComputeROI(st, "a3", 30, 50)
	if roi2.RejectedUSD != 0 {
		t.Errorf("rejected share must come only from clean runs: %+v", roi2)
	}
}

func TestLeaderboardOrderAndDistribution(t *testing.T) {
	st := testStore(t)
	seedFleet(t, st)
	rows, err := Leaderboard(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows: %d", len(rows))
	}
	if rows[0].AgentID != "a1" {
		t.Errorf("a1 must lead: %s", rows[0].AgentID)
	}
	if rows[0].Composite < rows[1].Composite || rows[1].Composite < rows[2].Composite {
		t.Error("not sorted desc")
	}
	dist := GradeDistribution(rows)
	total := 0
	for _, n := range dist {
		total += n
	}
	if total != 3 {
		t.Errorf("distribution total: %d", total)
	}
	for _, r := range rows {
		if !r.Grade.Uncalibrated { // fleet of 3 < minFleet
			t.Errorf("fleet of 3 must be uncalibrated: %+v", r.Grade)
		}
	}
}

func TestQualityGateChecks(t *testing.T) {
	st := testStore(t)
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "g-r1", "a1", "done", 1, 0)

	results, err := RunChecks(st, "g-r1", "", []string{"true", "false"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || !results[0].OK || results[1].OK {
		t.Errorf("results: %+v", results)
	}
	var flags, gateRows int
	st.DB.QueryRow(`SELECT count(*) FROM flags WHERE run_id='g-r1' AND status='open'`).Scan(&flags)
	st.DB.QueryRow(`SELECT count(*) FROM gate_results WHERE run_id='g-r1'`).Scan(&gateRows)
	if flags != 1 || gateRows != 2 {
		t.Errorf("flags=%d gate_results=%d", flags, gateRows)
	}
}
