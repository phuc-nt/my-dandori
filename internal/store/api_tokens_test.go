package store

import (
	"testing"
)

// seedOperator inserts a console-account-style operator row (username set)
// so a token's operator_id FK is satisfiable.
func seedOperator(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.DB.Exec(`INSERT INTO operators(id, display, created_at, username, role)
		VALUES(?, ?, ?, ?, 'viewer')`, id, id, Now(), id); err != nil {
		t.Fatalf("seed operator %q: %v", id, err)
	}
}

func TestCreateAndLookupToken(t *testing.T) {
	s := openTest(t)
	seedOperator(t, s, "alice")

	if err := s.CreateToken("hash-1", "alice", "laptop"); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	id, ok := s.LookupToken("hash-1")
	if !ok || id != "alice" {
		t.Errorf("LookupToken = (%q, %v), want (alice, true)", id, ok)
	}
}

func TestLookupTokenUnknownHash(t *testing.T) {
	s := openTest(t)
	if _, ok := s.LookupToken("does-not-exist"); ok {
		t.Error("unknown hash must not be found")
	}
}

func TestLookupTokenRevoked(t *testing.T) {
	s := openTest(t)
	seedOperator(t, s, "alice")
	if err := s.CreateToken("hash-1", "alice", "laptop"); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeToken("hash-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LookupToken("hash-1"); ok {
		t.Error("revoked token must not be found by LookupToken")
	}
}

func TestTouchTokenUpdatesLastUsed(t *testing.T) {
	s := openTest(t)
	seedOperator(t, s, "alice")
	if err := s.CreateToken("hash-1", "alice", "laptop"); err != nil {
		t.Fatal(err)
	}
	tokens, err := s.ListTokens("alice")
	if err != nil || len(tokens) != 1 {
		t.Fatalf("ListTokens before touch: %v %v", tokens, err)
	}
	if tokens[0].LastUsedAt != nil {
		t.Errorf("last_used_at should start NULL, got %v", *tokens[0].LastUsedAt)
	}
	if err := s.TouchToken("hash-1"); err != nil {
		t.Fatalf("TouchToken: %v", err)
	}
	tokens, err = s.ListTokens("alice")
	if err != nil || len(tokens) != 1 {
		t.Fatalf("ListTokens after touch: %v %v", tokens, err)
	}
	if tokens[0].LastUsedAt == nil || *tokens[0].LastUsedAt == "" {
		t.Error("last_used_at must be set after TouchToken")
	}
}

// TouchToken on an unknown hash must not error — best-effort (never fail
// the request it's piggy-backing on).
func TestTouchTokenUnknownHashNoError(t *testing.T) {
	s := openTest(t)
	if err := s.TouchToken("ghost-hash"); err != nil {
		t.Errorf("TouchToken on unknown hash must not error, got %v", err)
	}
}

func TestListTokensScopedToOperator(t *testing.T) {
	s := openTest(t)
	seedOperator(t, s, "alice")
	seedOperator(t, s, "bob")
	s.CreateToken("hash-alice-1", "alice", "laptop")
	s.CreateToken("hash-alice-2", "alice", "desktop")
	s.CreateToken("hash-bob-1", "bob", "laptop")

	tokens, err := s.ListTokens("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 {
		t.Fatalf("alice's tokens: %d, want 2", len(tokens))
	}
	for _, tok := range tokens {
		if tok.OperatorID != "alice" {
			t.Errorf("ListTokens(alice) returned a token for %q", tok.OperatorID)
		}
	}
}

func TestRevokeTokenIdempotent(t *testing.T) {
	s := openTest(t)
	seedOperator(t, s, "alice")
	s.CreateToken("hash-1", "alice", "laptop")
	if err := s.RevokeToken("hash-1"); err != nil {
		t.Fatal(err)
	}
	// Revoking again (already revoked) must not error.
	if err := s.RevokeToken("hash-1"); err != nil {
		t.Errorf("re-revoke must be a no-op, got %v", err)
	}
	// Revoking an unknown id must not error either.
	if err := s.RevokeToken("ghost"); err != nil {
		t.Errorf("revoke unknown id must not error, got %v", err)
	}
}

func TestTokenByPrefix(t *testing.T) {
	s := openTest(t)
	seedOperator(t, s, "alice")
	s.CreateToken("abc123hash", "alice", "laptop")

	full, err := s.TokenByPrefix("abc123")
	if err != nil || full != "abc123hash" {
		t.Errorf("TokenByPrefix(abc123) = (%q, %v), want (abc123hash, nil)", full, err)
	}
	if _, err := s.TokenByPrefix("does-not-match"); err == nil {
		t.Error("no-match prefix must error")
	}
}

// [MEDIUM-1] Disabling an operator must invalidate their tokens immediately
// (no explicit revoke needed) — mirrors auth.SessionStore.Load's
// operators.disabled_at IS NULL join.
func TestLookupTokenDisabledOperator(t *testing.T) {
	s := openTest(t)
	seedOperator(t, s, "alice")
	if err := s.CreateToken("hash-1", "alice", "laptop"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LookupToken("hash-1"); !ok {
		t.Fatal("token must resolve before operator is disabled")
	}
	if err := s.DisableOperator("alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LookupToken("hash-1"); ok {
		t.Error("token for a disabled operator must no longer resolve")
	}
}

func TestTokenByPrefixAmbiguous(t *testing.T) {
	s := openTest(t)
	seedOperator(t, s, "alice")
	s.CreateToken("abc123hash1", "alice", "laptop")
	s.CreateToken("abc123hash2", "alice", "desktop")

	if _, err := s.TokenByPrefix("abc123"); err == nil {
		t.Error("ambiguous prefix (matches 2 tokens) must error, not pick one")
	}
}
