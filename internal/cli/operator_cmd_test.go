package cli

import (
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/auth"
	"github.com/phuc-nt/dandori/internal/store"
)

func TestOperatorAddCreatesAccount(t *testing.T) {
	db := tempDB(t)
	out, err := execCLI(t, db, "operator", "add", "alice", "--role", "admin", "--password", "s3cret-pw")
	if err != nil {
		t.Fatalf("operator add: %v", err)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("output = %q, want mention of alice", out)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cred, err := st.OperatorByUsername("alice")
	if err != nil {
		t.Fatalf("OperatorByUsername: %v", err)
	}
	if cred == nil {
		t.Fatal("account was not created")
	}
	if cred.Role != "admin" {
		t.Errorf("role = %q, want admin", cred.Role)
	}
	if !auth.VerifyPassword("s3cret-pw", cred.PasswordHash) {
		t.Error("stored hash does not verify against the password given at creation")
	}
}

func TestOperatorAddInvalidRoleRejected(t *testing.T) {
	db := tempDB(t)
	_, err := execCLI(t, db, "operator", "add", "alice", "--role", "superadmin", "--password", "s3cret-pw")
	if err == nil {
		t.Fatal("operator add with an invalid role must fail (in-app validation, M2)")
	}
}

func TestOperatorAddDuplicateUsernameRejected(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "operator", "add", "alice", "--role", "viewer", "--password", "pw1"); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := execCLI(t, db, "operator", "add", "alice", "--role", "admin", "--password", "pw2"); err == nil {
		t.Error("duplicate username must fail (unique index)")
	}
}

func TestOperatorSetPasswordInvalidatesSessions(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "operator", "add", "alice", "--role", "admin", "--password", "old-pw"); err != nil {
		t.Fatalf("add: %v", err)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sessID, err := auth.NewSessionStore(st).Create("alice")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	st.Close()

	if _, err := execCLI(t, db, "operator", "set-password", "alice", "--password", "new-pw"); err != nil {
		t.Fatalf("set-password: %v", err)
	}

	st2, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	cred, err := st2.OperatorByUsername("alice")
	if err != nil {
		t.Fatalf("OperatorByUsername: %v", err)
	}
	if !auth.VerifyPassword("new-pw", cred.PasswordHash) {
		t.Error("password was not updated")
	}
	if sess, _ := auth.NewSessionStore(st2).Load(sessID); sess != nil {
		t.Error("set-password must invalidate the operator's existing sessions (H4)")
	}
}

func TestOperatorDisableInvalidatesSessions(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "operator", "add", "alice", "--role", "admin", "--password", "pw"); err != nil {
		t.Fatalf("add: %v", err)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	sessID, err := auth.NewSessionStore(st).Create("alice")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	st.Close()

	if _, err := execCLI(t, db, "operator", "disable", "alice"); err != nil {
		t.Fatalf("disable: %v", err)
	}

	st2, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	cred, err := st2.OperatorByUsername("alice")
	if err != nil {
		t.Fatalf("OperatorByUsername: %v", err)
	}
	if cred.DisabledAt == "" {
		t.Error("operator must be marked disabled")
	}
	if sess, _ := auth.NewSessionStore(st2).Load(sessID); sess != nil {
		t.Error("disable must invalidate the operator's existing sessions (H4)")
	}
}

func TestOperatorListShowsAccounts(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "operator", "add", "alice", "--role", "admin", "--password", "pw"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := execCLI(t, db, "operator", "add", "bob", "--role", "viewer", "--password", "pw"); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := execCLI(t, db, "operator", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "alice") || !strings.Contains(out, "bob") {
		t.Errorf("list output = %q, want both alice and bob", out)
	}
}

func TestOperatorListEmpty(t *testing.T) {
	db := tempDB(t)
	out, err := execCLI(t, db, "operator", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "no operator accounts") {
		t.Errorf("empty list output = %q, want guidance message", out)
	}
}

// Non-interactive stdin without --password must fail cleanly, never hang or
// silently accept an empty password. Explicitly resets the shared Cobra flag
// var to "" first — Cobra flag state persists across execCLI calls within a
// test binary (see execCLI doc comment), so a prior test's --password would
// otherwise leak into this one.
func TestOperatorAddNoPasswordNonInteractive(t *testing.T) {
	db := tempDB(t)
	flagOperatorPassword = ""
	_, err := execCLI(t, db, "operator", "add", "alice", "--role", "admin")
	if err == nil {
		t.Error("operator add with no --password and no TTY must fail, not hang or silently succeed")
	}
}
