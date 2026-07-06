package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"strings"
)

// Per-operator ingest tokens (GitHub PAT style): `dnd_<64-char base62
// entropy>_<6-char base62 crc32 checksum>`. Only SHA-256(token) is ever
// stored — the plaintext is shown once at creation and never persisted or
// logged (red-team L2).
const (
	tokenPrefix       = "dnd_"
	tokenEntropyChars = 64
	tokenChecksumLen  = 6
	base62Alphabet    = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// GenerateToken creates a new token, returning the plaintext (show-once) and
// its SHA-256 hex hash (the only form ever persisted).
func GenerateToken() (plain, hash string, err error) {
	entropy, err := randomBase62(tokenEntropyChars)
	if err != nil {
		return "", "", fmt.Errorf("generate token entropy: %w", err)
	}
	checksum := checksumBase62(entropy)
	plain = tokenPrefix + entropy + "_" + checksum
	return plain, TokenHash(plain), nil
}

// TokenHash returns the hex-encoded SHA-256 of a plaintext token — the form
// stored in api_tokens.id and compared on lookup.
func TokenHash(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// VerifyChecksum is an offline fast-reject: malformed tokens (typos,
// truncation, garbage) are rejected without a DB round trip. It is NOT
// authentication — a real hit still requires the DB lookup, so a crafted
// token with a valid checksum but no matching hash is still rejected there.
func VerifyChecksum(plain string) bool {
	if !strings.HasPrefix(plain, tokenPrefix) {
		return false
	}
	body := strings.TrimPrefix(plain, tokenPrefix)
	i := strings.LastIndexByte(body, '_')
	if i < 0 {
		return false
	}
	entropy, checksum := body[:i], body[i+1:]
	if len(entropy) != tokenEntropyChars || len(checksum) != tokenChecksumLen {
		return false
	}
	want := checksumBase62(entropy)
	return subtle.ConstantTimeCompare([]byte(checksum), []byte(want)) == 1
}

// randomBase62 returns n random characters drawn from base62Alphabet using
// crypto/rand (rejection sampling to avoid modulo bias).
func randomBase62(n int) (string, error) {
	const maxByte = 256 - (256 % 62)
	out := make([]byte, 0, n)
	buf := make([]byte, n+n/4+8) // oversample to reduce rand.Read round trips
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if len(out) == n {
				break
			}
			if int(b) >= maxByte {
				continue // reject to keep uniform distribution over 62 symbols
			}
			out = append(out, base62Alphabet[int(b)%62])
		}
	}
	return string(out), nil
}

// checksumBase62 encodes crc32(IEEE) of s as a fixed 6-char base62 string.
func checksumBase62(s string) string {
	sum := crc32.ChecksumIEEE([]byte(s))
	buf := make([]byte, tokenChecksumLen)
	for i := tokenChecksumLen - 1; i >= 0; i-- {
		buf[i] = base62Alphabet[sum%62]
		sum /= 62
	}
	return string(buf)
}
