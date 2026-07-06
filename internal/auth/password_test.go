package auth

import "testing"

func TestHashPasswordVerify(t *testing.T) {
	tests := []struct {
		name     string
		password string
		try      string
		want     bool
	}{
		{"correct password verifies", "correct-horse-battery-staple", "correct-horse-battery-staple", true},
		{"wrong password fails", "correct-horse-battery-staple", "wrong-password", false},
		{"empty try fails", "correct-horse-battery-staple", "", false},
		{"case-sensitive", "Password123", "password123", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := HashPassword(tt.password)
			if err != nil {
				t.Fatalf("HashPassword: %v", err)
			}
			if hash == tt.password {
				t.Fatal("hash must not equal plaintext")
			}
			got := VerifyPassword(tt.try, hash)
			if got != tt.want {
				t.Errorf("VerifyPassword(%q, hash) = %v, want %v", tt.try, got, tt.want)
			}
		})
	}
}

func TestHashPasswordFormat(t *testing.T) {
	hash, err := HashPassword("some-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash[:9] != "$argon2id" {
		t.Errorf("hash prefix = %q, want $argon2id", hash[:9])
	}
}

func TestHashPasswordUniqueSalts(t *testing.T) {
	h1, _ := HashPassword("same-password")
	h2, _ := HashPassword("same-password")
	if h1 == h2 {
		t.Error("two hashes of the same password must differ (random salt)")
	}
	// Both must still verify.
	if !VerifyPassword("same-password", h1) || !VerifyPassword("same-password", h2) {
		t.Error("both salted hashes must verify against the same password")
	}
}

func TestVerifyPasswordMalformedHash(t *testing.T) {
	tests := []struct {
		name string
		hash string
	}{
		{"empty string", ""},
		{"not argon2id", "$bcrypt$10$abc"},
		{"missing fields", "$argon2id$v=19$m=65536,t=3,p=1$onlysalt"},
		{"garbage", "not-a-hash-at-all"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if VerifyPassword("anything", tt.hash) {
				t.Error("malformed hash must never verify true")
			}
		})
	}
}
