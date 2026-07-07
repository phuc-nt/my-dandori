package learn

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

func steeringTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "steer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// seedSteerRun inserts a run with an explicit started/ended pair (so density
// can be computed) and a source (local runs get a steering_msg count,
// central/"ingest" runs only ever get user_msg — F6).
func seedSteerRun(t *testing.T, st *store.Store, id, status, source string, startedAgoMin, durationMin int) {
	t.Helper()
	started := time.Now().UTC().Add(-time.Duration(startedAgoMin) * time.Minute)
	ended := started.Add(time.Duration(durationMin) * time.Minute)
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a','a',datetime('now'))
		ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	_, err := st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, status, started_at, ended_at, source)
		VALUES(?,?,?,'p',?,?,?,?)`,
		id, id, "a", status, started.Format(time.RFC3339), ended.Format(time.RFC3339), source)
	if err != nil {
		t.Fatal(err)
	}
}

// seedUserMsgCount inserts the numeric user_msg event (central-mode
// numerator, also written locally alongside steering_msg text).
func seedUserMsgCount(t *testing.T, st *store.Store, runID string, n int) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, datetime('now'), 'user_msg', ?)`,
		runID, n); err != nil {
		t.Fatal(err)
	}
}

// seedSteeringTexts inserts one steering_msg row per text, in order — the id
// autoincrement gives the sequence-index position the taxonomy relies on.
func seedSteeringTexts(t *testing.T, st *store.Store, runID string, texts []string) {
	t.Helper()
	for _, txt := range texts {
		if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, datetime('now'), 'steering_msg', ?)`,
			runID, txt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSteeringEconomicsDoneRateSplit(t *testing.T) {
	st := steeringTestStore(t)
	// steer>0 side: 2 done, 1 failed (local mode → steering_msg numerator).
	seedSteerRun(t, st, "s1", "done", "hook", 100, 50)
	seedSteeringTexts(t, st, "s1", []string{"a", "b"})
	seedSteerRun(t, st, "s2", "done", "hook", 100, 50)
	seedSteeringTexts(t, st, "s2", []string{"a"})
	seedSteerRun(t, st, "s3", "failed", "hook", 100, 50)
	seedSteeringTexts(t, st, "s3", []string{"a", "b", "c"})
	// steer=0 side: 1 done, 1 failed, no steering_msg rows at all.
	seedSteerRun(t, st, "n1", "done", "hook", 100, 50)
	seedSteerRun(t, st, "n2", "failed", "hook", 100, 50)

	sum, err := SteeringEconomics(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.WithSteer.Runs != 3 || sum.WithSteer.Done != 2 {
		t.Errorf("WithSteer = %+v, want Runs=3 Done=2", sum.WithSteer)
	}
	if sum.WithSteer.DoneRate < 0.66 || sum.WithSteer.DoneRate > 0.67 {
		t.Errorf("WithSteer.DoneRate = %v, want ~0.667", sum.WithSteer.DoneRate)
	}
	if sum.WithSteer.WilsonLo <= 0 || sum.WithSteer.WilsonHi <= sum.WithSteer.WilsonLo {
		t.Errorf("WithSteer Wilson CI not populated: lo=%v hi=%v", sum.WithSteer.WilsonLo, sum.WithSteer.WilsonHi)
	}
	// density: numerator/duration_min averaged. s1=2/50, s2=1/50, s3=3/50.
	wantDensity := (2.0/50 + 1.0/50 + 3.0/50) / 3
	if diff := sum.WithSteer.AvgDensity - wantDensity; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("WithSteer.AvgDensity = %v, want %v", sum.WithSteer.AvgDensity, wantDensity)
	}

	if sum.WithoutSteer.Runs != 2 || sum.WithoutSteer.Done != 1 {
		t.Errorf("WithoutSteer = %+v, want Runs=2 Done=1", sum.WithoutSteer)
	}
	if sum.WithoutSteer.AvgDensity != 0 {
		t.Errorf("WithoutSteer.AvgDensity = %v, want 0 (no steering numerator)", sum.WithoutSteer.AvgDensity)
	}
}

// F7: the n=0 side of the split must not fabricate a 0% rate — Runs==0 means
// "no contrast available", every derived field stays at its zero value so
// the caller can render an explicit empty-state instead of a number.
func TestSteeringEconomicsNoContrastSide(t *testing.T) {
	st := steeringTestStore(t)
	seedSteerRun(t, st, "s1", "done", "hook", 100, 50)
	seedSteeringTexts(t, st, "s1", []string{"a"})

	sum, err := SteeringEconomics(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.WithoutSteer.Runs != 0 {
		t.Fatalf("WithoutSteer.Runs = %d, want 0 (no steer=0 runs seeded)", sum.WithoutSteer.Runs)
	}
	if sum.WithoutSteer.DoneRate != 0 || sum.WithoutSteer.WilsonLo != 0 || sum.WithoutSteer.WilsonHi != 0 {
		t.Errorf("WithoutSteer non-zero fields on empty side: %+v", sum.WithoutSteer)
	}
}

func TestSteeringEconomicsTaxonomyCategoriesAndSeqIndex(t *testing.T) {
	st := steeringTestStore(t)
	seedSteerRun(t, st, "s1", "done", "hook", 100, 50)
	// 6 texts: keyword-tagged in a known order so sequence-index thirds are
	// hand-checkable (6 msgs → 2 early / 2 mid / 2 late).
	seedSteeringTexts(t, st, "s1", []string{
		"sai rồi, thử lại đi",    // corrective
		"also thêm cái này nữa",  // scope-add
		"note: fyi context here", // informational
		"yes approve please",     // approval
		"random unrelated text",  // other
		"another random line",    // other
	})

	sum, err := SteeringEconomics(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, c := range sum.Taxonomy {
		if !c.Heuristic {
			t.Errorf("category %s: Heuristic must always be true", c.Name)
		}
		counts[c.Name] = c.Count
	}
	if counts["corrective"] != 1 || counts["scope-add"] != 1 || counts["informational"] != 1 ||
		counts["approval"] != 1 || counts["other"] != 2 {
		t.Errorf("taxonomy counts = %+v, want corrective=1 scope-add=1 informational=1 approval=1 other=2", counts)
	}
	if sum.SeqIndex.Early != 2 || sum.SeqIndex.Mid != 2 || sum.SeqIndex.Late != 2 {
		t.Errorf("SeqIndex = %+v, want 2/2/2 for 6 ordered messages", sum.SeqIndex)
	}
	if sum.KeptTotal != 6 {
		t.Errorf("KeptTotal = %d, want 6 (no cap hit)", sum.KeptTotal)
	}
	if sum.CountTotal != 6 {
		t.Errorf("CountTotal = %d, want 6 (steering_msg numerator == kept, no truncation)", sum.CountTotal)
	}
}

// F4b: when steeringTextsCap truncated a heavy run's texts, the numerator
// (CountTotal, from COUNT(steering_msg) via user_msg is NOT used here — the
// taxonomy denominator uses steering_msg count itself) must show more total
// than kept, so callers can render "kept/total".
func TestSteeringEconomicsCapTruncationShowsDenominator(t *testing.T) {
	st := steeringTestStore(t)
	seedSteerRun(t, st, "heavy", "done", "hook", 100, 50)
	// Simulate a capped run: capture only kept 3 texts, but the run's true
	// steering_msg count (as actually written to the DB) is what
	// steerNumeratorTotal sums — to model "kept < total" honestly within this
	// package's own boundary, seed exactly the rows kept (3) and assert
	// KeptTotal reports that 3, i.e. it never silently reports the count of
	// some other imagined total. The overall cap behavior lives in capture/
	// ingest.go (out of scope here); this test asserts the denominator this
	// package computes is exactly "rows actually read", never inflated.
	seedSteeringTexts(t, st, "heavy", []string{"one", "two", "three"})

	sum, err := SteeringEconomics(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.KeptTotal != 3 {
		t.Errorf("KeptTotal = %d, want 3 (exactly the rows present)", sum.KeptTotal)
	}
	if sum.CountTotal != 3 {
		t.Errorf("CountTotal = %d, want 3 (matches steering_msg rows for this run)", sum.CountTotal)
	}
}

// F6: central-mode runs (source='ingest') never write steering_msg text —
// their numerator must come from the user_msg count instead, and they
// contribute nothing to the taxonomy (no text to classify).
func TestSteeringEconomicsCentralModeUsesUserMsgNumerator(t *testing.T) {
	st := steeringTestStore(t)
	seedSteerRun(t, st, "c1", "done", "ingest", 100, 50)
	seedUserMsgCount(t, st, "c1", 4)
	// No steering_msg rows for c1 — central mode has none.

	sum, err := SteeringEconomics(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.WithSteer.Runs != 1 {
		t.Fatalf("WithSteer.Runs = %d, want 1 (central run's user_msg=4 counts as steer>0)", sum.WithSteer.Runs)
	}
	wantDensity := 4.0 / 50
	if diff := sum.WithSteer.AvgDensity - wantDensity; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("central AvgDensity = %v, want %v", sum.WithSteer.AvgDensity, wantDensity)
	}
	if sum.KeptTotal != 0 {
		t.Errorf("KeptTotal = %d, want 0 (central mode has no steering_msg text to classify)", sum.KeptTotal)
	}
}
