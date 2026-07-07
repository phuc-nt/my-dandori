package learn

import (
	"fmt"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedHorizonRun inserts a run whose started_at/ended_at differ by durMin
// minutes, at a fixed anchor time so julianday duration math is exact
// regardless of test wall-clock time. ended_at is computed by SQLite's own
// datetime() at insert time, so it stays in the same Z-format runs use.
func seedHorizonRun(t *testing.T, st *store.Store, id, agent, model, status string, durMin float64) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?,?,datetime('now'))
		ON CONFLICT(id) DO NOTHING`, agent, agent); err != nil {
		t.Fatal(err)
	}
	const start = "2026-07-01T00:00:00Z"
	_, err := st.DB.Exec(`INSERT INTO runs
		(id, session_id, agent_id, model, status, started_at, ended_at)
		VALUES(?,?,?,?,?, ?, datetime(?, '+' || ? || ' minutes'))`,
		id, id, agent, model, status, start, start, durMin)
	if err != nil {
		t.Fatal(err)
	}
}

func TestTimeHorizonBucketBoundaries(t *testing.T) {
	st := insightTestStore(t)
	seedHorizonRun(t, st, "r25", "a", "m", "done", 25)   // <30m
	seedHorizonRun(t, st, "r45", "a", "m", "done", 45)   // 30-60m
	seedHorizonRun(t, st, "r90", "a", "m", "done", 90)   // 60-120m
	seedHorizonRun(t, st, "r200", "a", "m", "done", 200) // >120m

	buckets, err := TimeHorizon(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 4 {
		t.Fatalf("want 4 buckets, got %d", len(buckets))
	}
	want := []struct {
		label string
		runs  int
	}{
		{"<30m", 1}, {"30-60m", 1}, {"60-120m", 1}, {">120m", 1},
	}
	for i, w := range want {
		if buckets[i].Label != w.label {
			t.Errorf("bucket %d label = %q, want %q (order must be fixed, not GROUP BY order)", i, buckets[i].Label, w.label)
		}
		if buckets[i].Runs != w.runs {
			t.Errorf("bucket %s runs = %d, want %d", w.label, buckets[i].Runs, w.runs)
		}
	}
}

func TestTimeHorizonWilsonCI(t *testing.T) {
	st := insightTestStore(t)
	// <30m bucket: 3 done, 1 failed → contrast exists, Wilson CI should not be [0,0].
	seedHorizonRun(t, st, "d1", "a", "m", "done", 10)
	seedHorizonRun(t, st, "d2", "a", "m", "done", 12)
	seedHorizonRun(t, st, "d3", "a", "m", "done", 15)
	seedHorizonRun(t, st, "f1", "a", "m", "failed", 20)

	buckets, err := TimeHorizon(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := buckets[0] // <30m
	if b.Runs != 4 || b.Done != 3 {
		t.Fatalf("<30m bucket = %+v, want runs=4 done=3", b)
	}
	if b.WilsonLo == 0 && b.WilsonHi == 0 {
		t.Error("expected non-trivial Wilson CI for n=4")
	}
	if b.WilsonLo > b.DoneRate || b.WilsonHi < b.DoneRate {
		t.Errorf("Wilson CI [%.3f,%.3f] does not bracket rate %.3f", b.WilsonLo, b.WilsonHi, b.DoneRate)
	}
	if b.NoContrast() {
		t.Error("bucket has a failed run — must not report NoContrast")
	}
}

func TestTimeHorizonZeroContrast(t *testing.T) {
	// F7: fleet where every run in a bucket is 'done' must surface as
	// "chưa có run fail để so", not be sold as a 100% success insight.
	st := insightTestStore(t)
	seedHorizonRun(t, st, "d1", "a", "m", "done", 10)
	seedHorizonRun(t, st, "d2", "a", "m", "done", 15)
	seedHorizonRun(t, st, "d3", "a", "m", "done", 20)

	buckets, err := TimeHorizon(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := buckets[0] // <30m
	if b.Runs != 3 || b.Done != 3 {
		t.Fatalf("<30m bucket = %+v, want runs=3 done=3", b)
	}
	if b.DoneRate != 1.0 {
		t.Errorf("done rate = %v, want 1.0", b.DoneRate)
	}
	if !b.NoContrast() {
		t.Error("all-done bucket must report NoContrast=true (F7)")
	}
	if b.Insufficient() {
		t.Error("n=3 with MinSampleForInsight=3 should not be Insufficient")
	}
}

func TestTimeHorizonInsufficient(t *testing.T) {
	st := insightTestStore(t)
	seedHorizonRun(t, st, "d1", "a", "m", "done", 10) // n=1 < MinSampleForInsight

	buckets, err := TimeHorizon(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !buckets[0].Insufficient() {
		t.Error("n=1 bucket should be Insufficient")
	}
}

func TestTimeHorizonEmptyBucketsStillPresent(t *testing.T) {
	st := insightTestStore(t)
	seedHorizonRun(t, st, "d1", "a", "m", "done", 10) // only <30m populated

	buckets, err := TimeHorizon(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 4 {
		t.Fatalf("want 4 buckets always present, got %d", len(buckets))
	}
	for i, label := range []string{"<30m", "30-60m", "60-120m", ">120m"} {
		if buckets[i].Label != label {
			t.Errorf("bucket %d = %q, want %q", i, buckets[i].Label, label)
		}
	}
	if buckets[1].Runs != 0 || buckets[2].Runs != 0 || buckets[3].Runs != 0 {
		t.Error("unpopulated buckets should have Runs=0, not be dropped")
	}
}

func TestTimeHorizonByModelMinSample(t *testing.T) {
	st := insightTestStore(t)
	// sonnet gets 5 runs in <30m → included.
	for i := 0; i < 5; i++ {
		seedHorizonRun(t, st, fmt.Sprintf("s%d", i), "a", "claude-sonnet-5", "done", 10)
	}
	// haiku gets 4 runs in <30m → excluded (< 5).
	for i := 0; i < 4; i++ {
		seedHorizonRun(t, st, fmt.Sprintf("h%d", i), "a", "claude-haiku", "done", 12)
	}

	rows, err := TimeHorizonByModel(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 bucket×model row (sonnet only), got %d: %+v", len(rows), rows)
	}
	if rows[0].Model != "claude-sonnet-5" || rows[0].Runs != 5 {
		t.Errorf("row = %+v, want sonnet runs=5", rows[0])
	}
}
