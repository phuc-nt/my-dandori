package cli

import (
	"strings"
	"testing"
)

func TestTokenCreateForUnknownOperatorRejected(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "token", "create", "ghost", "--name", "laptop"); err == nil {
		t.Error("token create for a non-existent operator must fail")
	}
}

// [MEDIUM-1] A disabled operator must not be issued a fresh ingest token —
// LookupToken already invalidates existing tokens, this closes the "mint a
// new one" gap.
func TestTokenCreateForDisabledOperatorRejected(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "operator", "add", "alice", "--role", "viewer", "--password", "pw"); err != nil {
		t.Fatalf("operator add: %v", err)
	}
	if _, err := execCLI(t, db, "operator", "disable", "alice"); err != nil {
		t.Fatalf("operator disable: %v", err)
	}
	_, err := execCLI(t, db, "token", "create", "alice", "--name", "laptop")
	if err == nil {
		t.Fatal("token create for a disabled operator must fail")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error = %q, want mention of disabled", err.Error())
	}
}

func TestTokenCreateForEnabledOperatorSucceeds(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "operator", "add", "alice", "--role", "viewer", "--password", "pw"); err != nil {
		t.Fatalf("operator add: %v", err)
	}
	out, err := execCLI(t, db, "token", "create", "alice", "--name", "laptop")
	if err != nil {
		t.Fatalf("token create: %v", err)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("output = %q, want mention of alice", out)
	}
}
