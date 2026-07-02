package observer

import (
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// Over-steering must land on the OPERATOR surface with the coaching caveat —
// never in the CEO one-click inbox, never as a ranking.
func TestOverSteeringIsPrivateOperatorSurface(t *testing.T) {
	st, cfg := testStore(t)
	st.ResolveOperator("alice@mac")
	for i := 0; i < 12; i++ {
		id := "os" + itoa(i)
		seedRun(t, st, id, "agent-a", "done", 0.1, 0)
		st.DB.Exec(`UPDATE runs SET operator_id = 'alice@mac' WHERE id = ?`, id)
		st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, ?, 'user_msg', '7')`, id, store.Now())
	}
	res, err := RunObserver(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Surfaced == 0 {
		t.Fatal("over-steering did not surface")
	}
	var surface, summary string
	err = st.DB.QueryRow(`SELECT surface, summary FROM insights WHERE type='operator_over_steering'`).
		Scan(&surface, &summary)
	if err != nil {
		t.Fatal("insight missing:", err)
	}
	if surface != "operator" {
		t.Errorf("surface: %s, want operator (private coaching)", surface)
	}
	if !strings.Contains(summary, "ĐÚNG") {
		t.Errorf("summary lacks the steering-can-be-healthy caveat: %q", summary)
	}
	// The CEO inbox filter must exclude it.
	var inCEO int
	st.DB.QueryRow(`SELECT count(*) FROM insights WHERE surface='ceo' AND type='operator_over_steering'`).Scan(&inCEO)
	if inCEO != 0 {
		t.Error("private coaching insight leaked to CEO surface")
	}
}

// A quiet fleet (low volume, average grades) produces no insights — the
// inbox must not fill with noise.
func TestDetectorsStaySilentOnQuietFleet(t *testing.T) {
	st, cfg := testStore(t)
	seedRun(t, st, "q1", "agent-a", "done", 0.1, 1)
	seedRun(t, st, "q2", "agent-a", "failed", 0.1, 2)
	res, err := RunObserver(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Proposed != 0 {
		t.Errorf("quiet fleet proposed approvals: %+v", res.Details)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM insights WHERE type IN ('budget_overshoot_trend','agent_underused','operator_over_steering')`).Scan(&n)
	if n != 0 {
		t.Errorf("noise insights on quiet fleet: %d (%+v)", n, res.Details)
	}
}
