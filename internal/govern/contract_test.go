package govern

import "testing"

// TestCheckFailureModesCoversContract asserts every check point named in the
// spec (engine chain + dispatch layer) is classified, and classified as the
// mode the surrounding code actually implements. This file is mostly
// documentation — the map doesn't drive control flow — but a missing or
// wrong entry here means the contract and the code have drifted apart.
func TestCheckFailureModesCoversContract(t *testing.T) {
	cases := []struct {
		check string
		want  FailureMode
	}{
		{"kill", FailClosed},
		{"sandbox", FailClosed},
		{"rules-load", FailClosed},
		{"block", FailClosed},
		{"secrets", FailClosed},
		{"budget", FailClosed},
		{"risk", FailClosed},
		{"gate", FailClosed},
		{"hook-input-decode", FailOpen},
		{"store-open", FailClosedMutating},
		{"pre-tool-ingest", FailClosedMutating},
		{"config-load", FailOpen},
		{"central-no-snapshot", FailClosedMutating},
		{"capture-post-tool", FailOpen},
		{"capture-stop", FailOpen},
		{"capture-event-append", FailOpen},
	}
	if len(CheckFailureModes) != len(cases) {
		t.Fatalf("CheckFailureModes has %d entries, test covers %d — keep them in sync",
			len(CheckFailureModes), len(cases))
	}
	for _, c := range cases {
		got, ok := CheckFailureModes[c.check]
		if !ok {
			t.Errorf("CheckFailureModes missing entry for %q", c.check)
			continue
		}
		if got != c.want {
			t.Errorf("CheckFailureModes[%q] = %v, want %v", c.check, got, c.want)
		}
	}
}

// TestFailureModeClassification asserts the three modes are distinct values
// and FailClosed (the strictest, before-side-effect, no-relaxation mode) is
// the zero value — a new check point added without an explicit entry in
// CheckFailureModes must not silently behave as FailOpen.
func TestFailureModeClassification(t *testing.T) {
	if FailClosed != 0 {
		t.Errorf("FailClosed must be the zero value (strictest default), got %d", FailClosed)
	}
	modes := map[FailureMode]bool{FailClosed: true, FailClosedMutating: true, FailOpen: true}
	if len(modes) != 3 {
		t.Errorf("expected 3 distinct FailureMode values, got %d", len(modes))
	}
}
