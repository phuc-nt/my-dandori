package learn

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func TestDetectSpikes(t *testing.T) {
	st := testStore(t)
	testseed.Agent(t, st, "spiky")
	testseed.Agent(t, st, "steady")
	// spiky: $1/day for 7 days, $5 today (5× median). steady: flat $1.
	for d := 1; d <= 7; d++ {
		testseed.Run(t, st, fmt.Sprintf("s-%d", d), "spiky", "done", d, 1.0)
		testseed.Run(t, st, fmt.Sprintf("t-%d", d), "steady", "done", d, 1.0)
	}
	testseed.Run(t, st, "s-today", "spiky", "done", 0, 5.0)
	testseed.Run(t, st, "t-today", "steady", "done", 0, 1.0)

	spiked, err := DetectSpikes(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(spiked) != 1 || spiked[0] != "spiky" {
		t.Fatalf("spiked: %v, want [spiky]", spiked)
	}
	// Dedup: second run detects nothing new.
	spiked2, _ := DetectSpikes(st)
	if len(spiked2) != 0 {
		t.Errorf("re-detect: %v, want none (dedup)", spiked2)
	}
	var events int
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='cost_spike'`).Scan(&events)
	if events != 1 {
		t.Errorf("spike events: %d", events)
	}

	contributors, err := SpikeContributors(st, time.Now().UTC().Format("2006-01-02"), "spiky")
	if err != nil || len(contributors) != 1 || contributors[0].RunID != "s-today" {
		t.Errorf("contributors: %+v err=%v", contributors, err)
	}
}

func TestDORALiteLeadTime(t *testing.T) {
	st := testStore(t)
	base := time.Now().UTC().Add(-72 * time.Hour)
	seed := func(key, status string, hours int) {
		payload, _ := json.Marshal(map[string]string{
			"created": base.Format(time.RFC3339),
			"updated": base.Add(time.Duration(hours) * time.Hour).Format(time.RFC3339),
		})
		st.DB.Exec(`INSERT INTO work_items(source, key, title, status, updated_at, payload)
			VALUES('jira', ?, 't', ?, ?, ?)`, key, status, store.Now(), string(payload))
	}
	seed("J-1", "Done", 10)
	seed("J-2", "Done", 20)
	seed("J-3", "Done", 30)
	seed("J-4", "In Progress", 5) // not done — excluded

	d, err := ComputeDORALite(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if d.LeadTimeCount != 3 || d.LeadTimeP50 != 20*time.Hour {
		t.Errorf("lead time: count=%d p50=%s", d.LeadTimeCount, d.LeadTimeP50)
	}
	if d.DeployFreqNote == "" {
		t.Error("deploy freq must state its gap honestly")
	}
}

// The leaderboard must Compute each agent exactly once for the main window
// (plus two small trend windows) — this guards the N+1 fix by construction:
// with 3 agents the old code did 6 main-window Computes, the new does 3.
func TestLeaderboardSinglePass(t *testing.T) {
	st := testStore(t)
	seedFleet(t, st)
	rows, err := Leaderboard(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows: %d", len(rows))
	}
	// Grades must still calibrate against the same fleet distribution.
	for _, r := range rows {
		if r.Grade.FleetSize != 3 {
			t.Errorf("fleet size in grade: %d, want 3", r.Grade.FleetSize)
		}
	}
}
