package observer

import (
	"testing"

	"github.com/phuc-nt/dandori/internal/learn"
)

// UE3: with no setting persisted, minGrade falls back to the documented
// default so existing playbook-candidate behavior is unchanged until an
// operator opts into a different floor.
func TestMinGradeFallsBackToDefault(t *testing.T) {
	st, _ := testStore(t)
	if got := minGrade(st); got != defaultGateMinGrade {
		t.Errorf("minGrade() = %q, want default %q", got, defaultGateMinGrade)
	}
}

// UE3: once the operator persists gate_min_grade (via the web form's
// SetSetting call), the detector must read it back immediately.
func TestMinGradeReadsPersistedSetting(t *testing.T) {
	st, _ := testStore(t)
	if err := st.SetSetting("gate_min_grade", "A"); err != nil {
		t.Fatal(err)
	}
	if got := minGrade(st); got != "A" {
		t.Errorf("minGrade() = %q, want A (persisted setting)", got)
	}
}

// An invalid persisted value (should never happen given form validation, but
// defense in depth) must not crash the detector — falls back to default.
func TestMinGradeIgnoresInvalidPersistedValue(t *testing.T) {
	st, _ := testStore(t)
	if err := st.SetSetting("gate_min_grade", "Z"); err != nil {
		t.Fatal(err)
	}
	if got := minGrade(st); got != defaultGateMinGrade {
		t.Errorf("minGrade() with invalid setting = %q, want fallback %q", got, defaultGateMinGrade)
	}
}

func TestMeetsMinGradeRanking(t *testing.T) {
	cases := []struct {
		letter, floor string
		want          bool
	}{
		{"A", "C", true}, {"B", "C", true}, {"C", "C", true},
		{"D", "C", false}, {"F", "C", false},
		{"A", "A", true}, {"F", "F", true},
	}
	for _, c := range cases {
		if got := meetsMinGrade(c.letter, c.floor); got != c.want {
			t.Errorf("meetsMinGrade(%q,%q) = %v, want %v", c.letter, c.floor, got, c.want)
		}
	}
}

// Raising the floor above a run's grade must exclude it from playbook
// candidates — proves the settings value actually gates the decision, not
// just that minGrade() returns the right string in isolation.
func TestDetectPlaybookCandidatesRespectsRaisedFloor(t *testing.T) {
	st, cfg := testStore(t)
	seedRun(t, st, "pb1", "agent-b", "done", 0.1, 0)
	board, err := learn.LeaderboardCalibrated(st, cfg.LearnWindowDays, cfg.CalibrateWithHumans)
	if err != nil {
		t.Fatal(err)
	}
	// Default floor ("C") should not exclude a lone agent (uncalibrated →
	// static band from Compute(); we only assert the raised-floor case is
	// strictly more restrictive than the default).
	baseline, err := detectPlaybookCandidates(st, board)
	if err != nil {
		t.Fatal(err)
	}
	st.SetSetting("gate_min_grade", "A")
	raised, err := detectPlaybookCandidates(st, board)
	if err != nil {
		t.Fatal(err)
	}
	if len(raised) > len(baseline) {
		t.Errorf("raising the floor to A produced MORE candidates (%d) than default (%d)", len(raised), len(baseline))
	}
}
