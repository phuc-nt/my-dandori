package ingest

import (
	"net/http"
	"testing"

	"github.com/phuc-nt/dandori/internal/auth"
)

// seedOperatorToken creates a console operator row (so the FK on
// api_tokens.operator_id is satisfiable) and a token bound to it, returning
// the plaintext token to send as a bearer credential.
func seedOperatorToken(t *testing.T, s *Server, operatorID, label string) string {
	t.Helper()
	if _, err := s.St.DB.Exec(`INSERT INTO operators(id, display, created_at, username, role)
		VALUES(?, ?, ?, ?, 'viewer')`, operatorID, operatorID, "now", operatorID); err != nil {
		t.Fatalf("seed operator: %v", err)
	}
	plain, hash, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if err := s.St.CreateToken(hash, operatorID, label); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	return plain
}

// Per-operator token → events attribute to the token's bound operator.
func TestIngestPerOperatorTokenAttributesCorrectOperator(t *testing.T) {
	s, st := testServer(t)
	token := seedOperatorToken(t, s, "alice", "laptop")

	w := postBatch(t, s.Handler(), token, sampleBatch())
	if w.Code != http.StatusOK {
		t.Fatalf("post: %d %s", w.Code, w.Body)
	}
	var operator string
	if err := st.DB.QueryRow(`SELECT COALESCE(operator_id,'') FROM runs WHERE id='s1'`).Scan(&operator); err != nil {
		t.Fatal(err)
	}
	if operator != "alice" {
		t.Errorf("operator = %q, want %q", operator, "alice")
	}
}

// [H1] A spoofed X-Dandori-Principal-Hint must NOT change attribution when a
// valid per-operator token is presented — the token's bound operator wins.
func TestIngestPerOperatorTokenRejectsSpoofedHint(t *testing.T) {
	s, st := testServer(t)
	token := seedOperatorToken(t, s, "alice", "laptop")

	req := newSignedRequest(t, token, sampleBatch())
	req.Header.Set("X-Dandori-Principal-Hint", "victim")
	w := doRequest(s.Handler(), req)
	if w.Code != http.StatusOK {
		t.Fatalf("post: %d %s", w.Code, w.Body)
	}
	var operator string
	st.DB.QueryRow(`SELECT COALESCE(operator_id,'') FROM runs WHERE id='s1'`).Scan(&operator)
	if operator != "alice" {
		t.Errorf("spoofed hint changed attribution: operator = %q, want %q", operator, "alice")
	}
}

// [H1] Legacy shared token + spoofed hint must attribute to the FIXED legacy
// principal, never to the spoofed value.
func TestIngestLegacyTokenSpoofedHintStaysFixedPrincipal(t *testing.T) {
	s, st := testServer(t) // AllowLegacyIngestToken: true, IngestToken: "secret-token"

	req := newSignedRequest(t, "secret-token", sampleBatch())
	req.Header.Set("X-Dandori-Principal-Hint", "victim")
	w := doRequest(s.Handler(), req)
	if w.Code != http.StatusOK {
		t.Fatalf("post: %d %s", w.Code, w.Body)
	}
	var operator string
	st.DB.QueryRow(`SELECT COALESCE(operator_id,'') FROM runs WHERE id='s1'`).Scan(&operator)
	if operator != legacyPrincipal {
		t.Errorf("operator = %q, want fixed %q (spoof must be rejected)", operator, legacyPrincipal)
	}
}

// AllowLegacyIngestToken=false must 401 the shared token outright.
func TestIngestLegacyTokenRejectedWhenDisallowed(t *testing.T) {
	s, _ := testServer(t)
	s.Cfg.AllowLegacyIngestToken = false

	w := postBatch(t, s.Handler(), "secret-token", sampleBatch())
	if w.Code != http.StatusUnauthorized {
		t.Errorf("legacy token with AllowLegacyIngestToken=false: %d, want 401", w.Code)
	}
}

// Revoking a per-operator token must 401 it immediately.
func TestIngestRevokedTokenRejected(t *testing.T) {
	s, _ := testServer(t)
	token := seedOperatorToken(t, s, "alice", "laptop")
	hash := auth.TokenHash(token)
	if err := s.St.RevokeToken(hash); err != nil {
		t.Fatal(err)
	}
	w := postBatch(t, s.Handler(), token, sampleBatch())
	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token: %d, want 401", w.Code)
	}
}

// [MEDIUM-1] Disabling the token's owning operator must 401 it immediately,
// even though the token itself was never explicitly revoked — an off-boarded
// operator must not keep authenticating to the ingest listener.
func TestIngestDisabledOperatorTokenRejected(t *testing.T) {
	s, st := testServer(t)
	token := seedOperatorToken(t, s, "alice", "laptop")

	w := postBatch(t, s.Handler(), token, sampleBatch())
	if w.Code != http.StatusOK {
		t.Fatalf("post before disable: %d %s", w.Code, w.Body)
	}

	if err := st.DisableOperator("alice"); err != nil {
		t.Fatalf("DisableOperator: %v", err)
	}

	w = postBatch(t, s.Handler(), token, sampleBatch())
	if w.Code != http.StatusUnauthorized {
		t.Errorf("token for disabled operator: %d, want 401", w.Code)
	}
}

// A garbage/malformed dnd_-shaped token (bad checksum) must fast-reject with
// 401 without ever reaching the DB lookup, and a completely unknown but
// well-formed token must also 401.
func TestIngestUnknownOrMalformedTokenRejected(t *testing.T) {
	s, _ := testServer(t)
	unknown, _, _ := auth.GenerateToken() // well-formed, never registered
	tests := []string{
		unknown,
		"dnd_not-even-close-to-the-right-shape_abcdef",
		"", // no Authorization header at all
	}
	for _, tok := range tests {
		if w := postBatch(t, s.Handler(), tok, sampleBatch()); w.Code != http.StatusUnauthorized {
			t.Errorf("token %q: %d, want 401", tok, w.Code)
		}
	}
}

func newSignedRequest(t *testing.T, token string, batch Batch) *http.Request {
	t.Helper()
	req := jsonPostRequest(t, batch)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}
