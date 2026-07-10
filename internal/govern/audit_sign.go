package govern

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sync"
)

// DefaultKeyID is the only key id in use for MVP single-key signing. A future
// key rotation would introduce key_id 2, 3, ... and a lookup table from
// key_id to public key; verifyRow already takes a map keyed by id so that
// extension does not require a signature change.
const DefaultKeyID = 1

// signingKeyEnv is the env var carrying the base64-encoded Ed25519 private
// key (the full 64-byte seed+public form returned by ed25519.GenerateKey, or
// its 32-byte seed — both accepted; see loadSigningKey). It is env-only
// (never YAML) because it is a secret, matching this repo's other secrets
// (OPENROUTER_API_KEY, ATLASSIAN_*, SLACK_*) which internal/config reads via
// os.Getenv directly rather than a dedicated Config field. config.Load's
// loadDotenv(".env") populates process env for ANY key present in .env
// (not just ones config.go explicitly names), so setting
// DANDORI_AUDIT_SIGNING_KEY in .env is sufficient — no config.go change
// needed — as long as config.Load has run once in the process (true for
// every dandori entrypoint: serve, CLI commands via openStore/loadConfig).
const signingKeyEnv = "DANDORI_AUDIT_SIGNING_KEY"

var (
	warnUnsignedOnce sync.Once
)

// loadSigningKey reads and decodes DANDORI_AUDIT_SIGNING_KEY. It accepts
// either a base64-encoded 64-byte ed25519.PrivateKey (seed+pubkey, as
// produced by ed25519.GenerateKey and printed by `dandori audit keygen`) or a
// base64-encoded 32-byte seed (ed25519.NewKeyFromSeed). Returns (nil, false)
// when unset — callers fall back to unsigned mode.
func loadSigningKey() (ed25519.PrivateKey, bool) {
	v := os.Getenv(signingKeyEnv)
	if v == "" {
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		log.Printf("audit: %s is set but not valid base64 — running UNSIGNED: %v", signingKeyEnv, err)
		return nil, false
	}
	switch len(raw) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), true
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), true
	default:
		log.Printf("audit: %s has invalid length %d (want %d or %d) — running UNSIGNED",
			signingKeyEnv, len(raw), ed25519.SeedSize, ed25519.PrivateKeySize)
		return nil, false
	}
}

// warnUnsignedOnceLog logs, exactly once per process, that audit rows are
// being written without a signature (no key configured). Silence would let
// an operator not notice they never enabled tamper-evidence beyond the
// in-DB hash chain.
func warnUnsignedOnceLog() {
	warnUnsignedOnce.Do(func() {
		log.Printf("audit: %s not set — audit rows are UNSIGNED (hash-chain only, no co-sign). "+
			"Run `dandori audit keygen` and set %s to enable tamper-evident signing.", signingKeyEnv, signingKeyEnv)
	})
}

// signHash signs the canonical chain-hash hex string (as bytes) and returns
// the raw 64-byte Ed25519 signature.
func signHash(priv ed25519.PrivateKey, hash string) []byte {
	return ed25519.Sign(priv, []byte(hash))
}

// verifyRow checks a row's signature against the public key registered for
// its key_id. pubByKeyID is normally {DefaultKeyID: <configured pubkey>};
// the map shape is what lets future key rotation add more entries without
// changing this function's signature.
func verifyRow(pubByKeyID map[int]ed25519.PublicKey, keyID int, hash string, sig []byte) bool {
	pub, ok := pubByKeyID[keyID]
	if !ok || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, []byte(hash), sig)
}

// GenerateSigningKeypair creates a new Ed25519 keypair for `dandori audit
// keygen`. Returns the private key base64 (to set as DANDORI_AUDIT_SIGNING_KEY)
// and the public key base64 (for out-of-band distribution / pinning).
func GenerateSigningKeypair() (privB64, pubB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", fmt.Errorf("generate ed25519 keypair: %w", err)
	}
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub), nil
}

// PublicKeyFromSigningKey derives the base64 public key from the configured
// signing key, for `dandori audit pubkey` and the /ingest/audit-pubkey route.
// Returns ok=false when no signing key is configured.
func PublicKeyFromSigningKey() (pubB64 string, ok bool) {
	priv, has := loadSigningKey()
	if !has {
		return "", false
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(pub), true
}

// PubkeyFingerprint hashes the DECODED raw public key bytes (always the same
// canonical form regardless of source) and returns an error rather than
// silently falling back to hashing the base64 text — hashing two different
// representations of "the same key" would produce two different
// fingerprints for what an operator expects to be one stable identifier.
// This is the single implementation `dandori audit pubkey`/`keygen` (via
// internal/cli's fingerprint wrapper) and the compliance export both use, so
// the value an auditor pins out-of-band and the value in the export are
// always computed the exact same way.
func PubkeyFingerprint(pubB64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}
