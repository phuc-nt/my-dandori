// Package testseed provides fixture helpers shared by tests across packages:
// deterministic agents/runs/events so metric math can be verified by hand.
package testseed

import (
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

func ts(daysAgo int) string {
	return time.Now().UTC().AddDate(0, 0, -daysAgo).Format(time.RFC3339)
}

// Agent inserts an agent (id == name slug for simplicity).
func Agent(t testing.TB, st *store.Store, id string) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?, ?, ?)
		ON CONFLICT(name) DO NOTHING`, id, id, ts(30)); err != nil {
		t.Fatal(err)
	}
}

// Run inserts a run for an agent.
func Run(t testing.TB, st *store.Store, id, agentID, status string, daysAgo int, cost float64) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, status, started_at, ended_at, cost_usd)
		VALUES(?, ?, ?, 'proj', ?, ?, ?, ?)`,
		id, id, agentID, status, ts(daysAgo), ts(daysAgo), cost); err != nil {
		t.Fatal(err)
	}
}

// Event inserts an event on a run. ok: -1 = NULL.
func Event(t testing.TB, st *store.Store, runID, kind, tool string, ok int, payload string) {
	t.Helper()
	var okVal any
	if ok >= 0 {
		okVal = ok
	}
	if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, ?, ?, ?, ?, ?)`, runID, ts(1), kind, tool, okVal, payload); err != nil {
		t.Fatal(err)
	}
}

// Flag opens a flag on a run.
func Flag(t testing.TB, st *store.Store, runID, reason string) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO flags(run_id, reason, created_at) VALUES(?, ?, ?)`,
		runID, reason, ts(0)); err != nil {
		t.Fatal(err)
	}
}

// WorkItem upserts a jira/github work item.
func WorkItem(t testing.TB, st *store.Store, source, key, status string) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO work_items(source, key, title, status, updated_at)
		VALUES(?, ?, ?, ?, ?) ON CONFLICT(source, key) DO UPDATE SET status = excluded.status`,
		source, key, key, status, ts(0)); err != nil {
		t.Fatal(err)
	}
}
