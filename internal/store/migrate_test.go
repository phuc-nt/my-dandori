package store

import (
	"path/filepath"
	"testing"
)

func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	// Second open must re-run migrate without error and without duplicates.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()

	tables := []string{"agents", "runs", "events", "work_items", "budgets",
		"approvals", "guardrail_rules", "flags", "audit_log", "settings",
		"gate_results", "notifications",
		// v4 (migrations 007-010)
		"operators", "teams", "team_members", "insights", "adoptions",
		"chat_sessions", "chat_messages", "tool_audits"}
	for _, tbl := range tables {
		var n int
		err := s2.DB.QueryRow(
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("table %s: count=%d err=%v", tbl, n, err)
		}
	}
	// The ULID partial unique index is what makes spool replay idempotent —
	// a migration that dropped it would silently break dedup.
	var idx int
	s2.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_events_ulid'`).Scan(&idx)
	if idx != 1 {
		t.Error("idx_events_ulid missing — event dedup would break")
	}
	// v4 columns on existing tables must be present.
	if _, err := s2.DB.Exec(`SELECT operator_id FROM runs LIMIT 1`); err != nil {
		t.Errorf("runs.operator_id missing: %v", err)
	}
	if _, err := s2.DB.Exec(`SELECT ulid FROM events LIMIT 1`); err != nil {
		t.Errorf("events.ulid missing: %v", err)
	}
}

// v4 migrations must apply on a database that already holds rows (seeded),
// not just an empty one — additive columns/tables can't break existing data.
func TestMigrateOnSeededDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seeded.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a','a', ?)`, Now())
	s.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, started_at) VALUES('r','r','a', ?)`, Now())
	s.DB.Exec(`INSERT INTO events(run_id, ts, kind) VALUES('r', ?, 'tool_use')`, Now())
	s.Close()

	// Re-open re-runs migrate (no-op here, but proves seeded rows survive).
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen seeded db: %v", err)
	}
	defer s2.Close()
	var runs, events int
	s2.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs)
	s2.DB.QueryRow(`SELECT count(*) FROM events`).Scan(&events)
	if runs != 1 || events != 1 {
		t.Errorf("seeded rows lost after migrate: runs=%d events=%d", runs, events)
	}
}

func TestSettings(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if got := s.Setting("missing"); got != "" {
		t.Errorf("missing key: got %q", got)
	}
	if err := s.SetSetting("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("k", "v2"); err != nil {
		t.Fatal(err)
	}
	if got := s.Setting("k"); got != "v2" {
		t.Errorf("upsert: got %q", got)
	}
}
