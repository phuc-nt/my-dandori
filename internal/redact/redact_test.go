package redact

import "testing"

// TestSupersetInvariant proves the core leak-prevention property: every
// string the guardrail treats as Deny-worthy (SecretStrictRe / a real
// Bearer secret) or Gate-worthy (PiiRe) must ALSO be caught by the
// capture-path redactor. Otherwise a secret/PII value could pass through
// checkSecrets' verdict logic but still land raw in events.payload or an
// export bundle.
func TestSupersetInvariant(t *testing.T) {
	cases := []string{
		"sk-abcdefghijklmnopqrstuvwxyz1234",
		"xoxb-1234567890-abcdefghij",
		"ATATT3xFfGF0T1234567890123456789012345",
		"ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"AKIAIOSFODNN7EXAMPLE",
		"-----BEGIN RSA PRIVATE KEY-----",
		"Bearer sk-liveTOKEN1234567890ABCDEF",
		"contact me at jane.doe@example.com",
		"card 4111 1111 1111 1111 on file",
	}
	for _, s := range cases {
		if String(s) == s {
			t.Errorf("String(%q) did not redact — superset invariant broken", s)
		}
	}
}

// TestSecretStrictRe checks the high-confidence Deny set only matches the
// intended provider-key shapes.
func TestSecretStrictRe(t *testing.T) {
	shouldMatch := []string{
		"sk-abcdefghijklmnopqrstuvwxyz1234",
		"xoxb-1234567890-abcdefghij",
		"ATATT3xFfGF0T1234567890123456789012345",
		"ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"AKIAIOSFODNN7EXAMPLE",
		"-----BEGIN PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----",
	}
	for _, s := range shouldMatch {
		if !SecretStrictRe.MatchString(s) {
			t.Errorf("SecretStrictRe should match %q", s)
		}
	}
	shouldNotMatch := []string{
		"sk-abc",                    // too short
		"password = os.environ['X']", // kv-generic excluded from strict set
		"just some text",
	}
	for _, s := range shouldNotMatch {
		if SecretStrictRe.MatchString(s) {
			t.Errorf("SecretStrictRe should NOT match %q", s)
		}
	}
}

// TestBearerSecretMatch is the false-positive-avoidance test: env-var
// references are the sanctioned way to wire a secret into a command and
// must never be denied.
func TestBearerSecretMatch(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"Bearer $OPENROUTER_API_KEY", false},
		{"Bearer ${TOKEN}", false},
		{"Bearer sk-liveTOKEN1234567890ABCDEF", true},
		{"Bearer abcdefghijklmnopqrstuvwxyz", true},
		{"no bearer here", false},
		{"Bearer short", false}, // under 16 chars
	}
	for _, c := range cases {
		if got := BearerSecretMatch(c.s); got != c.want {
			t.Errorf("BearerSecretMatch(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

// TestPiiRe checks email/card detection surface.
func TestPiiRe(t *testing.T) {
	shouldMatch := []string{
		"jane.doe@example.com",
		"4111 1111 1111 1111",
		"4111-1111-1111-1111",
		"4111111111111111",
	}
	for _, s := range shouldMatch {
		if !PiiRe.MatchString(s) {
			t.Errorf("PiiRe should match %q", s)
		}
	}
}

// TestRegressionExistingSecretRedaction locks in behavior that predates this
// change — over-matching kv-generic pairs and bearer/xox/sk/ghp tokens.
func TestRegressionExistingSecretRedaction(t *testing.T) {
	cases := []string{
		`Bearer some-long-token-value`,
		`xoxb-1234-5678-abcdef`,
		`ATATT3xFfGF01234567890`,
		`sk-shortenough12`,
		`ghp_abcdefgh12345678`,
		`api_key: "abc123"`,
		`"token": "xyz789"`,
		`password=hunter2`,
	}
	for _, s := range cases {
		if String(s) == s {
			t.Errorf("regression: String(%q) should redact", s)
		}
	}
}

// TestMaskPII verifies the approval-facing masker preserves structure
// (card first4/last4, email first-char) without exposing the raw value.
func TestMaskPII(t *testing.T) {
	masked := MaskPII("card 4111111111111111 email jane@example.com")
	if masked == "card 4111111111111111 email jane@example.com" {
		t.Fatal("MaskPII did not mask anything")
	}
	if contains(masked, "4111111111111111") {
		t.Error("MaskPII leaked full card number")
	}
	if contains(masked, "jane@example.com") {
		t.Error("MaskPII leaked full email")
	}
	// Structure preserved: first4+last4 of card visible, domain visible.
	if !contains(masked, "4111") || !contains(masked, "1111") {
		t.Error("MaskPII should keep first4/last4 digits for reviewer context")
	}
	if !contains(masked, "example.com") {
		t.Error("MaskPII should keep email domain for reviewer context")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
