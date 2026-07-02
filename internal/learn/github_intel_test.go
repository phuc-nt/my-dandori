package learn

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

func seedPR(t *testing.T, st *store.Store, number int, mergedAt string, revertOf, revertedBy int) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"number": number, "author": "phuc",
		"createdAt": time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339),
		"mergedAt":  mergedAt, "revert_of": revertOf, "reverted_by": revertedBy,
	})
	key := "r#" + string(rune('0'+number))
	_, err := st.DB.Exec(`INSERT INTO work_items(source, key, title, status, updated_at, payload)
		VALUES('github', ?, 'pr', 'MERGED', ?, ?)`, key, store.Now(), string(payload))
	if err != nil {
		t.Fatal(err)
	}
}

func TestCFRHandChecked(t *testing.T) {
	st := testStore(t)
	now := time.Now().UTC().Format(time.RFC3339)
	// 5 merged changes, #3 was reverted by #6 (a merged revert PR).
	seedPR(t, st, 1, now, 0, 0)
	seedPR(t, st, 2, now, 0, 0)
	seedPR(t, st, 3, now, 0, 6)
	seedPR(t, st, 4, now, 0, 0)
	seedPR(t, st, 5, now, 0, 0)
	seedPR(t, st, 6, now, 3, 0) // the revert PR — excluded from denominator

	m, err := CFR(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if m.Value != 20 {
		t.Errorf("CFR = %v (%s), want 20", m.Value, m.Formula)
	}
	if len(m.RunIDs) != 5 {
		t.Errorf("provenance keys: %d, want 5 merged PRs", len(m.RunIDs))
	}
}

func TestPRCyclePercentiles(t *testing.T) {
	st := testStore(t)
	base := time.Now().UTC().Add(-24 * time.Hour)
	for i, hours := range []int{1, 2, 3, 4} { // cycle times 1h..4h
		payload, _ := json.Marshal(map[string]any{
			"number": i + 1, "author": "p",
			"createdAt": base.Format(time.RFC3339),
			"mergedAt":  base.Add(time.Duration(hours) * time.Hour).Format(time.RFC3339),
		})
		st.DB.Exec(`INSERT INTO work_items(source, key, title, status, updated_at, payload)
			VALUES('github', ?, 'pr', 'MERGED', ?, ?)`, "c#"+string(rune('0'+i)), store.Now(), string(payload))
	}
	r, err := PRCycle(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if r.Count != 4 || r.P50 != 2*time.Hour || r.P75 != 3*time.Hour {
		t.Errorf("cycle: count=%d p50=%s p75=%s", r.Count, r.P50, r.P75)
	}
}

func TestAttributionAggregates(t *testing.T) {
	st := testStore(t)
	seedFleet(t, st)
	st.DB.Exec(`UPDATE runs SET lines_added = 10, lines_deleted = 2 WHERE agent_id = 'a1'`)
	st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES('a1-r1', ?, 'revert_detected', 'git', 0, 'abc123')`, store.Now())

	rows, err := Attribution(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	var a1 *AttributionRow
	for i := range rows {
		if rows[i].AgentID == "a1" {
			a1 = &rows[i]
		}
	}
	if a1 == nil || a1.LinesAdded != 20 || a1.Runs != 2 || a1.RevertedRuns != 1 {
		t.Errorf("a1 attribution: %+v", a1)
	}
}
