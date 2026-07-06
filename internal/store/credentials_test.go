package store

import (
	"path/filepath"
	"testing"
)

func openCredTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "cred-test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestHasAnyAccountEmpty(t *testing.T) {
	st := openCredTestStore(t)
	ok, err := st.HasAnyAccount()
	if err != nil {
		t.Fatalf("HasAnyAccount: %v", err)
	}
	if ok {
		t.Error("fresh DB must report no accounts")
	}
}

func TestCreateOperatorAccountAndHasAnyAccount(t *testing.T) {
	st := openCredTestStore(t)
	if err := st.CreateOperatorAccount("alice", "alice", "hash123", "admin"); err != nil {
		t.Fatalf("CreateOperatorAccount: %v", err)
	}
	ok, err := st.HasAnyAccount()
	if err != nil {
		t.Fatalf("HasAnyAccount: %v", err)
	}
	if !ok {
		t.Error("HasAnyAccount must be true after creating an account")
	}
}

func TestHasAnyAccountIgnoresMachinePrincipal(t *testing.T) {
	st := openCredTestStore(t)
	// ResolveOperator-style row: no password_hash — must NOT count as an account.
	if _, err := st.ResolveOperator("alice@dev-laptop"); err != nil {
		t.Fatalf("ResolveOperator: %v", err)
	}
	ok, err := st.HasAnyAccount()
	if err != nil {
		t.Fatalf("HasAnyAccount: %v", err)
	}
	if ok {
		t.Error("a machine-principal row with no password_hash must not count as an account")
	}
}

func TestHasAnyAccountIgnoresDisabled(t *testing.T) {
	st := openCredTestStore(t)
	if err := st.CreateOperatorAccount("alice", "alice", "hash123", "admin"); err != nil {
		t.Fatalf("CreateOperatorAccount: %v", err)
	}
	if err := st.DisableOperator("alice"); err != nil {
		t.Fatalf("DisableOperator: %v", err)
	}
	ok, err := st.HasAnyAccount()
	if err != nil {
		t.Fatalf("HasAnyAccount: %v", err)
	}
	if ok {
		t.Error("a disabled account must not count toward HasAnyAccount")
	}
}

func TestOperatorByUsername(t *testing.T) {
	st := openCredTestStore(t)
	if err := st.CreateOperatorAccount("alice", "alice", "hash123", "admin"); err != nil {
		t.Fatalf("CreateOperatorAccount: %v", err)
	}
	c, err := st.OperatorByUsername("alice")
	if err != nil {
		t.Fatalf("OperatorByUsername: %v", err)
	}
	if c == nil {
		t.Fatal("expected credential row, got nil")
	}
	if c.ID != "alice" || c.PasswordHash != "hash123" || c.Role != "admin" || c.DisabledAt != "" {
		t.Errorf("credential = %+v, unexpected values", c)
	}
}

func TestOperatorByUsernameNotFound(t *testing.T) {
	st := openCredTestStore(t)
	c, err := st.OperatorByUsername("nobody")
	if err != nil {
		t.Fatalf("OperatorByUsername: %v", err)
	}
	if c != nil {
		t.Error("unknown username must return nil, not an error (avoid enumeration signal)")
	}
}

func TestSetPassword(t *testing.T) {
	st := openCredTestStore(t)
	if err := st.CreateOperatorAccount("alice", "alice", "old-hash", "admin"); err != nil {
		t.Fatalf("CreateOperatorAccount: %v", err)
	}
	if err := st.SetPassword("alice", "new-hash"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	c, err := st.OperatorByUsername("alice")
	if err != nil {
		t.Fatalf("OperatorByUsername: %v", err)
	}
	if c.PasswordHash != "new-hash" {
		t.Errorf("password hash = %q, want new-hash", c.PasswordHash)
	}
}

func TestSetPasswordUnknownOperator(t *testing.T) {
	st := openCredTestStore(t)
	if err := st.SetPassword("nobody", "hash"); err == nil {
		t.Error("SetPassword on an unknown operator must return an error")
	}
}

func TestOperatorRole(t *testing.T) {
	st := openCredTestStore(t)
	if err := st.CreateOperatorAccount("bob", "bob", "hash", "viewer"); err != nil {
		t.Fatalf("CreateOperatorAccount: %v", err)
	}
	role, err := st.OperatorRole("bob")
	if err != nil {
		t.Fatalf("OperatorRole: %v", err)
	}
	if role != "viewer" {
		t.Errorf("role = %q, want viewer", role)
	}
}

func TestOperatorRoleNotFound(t *testing.T) {
	st := openCredTestStore(t)
	role, err := st.OperatorRole("nobody")
	if err != nil {
		t.Fatalf("OperatorRole: %v", err)
	}
	if role != "" {
		t.Errorf("role for unknown operator = %q, want empty", role)
	}
}

func TestDisableOperator(t *testing.T) {
	st := openCredTestStore(t)
	if err := st.CreateOperatorAccount("alice", "alice", "hash", "admin"); err != nil {
		t.Fatalf("CreateOperatorAccount: %v", err)
	}
	if err := st.DisableOperator("alice"); err != nil {
		t.Fatalf("DisableOperator: %v", err)
	}
	c, err := st.OperatorByUsername("alice")
	if err != nil {
		t.Fatalf("OperatorByUsername: %v", err)
	}
	if c.DisabledAt == "" {
		t.Error("DisabledAt must be set after DisableOperator")
	}
}

func TestDisableOperatorUnknown(t *testing.T) {
	st := openCredTestStore(t)
	if err := st.DisableOperator("nobody"); err == nil {
		t.Error("DisableOperator on an unknown operator must return an error")
	}
}

func TestOperatorEnabled(t *testing.T) {
	st := openCredTestStore(t)
	if err := st.CreateOperatorAccount("alice", "alice", "hash", "admin"); err != nil {
		t.Fatalf("CreateOperatorAccount: %v", err)
	}
	found, enabled, err := st.OperatorEnabled("alice")
	if err != nil {
		t.Fatalf("OperatorEnabled: %v", err)
	}
	if !found || !enabled {
		t.Errorf("OperatorEnabled(alice) = (%v, %v), want (true, true)", found, enabled)
	}
}

func TestOperatorEnabledAfterDisable(t *testing.T) {
	st := openCredTestStore(t)
	if err := st.CreateOperatorAccount("alice", "alice", "hash", "admin"); err != nil {
		t.Fatalf("CreateOperatorAccount: %v", err)
	}
	if err := st.DisableOperator("alice"); err != nil {
		t.Fatalf("DisableOperator: %v", err)
	}
	found, enabled, err := st.OperatorEnabled("alice")
	if err != nil {
		t.Fatalf("OperatorEnabled: %v", err)
	}
	if !found || enabled {
		t.Errorf("OperatorEnabled(alice) after disable = (%v, %v), want (true, false)", found, enabled)
	}
}

func TestOperatorEnabledNotFound(t *testing.T) {
	st := openCredTestStore(t)
	found, _, err := st.OperatorEnabled("nobody")
	if err != nil {
		t.Fatalf("OperatorEnabled: %v", err)
	}
	if found {
		t.Error("OperatorEnabled(nobody) found must be false")
	}
}
