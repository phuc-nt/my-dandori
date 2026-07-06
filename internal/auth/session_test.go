package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "auth-test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func mustCreateOperator(t *testing.T, st *store.Store, id, role string) {
	t.Helper()
	hash, err := HashPassword("irrelevant-for-session-tests")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := st.CreateOperatorAccount(id, id, hash, role); err != nil {
		t.Fatalf("create operator: %v", err)
	}
}

func TestSessionCreateLoad(t *testing.T) {
	st := testStore(t)
	mustCreateOperator(t, st, "alice", "admin")
	ss := NewSessionStore(st)

	id, err := ss.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(id) == 0 {
		t.Fatal("session id must not be empty")
	}

	sess, err := ss.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sess == nil {
		t.Fatal("Load returned nil for a fresh session")
	}
	if sess.OperatorID != "alice" || sess.Role != "admin" {
		t.Errorf("session = %+v, want operator=alice role=admin", sess)
	}
}

func TestSessionLoadUnknownID(t *testing.T) {
	st := testStore(t)
	ss := NewSessionStore(st)
	sess, err := ss.Load("does-not-exist")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sess != nil {
		t.Error("Load of unknown id must return nil session")
	}
}

func TestSessionLoadEmptyID(t *testing.T) {
	st := testStore(t)
	ss := NewSessionStore(st)
	sess, err := ss.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sess != nil {
		t.Error("Load of empty id must return nil session")
	}
}

func TestSessionDelete(t *testing.T) {
	st := testStore(t)
	mustCreateOperator(t, st, "alice", "admin")
	ss := NewSessionStore(st)
	id, _ := ss.Create("alice")

	if err := ss.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	sess, err := ss.Load(id)
	if err != nil {
		t.Fatalf("Load after delete: %v", err)
	}
	if sess != nil {
		t.Error("session must be gone after Delete")
	}
}

func TestSessionRotate(t *testing.T) {
	st := testStore(t)
	mustCreateOperator(t, st, "alice", "viewer")
	ss := NewSessionStore(st)
	oldID, _ := ss.Create("alice")

	newID, err := ss.Rotate(oldID, "alice")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if newID == oldID {
		t.Fatal("Rotate must produce a different session id")
	}
	if sess, _ := ss.Load(oldID); sess != nil {
		t.Error("old session id must be invalidated after Rotate")
	}
	sess, err := ss.Load(newID)
	if err != nil || sess == nil {
		t.Fatalf("new session must load: sess=%v err=%v", sess, err)
	}
	if sess.OperatorID != "alice" {
		t.Errorf("rotated session operator = %q, want alice", sess.OperatorID)
	}
}

func TestSessionDeleteForOperator(t *testing.T) {
	st := testStore(t)
	mustCreateOperator(t, st, "alice", "admin")
	mustCreateOperator(t, st, "bob", "viewer")
	ss := NewSessionStore(st)

	aliceSess1, _ := ss.Create("alice")
	aliceSess2, _ := ss.Create("alice")
	bobSess, _ := ss.Create("bob")

	if err := ss.DeleteForOperator("alice"); err != nil {
		t.Fatalf("DeleteForOperator: %v", err)
	}

	if sess, _ := ss.Load(aliceSess1); sess != nil {
		t.Error("alice's first session must be invalidated")
	}
	if sess, _ := ss.Load(aliceSess2); sess != nil {
		t.Error("alice's second session must be invalidated")
	}
	if sess, _ := ss.Load(bobSess); sess == nil {
		t.Error("bob's session must survive alice's invalidation")
	}
}

func TestSessionAbsoluteExpiry(t *testing.T) {
	st := testStore(t)
	mustCreateOperator(t, st, "alice", "admin")
	ss := NewSessionStore(st)
	id, _ := ss.Create("alice")

	// Force expiry by rewriting expires_at into the past.
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := st.DB.Exec(`UPDATE sessions SET expires_at = ? WHERE id = ?`, past, id); err != nil {
		t.Fatalf("force expiry: %v", err)
	}

	sess, err := ss.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sess != nil {
		t.Error("session past absolute expiry must not load")
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	st := testStore(t)
	mustCreateOperator(t, st, "alice", "admin")
	ss := NewSessionStore(st)
	id, _ := ss.Create("alice")

	// Force idle timeout by rewriting last_activity_at far in the past,
	// while absolute expiry (8h from now) is still far away.
	stale := time.Now().UTC().Add(-IdleTimeout - time.Minute).Format(time.RFC3339)
	if _, err := st.DB.Exec(`UPDATE sessions SET last_activity_at = ? WHERE id = ?`, stale, id); err != nil {
		t.Fatalf("force idle: %v", err)
	}

	sess, err := ss.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sess != nil {
		t.Error("session past idle timeout must not load")
	}
}

func TestSessionDisabledOperatorCannotLoad(t *testing.T) {
	st := testStore(t)
	mustCreateOperator(t, st, "alice", "admin")
	ss := NewSessionStore(st)
	id, _ := ss.Create("alice")

	if err := st.DisableOperator("alice"); err != nil {
		t.Fatalf("DisableOperator: %v", err)
	}

	sess, err := ss.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sess != nil {
		t.Error("session for a disabled operator must not load")
	}
}

func TestSessionGC(t *testing.T) {
	st := testStore(t)
	mustCreateOperator(t, st, "alice", "admin")
	ss := NewSessionStore(st)
	id, _ := ss.Create("alice")

	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err := st.DB.Exec(`UPDATE sessions SET expires_at = ? WHERE id = ?`, past, id); err != nil {
		t.Fatalf("force expiry: %v", err)
	}
	if err := ss.GC(); err != nil {
		t.Fatalf("GC: %v", err)
	}
	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Error("GC must delete expired sessions")
	}
}
