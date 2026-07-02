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
		"gate_results", "notifications"}
	for _, tbl := range tables {
		var n int
		err := s2.DB.QueryRow(
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("table %s: count=%d err=%v", tbl, n, err)
		}
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
