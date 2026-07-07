package learn

import (
	"fmt"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedContextRun inserts a run (done, ended) with an optional user_msg
// (steering) count, mirroring seedInsightRun's shape but adding ended_at
// (ContextROI requires ended_at IS NOT NULL) and a steering event.
func seedContextRun(t *testing.T, st *store.Store, id, project, agent, status string, cost float64, steering int) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?,?,datetime('now'))
		ON CONFLICT(id) DO NOTHING`, agent, agent); err != nil {
		t.Fatal(err)
	}
	_, err := st.DB.Exec(`INSERT INTO runs
		(id, session_id, project, agent_id, status, started_at, ended_at, cost_usd)
		VALUES(?,?,?,?,?,datetime('now'),datetime('now'),?)`,
		id, id, project, agent, status, cost)
	if err != nil {
		t.Fatal(err)
	}
	// Local-mode (source='hook') steering numerator = COUNT(steering_msg) rows
	// (F6). Seed one steering_msg event per unit so context_roi's per-run
	// count matches, mirroring how the capture layer records mid-run messages.
	for i := 0; i < steering; i++ {
		if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, datetime('now'), 'steering_msg', ?)`,
			id, fmt.Sprintf("msg %d", i)); err != nil {
			t.Fatal(err)
		}
	}
}

// seedContextInjected inserts one context_injected event for a run with the
// given company-layer version. Multiple calls for the same run simulate
// startup+resume duplicates or mid-run bumps, depending on the version.
func seedContextInjected(t *testing.T, st *store.Store, runID string, companyVersion int) {
	t.Helper()
	payload := fmt.Sprintf(`{"company":%d}`, companyVersion)
	if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, datetime('now'), 'context_injected', ?)`,
		runID, payload); err != nil {
		t.Fatal(err)
	}
}

func TestHasContextData(t *testing.T) {
	st := insightTestStore(t)
	has, err := HasContextData(st)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("empty store should report HasContextData=false")
	}

	seedContextRun(t, st, "r1", "proj", "a1", "done", 1, 0)
	seedContextInjected(t, st, "r1", 1)

	has, err = HasContextData(st)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("store with 1 context_injected row should report HasContextData=true")
	}
}

func TestContextROIEmpty(t *testing.T) {
	st := insightTestStore(t)
	stats, err := ContextROI(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Errorf("empty store should yield no rows, got %d", len(stats))
	}
}

// TestContextROIResumeDuplicate: a run fires context_injected on BOTH
// startup and resume (same version). F5 dedupe must count the run once,
// not twice.
func TestContextROIResumeDuplicate(t *testing.T) {
	st := insightTestStore(t)
	seedContextRun(t, st, "r1", "proj", "a1", "done", 1, 0)
	seedContextInjected(t, st, "r1", 1) // startup
	seedContextInjected(t, st, "r1", 1) // resume, same version

	stats, err := ContextROI(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("want 1 bucket, got %d: %+v", len(stats), stats)
	}
	if stats[0].Runs != 1 {
		t.Errorf("resume-duplicate must dedupe to 1 run, got %d", stats[0].Runs)
	}
}

// TestContextROIMidRunBump: a run fires context_injected TWICE with
// DIFFERENT versions in the same run (a mid-run bump). F5 requires the run
// land in the FIRST version's bucket only, never both.
func TestContextROIMidRunBump(t *testing.T) {
	st := insightTestStore(t)
	seedContextRun(t, st, "r1", "proj", "a1", "done", 1, 0)
	seedContextInjected(t, st, "r1", 1) // first injection (startup) — v1
	seedContextInjected(t, st, "r1", 2) // mid-run bump — v2 (must be ignored)

	stats, err := ContextROI(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("want 1 bucket (run counted once, in v1 only), got %d: %+v", len(stats), stats)
	}
	if stats[0].VersionN != 1 {
		t.Errorf("run must land in FIRST version (1), got version %d", stats[0].VersionN)
	}
	if stats[0].Runs != 1 {
		t.Errorf("run must be counted exactly once, got %d", stats[0].Runs)
	}
}

// TestContextVersionDeltaArithmetic seeds 2 versions x >=3 runs/side with
// known done/cost/steering counts and checks the delta matches hand
// arithmetic.
func TestContextVersionDeltaArithmetic(t *testing.T) {
	st := insightTestStore(t)
	// v1: 4 runs, 2 done, cost 1 each (total 4, cost/done=2), steering 1 each (total 4)
	statuses1 := []string{"done", "done", "failed", "failed"}
	for i, status := range statuses1 {
		id := fmt.Sprintf("v1-%d", i)
		seedContextRun(t, st, id, "proj", "a1", status, 1, 1)
		seedContextInjected(t, st, id, 1)
	}
	// v2: 5 runs, 4 done, cost 2 each (total 10, cost/done=2.5), steering 0 each
	statuses2 := []string{"done", "done", "done", "done", "failed"}
	for i, status := range statuses2 {
		id := fmt.Sprintf("v2-%d", i)
		seedContextRun(t, st, id, "proj", "a1", status, 2, 0)
		seedContextInjected(t, st, id, 2)
	}

	ready, insufficient, err := ContextROIPairs(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(insufficient) != 0 {
		t.Errorf("both sides have >=3 runs, want 0 insufficient pairs, got %d: %+v", len(insufficient), insufficient)
	}
	if len(ready) != 1 {
		t.Fatalf("want 1 ready pair, got %d", len(ready))
	}
	d := ready[0]
	if d.Layer != "company" || d.Target != "proj" || d.FromV != 1 || d.ToV != 2 {
		t.Fatalf("pair identity wrong: %+v", d)
	}
	if d.RunsFrom != 4 || d.RunsTo != 5 {
		t.Errorf("run counts wrong: from=%d to=%d", d.RunsFrom, d.RunsTo)
	}
	if d.DoneRateFrom != 0.5 {
		t.Errorf("DoneRateFrom = %v, want 0.5", d.DoneRateFrom)
	}
	if d.DoneRateTo != 0.8 {
		t.Errorf("DoneRateTo = %v, want 0.8", d.DoneRateTo)
	}
	if d.CostPerDoneFrom != 2.0 {
		t.Errorf("CostPerDoneFrom = %v, want 2.0", d.CostPerDoneFrom)
	}
	if d.CostPerDoneTo != 2.5 {
		t.Errorf("CostPerDoneTo = %v, want 2.5", d.CostPerDoneTo)
	}
	if d.SteeringRateFrom != 1.0 {
		t.Errorf("SteeringRateFrom = %v, want 1.0", d.SteeringRateFrom)
	}
	if d.SteeringRateTo != 0.0 {
		t.Errorf("SteeringRateTo = %v, want 0.0", d.SteeringRateTo)
	}
	if d.NoContrast {
		t.Error("both sides have data, NoContrast should be false")
	}
	// Wilson CI sanity: lo <= point <= hi (in whole percent terms).
	if d.DoneRateFromLo > 50 || d.DoneRateFromHi < 50 {
		t.Errorf("Wilson CI for v1 done-rate should bracket 50%%: lo=%d hi=%d", d.DoneRateFromLo, d.DoneRateFromHi)
	}
}

// TestContextROIInsufficientSide: one version has n<MinSampleForInsight —
// the pair must be routed to "insufficient", not "ready".
func TestContextROIInsufficientSide(t *testing.T) {
	st := insightTestStore(t)
	// v1: 1 run only (below MinSampleForInsight=3)
	seedContextRun(t, st, "v1-0", "proj", "a1", "done", 1, 0)
	seedContextInjected(t, st, "v1-0", 1)
	// v2: 3 runs (sufficient)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("v2-%d", i)
		seedContextRun(t, st, id, "proj", "a1", "done", 1, 0)
		seedContextInjected(t, st, id, 2)
	}

	ready, insufficient, err := ContextROIPairs(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Errorf("v1 side has n=1 < MinSample, want 0 ready pairs, got %d", len(ready))
	}
	if len(insufficient) != 1 {
		t.Fatalf("want 1 insufficient pair, got %d", len(insufficient))
	}
	if insufficient[0].RunsFrom != 1 || insufficient[0].RunsTo != 3 {
		t.Errorf("insufficient pair run counts wrong: %+v", insufficient[0])
	}
}

// TestContextROICentralModeUsesUserMsgNumerator locks the cross-phase
// consistency invariant with steering_econ.go (F6): a central-mode run
// (source='ingest') has no steering_msg text, so context-ROI's steering
// numerator must fall back to the run's user_msg count, exactly like
// SteeringEconomics does for the same run shape.
func TestContextROICentralModeUsesUserMsgNumerator(t *testing.T) {
	st := insightTestStore(t)
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a1','a1',datetime('now'))
		ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO runs
		(id, session_id, project, agent_id, status, started_at, ended_at, cost_usd, source)
		VALUES('c1','c1','proj','a1','done',datetime('now'),datetime('now'),1,'ingest')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES('c1', datetime('now'), 'user_msg', '4')`); err != nil {
		t.Fatal(err)
	}
	seedContextInjected(t, st, "c1", 1)

	stats, err := ContextROI(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("want 1 bucket, got %d: %+v", len(stats), stats)
	}
	if stats[0].Steering != 4 {
		t.Errorf("central-mode steering numerator = %d, want 4 (from user_msg count, not steering_msg)", stats[0].Steering)
	}
}

// TestContextROIAgentLayerGrain checks the agent-layer target column uses
// agent_id rather than project.
func TestContextROIAgentLayerGrain(t *testing.T) {
	st := insightTestStore(t)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("a-%d", i)
		seedContextRun(t, st, id, "proj", "agent-x", "done", 1, 0)
		if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, datetime('now'), 'context_injected', ?)`,
			id, `{"agent":1}`); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := ContextROI(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range stats {
		if s.Layer == "agent" {
			found = true
			if s.Target != "agent-x" {
				t.Errorf("agent-layer target should be agent_id, got %q", s.Target)
			}
			if s.Runs != 3 {
				t.Errorf("agent-layer runs = %d, want 3", s.Runs)
			}
		}
	}
	if !found {
		t.Fatal("expected an agent-layer bucket")
	}
}
