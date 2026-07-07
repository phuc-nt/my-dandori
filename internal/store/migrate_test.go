package store

import (
	"database/sql"
	"io"
	"os"
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

// TestMigrate016OnRealFleetDB proves the 016 migration (knowledge_units +
// knowledge_transitions + the adoptions rebuild) applies cleanly on the
// ACTUAL fleet database, not just an empty/synthetic one. It copies
// ~/.dandori/dandori.db into a scratch t.TempDir() and operates only on that
// copy — the original fleet DB is NEVER opened or written by this test. If
// the fleet DB isn't present (e.g. CI, a fresh machine), the test skips
// rather than failing.
func TestMigrate016OnRealFleetDB(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir:", err)
	}
	src := filepath.Join(home, ".dandori", "dandori.db")
	info, err := os.Stat(src)
	if err != nil || info.IsDir() {
		t.Skip("fleet DB not present at", src)
	}

	dst := filepath.Join(t.TempDir(), "fleet-copy.db")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy fleet db: %v", err)
	}

	// Count adoptions BEFORE migrating, via a raw read-only connection (no
	// migration side effects from merely counting).
	preCount, err := countAdoptionsReadOnly(dst)
	if err != nil {
		t.Fatalf("pre-migrate adoptions count: %v", err)
	}

	s, err := Open(dst) // runs all pending migrations, including 016
	if err != nil {
		t.Fatalf("open+migrate fleet copy: %v", err)
	}
	defer s.Close()

	var version int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version < 16 {
		t.Fatalf("user_version after migrate: %d, want >= 16", version)
	}

	for _, tbl := range []string{"knowledge_units", "knowledge_transitions"} {
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).
			Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: count=%d err=%v", tbl, n, err)
		}
	}

	var postCount int
	if err := s.DB.QueryRow(`SELECT count(*) FROM adoptions`).Scan(&postCount); err != nil {
		t.Fatalf("post-migrate adoptions count: %v", err)
	}
	if postCount != preCount {
		t.Errorf("adoptions count changed by migration: before=%d after=%d", preCount, postCount)
	}

	// New columns must be usable: unit_id + installed on an adoptions insert,
	// and a knowledge_units row with the new name/version_n/supersedes_id
	// columns.
	if _, err := s.DB.Exec(`INSERT INTO knowledge_units(kind, name, title, state, version_n, created_at, updated_at)
		VALUES('skill', 'migration-smoke-test', 'Migration smoke test', 'nominated', 1, ?, ?)`, Now(), Now()); err != nil {
		t.Fatalf("insert knowledge_units row: %v", err)
	}
	var unitID int64
	if err := s.DB.QueryRow(`SELECT id FROM knowledge_units WHERE name = 'migration-smoke-test'`).Scan(&unitID); err != nil {
		t.Fatalf("read back knowledge_units row: %v", err)
	}
	if _, err := s.DB.Exec(`INSERT INTO adoptions(unit_id, installed, adopted_at) VALUES(?, 1, ?)`,
		unitID, Now()); err != nil {
		t.Fatalf("insert adoptions with unit_id+installed: %v", err)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// countAdoptionsReadOnly opens the DB file with a bare database/sql
// connection — bypassing this package's Open/migrate entirely — so counting
// rows before the 016 migration runs doesn't itself trigger the migration.
func countAdoptionsReadOnly(path string) (int, error) {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var n int
	err = db.QueryRow(`SELECT count(*) FROM adoptions`).Scan(&n)
	return n, err
}
