package learn

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

func funnelTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "funnel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// fixedNow anchors every seeded timestamp in this test file to a constant
// instant rather than time.Now() (F15): a stale/aged fixture must stay stale
// no matter when the test suite actually runs.
var fixedNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// seedApprovalRow inserts one approvals row. decidedAfterMin<0 means
// decided_at stays NULL (still pending). requestedAgoMin anchors
// requested_at relative to fixedNow, not time.Now().
func seedApprovalRow(t *testing.T, st *store.Store, action, status string, requestedAgoMin, decidedAfterMin int) {
	t.Helper()
	requested := fixedNow.Add(-time.Duration(requestedAgoMin) * time.Minute)
	var decidedAt any
	if decidedAfterMin >= 0 {
		decidedAt = requested.Add(time.Duration(decidedAfterMin) * time.Minute).Format(time.RFC3339)
	}
	_, err := st.DB.Exec(`INSERT INTO approvals(run_id, action, status, requested_at, decided_at)
		VALUES('r', ?, ?, ?, ?)`, action, status, requested.Format(time.RFC3339), decidedAt)
	if err != nil {
		t.Fatal(err)
	}
}

func TestApprovalFunnelStageCounts(t *testing.T) {
	st := funnelTestStore(t)
	seedApprovalRow(t, st, "run-shell", "approved", 100, 10)
	seedApprovalRow(t, st, "run-shell", "rejected", 100, 20)
	seedApprovalRow(t, st, "run-shell", "expired", 100, 90)
	seedApprovalRow(t, st, "run-shell", "pending", 5, -1)

	res, err := ApprovalFunnel(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := FunnelStages{Requested: 4, Approved: 1, Rejected: 1, Expired: 1, Pending: 1}
	if res.Stages != want {
		t.Errorf("Stages = %+v, want %+v", res.Stages, want)
	}
}

// Known latency values → hand-checkable median/p90, restricted to
// approved/rejected only (F2).
func TestApprovalFunnelLatencyApprovedRejectedOnly(t *testing.T) {
	st := funnelTestStore(t)
	// run-shell latencies (minutes): 10, 20, 30, 40, 50 → median=30.
	seedApprovalRow(t, st, "run-shell", "approved", 100, 10)
	seedApprovalRow(t, st, "run-shell", "approved", 100, 20)
	seedApprovalRow(t, st, "run-shell", "rejected", 100, 30)
	seedApprovalRow(t, st, "run-shell", "approved", 100, 40)
	seedApprovalRow(t, st, "run-shell", "rejected", 100, 50)
	// An expired row with a huge "latency" must NEVER leak into run-shell's
	// stats (F2) — if it did, median/p90 would blow up.
	seedApprovalRow(t, st, "run-shell", "expired", 1000, 900)

	res, err := ApprovalFunnel(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ByAction) != 1 {
		t.Fatalf("ByAction = %+v, want exactly 1 action", res.ByAction)
	}
	al := res.ByAction[0]
	if al.Action != "run-shell" || al.Count != 5 {
		t.Fatalf("ByAction[0] = %+v, want action=run-shell count=5", al)
	}
	// julianday() arithmetic carries sub-millisecond float noise, so compare
	// with a generous tolerance rather than exact equality.
	if diff := al.MedianMin - 30; diff > 0.01 || diff < -0.01 {
		t.Errorf("MedianMin = %v, want ~30", al.MedianMin)
	}
	// nearest-rank p90 of [10,20,30,40,50], n=5: idx=ceil(0.9*5)=5 → sorted[4]=50.
	if diff := al.P90Min - 50; diff > 0.01 || diff < -0.01 {
		t.Errorf("P90Min = %v, want ~50", al.P90Min)
	}
	if !res.HasHumanDecisions {
		t.Error("HasHumanDecisions = false, want true (approved+rejected>0)")
	}
}

// F2: an all-expired fleet (today's live state — 9/9 decided rows are expiry
// sweeps) must yield empty latency and HasHumanDecisions=false, so callers
// render the "chưa có quyết định người, chỉ có expiry sweep" empty-state
// instead of publishing sweep timestamps as human latency.
func TestApprovalFunnelAllExpiredIsEmptyState(t *testing.T) {
	st := funnelTestStore(t)
	seedApprovalRow(t, st, "run-shell", "expired", 100, 90)
	seedApprovalRow(t, st, "run-shell", "expired", 200, 190)
	seedApprovalRow(t, st, "band-demote:x", "expired", 50, 45)

	res, err := ApprovalFunnel(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.HasHumanDecisions {
		t.Error("HasHumanDecisions = true, want false (all decided rows are expiry sweeps)")
	}
	if len(res.ByAction) != 0 {
		t.Errorf("ByAction = %+v, want empty (no approved/rejected rows)", res.ByAction)
	}
	if res.Stages.Expired != 3 || res.Stages.Approved != 0 || res.Stages.Rejected != 0 {
		t.Errorf("Stages = %+v, want Expired=3 Approved=0 Rejected=0", res.Stages)
	}
}

// F15: a "stale/aged" fixture must be seeded at a timestamp fixed relative to
// the SQL window boundary itself, not at whatever time.Now() is when the
// suite happens to run — insightWindowClauseCol's " >= datetime('now','-N
// day')" is real wall-clock SQL, so the only reliable way to prove the
// window excludes an old row is to seed it far outside any window
// (100 days back) and prove a 7-day window drops it while an all-time query
// (days<=0) still finds it. That comparison holds no matter when this test
// executes — it never depends on capturing time.Now() in Go and asserting
// against it.
func TestApprovalFunnelWindowFiltersByRequestedAt(t *testing.T) {
	st := funnelTestStore(t)
	oldRequested := time.Now().UTC().AddDate(0, 0, -100)
	if _, err := st.DB.Exec(`INSERT INTO approvals(run_id, action, status, requested_at, decided_at)
		VALUES('r', 'run-shell', 'approved', ?, ?)`,
		oldRequested.Format(time.RFC3339), oldRequested.Add(10*time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	windowed, err := ApprovalFunnel(st, 7)
	if err != nil {
		t.Fatal(err)
	}
	if windowed.Stages.Requested != 0 {
		t.Errorf("7-day window Requested = %d, want 0 (row is 100 days old)", windowed.Stages.Requested)
	}

	allTime, err := ApprovalFunnel(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if allTime.Stages.Requested != 1 {
		t.Errorf("all-time Requested = %d, want 1 (days<=0 means no window filter)", allTime.Stages.Requested)
	}
}

func TestApprovalFunnelEmptyStoreNoRows(t *testing.T) {
	st := funnelTestStore(t)
	res, err := ApprovalFunnel(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stages != (FunnelStages{}) {
		t.Errorf("Stages = %+v, want zero value", res.Stages)
	}
	if res.HasHumanDecisions {
		t.Error("HasHumanDecisions = true on empty store, want false")
	}
	if len(res.ByAction) != 0 {
		t.Errorf("ByAction = %+v, want empty", res.ByAction)
	}
}
