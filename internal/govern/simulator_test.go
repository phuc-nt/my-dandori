package govern

import (
	"context"
	"testing"
)

func TestSimulateCountsHistoricalMatches(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "s1", 0)
	// tool_use history is written by the hook ingest path — seed it directly.
	for _, cmd := range []string{"ls -la", "curl --insecure https://x", "go test ./...", "curl --insecure http://y"} {
		e.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
			VALUES('s1', datetime('now'), 'tool_use', 'Bash', NULL, ?)`, `{"command":"`+cmd+`"}`)
	}

	res, err := Simulate(e.St, `curl .*--insecure`, "global", "", 30)
	if err != nil {
		t.Fatal(err)
	}
	if res.Hits != 2 || res.Total != 4 {
		t.Errorf("hits=%d total=%d, want 2/4", res.Hits, res.Total)
	}
	if len(res.Samples) != 2 || res.Samples[0].RunID != "s1" {
		t.Errorf("samples: %+v", res.Samples)
	}
	// Scoped to another agent → no hits.
	res2, _ := Simulate(e.St, `curl .*--insecure`, "agent", "someone-else", 30)
	if res2.Hits != 0 {
		t.Errorf("scoped sim hits: %d, want 0", res2.Hits)
	}
	// Bad regex → friendly error.
	if _, err := Simulate(e.St, `([`, "global", "", 30); err == nil {
		t.Error("invalid pattern must error")
	}
}

func TestScopedRuleOnlyBindsItsTarget(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "sc1", 0) // agent a1
	if _, err := CreateRule(e.St, "block", `npm publish`, "no publishing", "agent", "other-agent", false, "tester"); err != nil {
		t.Fatal(err)
	}
	if d := e.Evaluate(context.Background(), bashCall("sc1", "npm publish")); d.Verdict != Allow {
		t.Errorf("rule scoped to other-agent must not block a1: %s (%s)", d.Verdict, d.Reason)
	}
	if _, err := CreateRule(e.St, "block", `npm publish`, "no publishing", "agent", "a1", false, "tester"); err != nil {
		t.Fatal(err)
	}
	if d := e.Evaluate(context.Background(), bashCall("sc1", "npm publish")); d.Verdict != Deny {
		t.Errorf("rule scoped to a1 must block: %s", d.Verdict)
	}
	// Validation.
	if _, err := CreateRule(e.St, "block", `([`, "bad", "global", "", false, "t"); err == nil {
		t.Error("bad regex must be rejected")
	}
	if _, err := CreateRule(e.St, "block", `x`, "d", "agent", "", false, "t"); err == nil {
		t.Error("agent scope without target must be rejected")
	}
	// Delete + audit.
	if err := DeleteRule(e.St, 1, "tester"); err != nil {
		t.Fatal(err)
	}
	var audits int
	e.St.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action IN ('create_rule','delete_rule')`).Scan(&audits)
	if audits != 3 {
		t.Errorf("rule audits: %d, want 3", audits)
	}
}
