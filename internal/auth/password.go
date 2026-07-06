// Package auth implements local username+password login for the console:
// argon2id hashing, hand-rolled SQLite-backed sessions, and an in-memory
// login rate limiter. Pure-Go (golang.org/x/crypto) — no CGO.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// OWASP 2025 interactive-login parameters: ~100ms verification latency,
// acceptable for a small team, resistant to GPU/side-channel attacks.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB (64 MiB)
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// ErrInvalidHash means the stored hash is not a well-formed $argon2id$ string
// (corrupt data, or a hash from an incompatible algorithm/version).
var ErrInvalidHash = errors.New("auth: invalid encoded hash")

// HashPassword returns a self-describing argon2id encoded hash:
// $argon2id$v=19$m=65536,t=3,p=1$<base64 salt>$<base64 hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	encSalt := base64.RawStdEncoding.EncodeToString(salt)
	encHash := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, encSalt, encHash), nil
}

// VerifyPassword reports whether password matches the encoded hash produced
// by HashPassword. Comparison of the derived key is constant-time; a
// malformed hash returns false (never panics), so a corrupt row fails closed.
func VerifyPassword(password, encodedHash string) bool {
	salt, hash, m, t, p, err := decodeHash(encodedHash)
	if err != nil {
		return false
	}
	candidate := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(hash)))
	return subtle.ConstantTimeCompare(candidate, hash) == 1
}

func decodeHash(encoded string) (salt, hash []byte, m uint32, t uint32, p uint8, err error) {
	parts := strings.Split(encoded, "$")
	// parts[0] is empty (leading $); expect: "", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	var mem, time int
	var threads int
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); err != nil {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	return salt, hash, uint32(mem), uint32(time), uint8(threads), nil
}
