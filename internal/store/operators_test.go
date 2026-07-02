package store

import (
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Migration 007 must apply on a database that already holds seeded rows —
// additive columns cannot break existing runs/events.
func TestMigrationSeededThroughLatest(t *testing.T) {
	s := openTest(t)
	if _, err := s.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a1','a1', ?)`, Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, started_at) VALUES('r1','r1','a1', ?)`, Now()); err != nil {
		t.Fatal(err)
	}
	// New columns exist and are nullable for pre-central rows.
	var op any
	if err := s.DB.QueryRow(`SELECT operator_id FROM runs WHERE id='r1'`).Scan(&op); err != nil {
		t.Fatalf("operator_id column: %v", err)
	}
	if op != nil {
		t.Errorf("legacy run operator_id: %v, want NULL", op)
	}
	for _, tbl := range []string{"operators", "teams", "team_members"} {
		var n int
		s.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n)
		if n != 1 {
			t.Errorf("table %s missing", tbl)
		}
	}
}

func TestResolveOperatorImmutable(t *testing.T) {
	s := openTest(t)
	id1, err := s.ResolveOperator("alice@mac")
	if err != nil || id1 != "alice@mac" {
		t.Fatalf("resolve: %q %v", id1, err)
	}
	id2, err := s.ResolveOperator("alice@mac")
	if err != nil || id2 != id1 {
		t.Fatalf("re-resolve changed id: %q vs %q (%v)", id2, id1, err)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM operators`).Scan(&n)
	if n != 1 {
		t.Errorf("operators rows: %d, want 1", n)
	}
	if id, err := s.ResolveOperator(""); err != nil || id != "" {
		t.Errorf("empty principal must be a no-op, got %q %v", id, err)
	}
}

func TestTeamMembership(t *testing.T) {
	s := openTest(t)
	s.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('bot','bot', ?)`, Now())
	s.ResolveOperator("bob@dev")

	id, err := s.CreateTeam("Đội Alpha")
	if err != nil {
		t.Fatal(err)
	}
	id2, _ := s.CreateTeam("Đội Alpha") // idempotent
	if id2 != id {
		t.Errorf("duplicate team: %d vs %d", id2, id)
	}
	if err := s.AssignMember(id, "agent", "bot"); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignMember(id, "operator", "bob@dev"); err != nil {
		t.Fatal(err)
	}
	// Unknown member or bad type must be rejected (polymorphic relation is app-enforced).
	if err := s.AssignMember(id, "agent", "ghost"); err == nil {
		t.Error("assigning unknown agent must fail")
	}
	if err := s.AssignMember(id, "robot", "bot"); err == nil {
		t.Error("bad member type must fail")
	}
	ms, err := s.TeamMembers(id)
	if err != nil || len(ms) != 2 {
		t.Fatalf("members: %v %v", ms, err)
	}
	if err := s.UnassignMember(id, "agent", "bot"); err != nil {
		t.Fatal(err)
	}
	ms, _ = s.TeamMembers(id)
	if len(ms) != 1 {
		t.Errorf("after unassign: %d members", len(ms))
	}
}

func TestReadPoolFallback(t *testing.T) {
	s := openTest(t)
	if s.Read() != s.DB {
		t.Error("without EnableReadPool, Read() must fall back to the writer")
	}
	if err := s.EnableReadPool(); err != nil {
		t.Fatal(err)
	}
	if s.Read() == s.DB {
		t.Error("after EnableReadPool, Read() must be the read-only pool")
	}
	var n int
	if err := s.Read().QueryRow(`SELECT count(*) FROM agents`).Scan(&n); err != nil {
		t.Fatalf("read pool query: %v", err)
	}
	if _, err := s.Read().Exec(`INSERT INTO settings(key, value) VALUES('x','y')`); err == nil {
		t.Error("read pool must reject writes (mode=ro)")
	}
}
