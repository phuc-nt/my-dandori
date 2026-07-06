package auth

import (
	"regexp"
	"strings"
	"testing"
)

var tokenFormatRe = regexp.MustCompile(`^dnd_[0-9A-Za-z]{64}_[0-9A-Za-z]{6}$`)

func TestGenerateTokenFormat(t *testing.T) {
	plain, hash, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if !tokenFormatRe.MatchString(plain) {
		t.Errorf("token %q does not match expected format dnd_<64>_<6>", plain)
	}
	if hash == "" || hash == plain {
		t.Errorf("hash must be non-empty and distinct from plaintext, got %q", hash)
	}
	if len(hash) != 64 { // sha256 hex
		t.Errorf("hash length = %d, want 64 (sha256 hex)", len(hash))
	}
}

func TestGenerateTokenUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		plain, _, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		if seen[plain] {
			t.Fatalf("duplicate token generated: %q", plain)
		}
		seen[plain] = true
	}
}

func TestTokenHashDeterministic(t *testing.T) {
	plain, hash, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if got := TokenHash(plain); got != hash {
		t.Errorf("TokenHash(plain) = %q, want %q (must match hash returned by GenerateToken)", got, hash)
	}
}

func TestTokenHashDistinctForDistinctInput(t *testing.T) {
	p1, h1, _ := GenerateToken()
	p2, h2, _ := GenerateToken()
	if p1 == p2 {
		t.Fatal("two generated tokens were equal — entropy source broken")
	}
	if h1 == h2 {
		t.Fatal("distinct tokens hashed to the same value")
	}
}

func TestVerifyChecksum(t *testing.T) {
	valid, _, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"valid generated token", valid, true},
		{"wrong prefix", strings.Replace(valid, "dnd_", "xyz_", 1), false},
		{"flipped last checksum char", flipLastChar(valid), false},
		{"truncated entropy", valid[:len(valid)-10], false},
		{"empty string", "", false},
		{"missing separator", "dnd_" + strings.ReplaceAll(valid[4:], "_", ""), false},
		{"garbage", "not-a-token-at-all", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VerifyChecksum(tt.token); got != tt.want {
				t.Errorf("VerifyChecksum(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}

// flipLastChar mutates the final character of s to something different,
// corrupting the checksum without changing the string's length/shape.
func flipLastChar(s string) string {
	if s == "" {
		return s
	}
	last := s[len(s)-1]
	repl := byte('A')
	if last == 'A' {
		repl = 'B'
	}
	return s[:len(s)-1] + string(repl)
}
