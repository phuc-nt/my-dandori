package learn

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func seedHumanItems(t *testing.T, st *store.Store, assignee string, done, notDone int) {
	t.Helper()
	i := 0
	for ; i < done; i++ {
		testseed.WorkItem(t, st, "jira", assignee+"-d"+string(rune('0'+i)), "Done")
		st.DB.Exec(`UPDATE work_items SET assignee = ? WHERE key = ?`, assignee, assignee+"-d"+string(rune('0'+i)))
	}
	for j := 0; j < notDone; j++ {
		testseed.WorkItem(t, st, "jira", assignee+"-o"+string(rune('0'+j)), "To Do")
		st.DB.Exec(`UPDATE work_items SET assignee = ? WHERE key = ?`, assignee, assignee+"-o"+string(rune('0'+j)))
	}
	_ = i
}

func TestHumanBaseline(t *testing.T) {
	st := testStore(t)
	seedHumanItems(t, st, "alice", 3, 1) // 75%
	seedHumanItems(t, st, "bob", 2, 0)   // only 2 items → excluded

	vals, err := HumanBaseline(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 1 || vals[0] != 75 {
		t.Errorf("baseline: %v, want [75]", vals)
	}
}

func TestLeaderboardIncludesHumanBaseline(t *testing.T) {
	st := testStore(t)
	seedFleet(t, st) // 3 agents
	seedHumanItems(t, st, "alice", 4, 0)
	seedHumanItems(t, st, "carol", 3, 1)

	rows, err := LeaderboardCalibrated(st, 30, true)
	if err != nil {
		t.Fatal(err)
	}
	// 3 agents + 2 humans = fleet 5 → calibrated (minFleet reached).
	for _, r := range rows {
		if r.Grade.FleetSize != 5 || r.Grade.Humans != 2 {
			t.Errorf("%s: fleet=%d humans=%d, want 5/2", r.AgentID, r.Grade.FleetSize, r.Grade.Humans)
		}
		if r.Grade.Uncalibrated {
			t.Errorf("%s: fleet of 5 must be calibrated", r.AgentID)
		}
		if !r.Grade.LowConfidence {
			t.Errorf("%s: %d runs < 5 must flag low confidence", r.AgentID, r.Runs)
		}
	}
	// Toggle off → agents only.
	rows2, _ := LeaderboardCalibrated(st, 30, false)
	if rows2[0].Grade.FleetSize != 3 || rows2[0].Grade.Humans != 0 {
		t.Errorf("without humans: fleet=%d humans=%d", rows2[0].Grade.FleetSize, rows2[0].Grade.Humans)
	}
}

func TestAIReviewCacheAndFailSafe(t *testing.T) {
	st := testStore(t)
	seedFleet(t, st)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte(`{"choices":[{"message":{"content":"Solid agent; watch reliability."}}]}`))
	}))
	defer srv.Close()

	a := NewAIReviewer(st, "test-key", "test-model")
	a.BaseURL = srv.URL
	if got := a.Review("a1", 30); got != "Solid agent; watch reliability." {
		t.Fatalf("review: %q", got)
	}
	a.Review("a1", 30) // cached — no second HTTP call
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("upstream calls: %d, want 1 (weekly cache)", calls)
	}
	// No key → silent empty, zero calls.
	b := NewAIReviewer(st, "", "m")
	b.BaseURL = srv.URL
	if got := b.Review("a2", 30); got != "" {
		t.Errorf("no-key review must be empty: %q", got)
	}
}
