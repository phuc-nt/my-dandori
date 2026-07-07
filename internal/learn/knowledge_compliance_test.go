package learn

import (
	"testing"
)

// TestAgentComplianceStatuses covers the three install-status values
// AgentCompliance must distinguish for one mandated skill unit: an operator
// who installed the current unit id (ok), one who installed an OLDER unit id
// in the same (kind,name) lineage (stale), and one who ran at least once but
// never installed at all (missing).
func TestAgentComplianceStatuses(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "r-ok", "alice@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "r-stale", "bob@dev", "agent-a", "done", 0, ``, 0)
	seedBehaviorRun(t, st, "r-missing", "carol@dev", "agent-a", "done", 0, ``, 0)

	// v1 (superseded) then v2 (current, mandated) of the same skill lineage —
	// mirrors F5 versioning: only one LIVE row per (kind,name) at a time
	// (idx_ku_kind_name_live), so v1 must be superseded before v2 publishes.
	oldUnitID := nominateUnitForTest(t, st, KindSkill, "guarded-skill")
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET state = ? WHERE id = ?`, StateSuperseded, oldUnitID); err != nil {
		t.Fatal(err)
	}
	newUnitID := nominateUnitForTest(t, st, KindSkill, "guarded-skill")
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET required = 1 WHERE id = ?`, newUnitID); err != nil {
		t.Fatal(err)
	}

	if _, err := RecordUnitAdoption(st, newUnitID, "alice@dev", "", true, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordUnitAdoption(st, oldUnitID, "bob@dev", "", true, 30); err != nil {
		t.Fatal(err)
	}
	// carol never adopts — stays "missing".

	rows, err := AgentCompliance(st)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		if r.Name != "guarded-skill" {
			t.Errorf("unexpected unit in compliance rows: %+v", r)
			continue
		}
		got[r.OperatorID] = r.Status
	}
	want := map[string]string{
		"alice@dev": ComplianceOK,
		"bob@dev":   ComplianceStale,
		"carol@dev": ComplianceMissing,
	}
	for op, wantStatus := range want {
		if got[op] != wantStatus {
			t.Errorf("operator %s status = %q, want %q", op, got[op], wantStatus)
		}
	}
}

// A skill unit that is NOT mandated (required=0) must never appear in
// AgentCompliance's output — mandate is opt-in, not a default audit of every
// published skill (Goodhart avoidance: no ranking on unrequested skills).
func TestAgentComplianceSkipsNonMandatedUnits(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "r1", "dan@dev", "agent-a", "done", 0, ``, 0)
	nominateUnitForTest(t, st, KindSkill, "optional-skill") // required=0 by default

	rows, err := AgentCompliance(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Name == "optional-skill" {
			t.Errorf("non-mandated unit must not appear in compliance rows: %+v", r)
		}
	}
}

// AgentCompliance must never produce a ranking/score field — install-status
// text values only (Goodhart avoidance, F13's own doc comment). This is a
// compile-time/structural guard: AgentComplianceRow's Status must be one of
// the three constants for every row.
func TestAgentComplianceStatusIsAlwaysOneOfThreeValues(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "r1", "erin@dev", "agent-a", "done", 0, ``, 0)
	unitID := nominateUnitForTest(t, st, KindSkill, "checked-skill")
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET required = 1 WHERE id = ?`, unitID); err != nil {
		t.Fatal(err)
	}

	rows, err := AgentCompliance(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one compliance row")
	}
	for _, r := range rows {
		switch r.Status {
		case ComplianceOK, ComplianceStale, ComplianceMissing:
			// ok
		default:
			t.Errorf("unexpected status value %q — install-status only, no scores", r.Status)
		}
	}
}

// No mandated units and/or no known operators must degrade to an empty,
// error-free result rather than panicking or returning nil-with-error.
func TestAgentComplianceEmptyWhenNothingMandated(t *testing.T) {
	st := testStore(t)
	seedBehaviorRun(t, st, "r1", "fay@dev", "agent-a", "done", 0, ``, 0)

	rows, err := AgentCompliance(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("expected no compliance rows with nothing mandated, got %+v", rows)
	}
}
