package govern

import (
	"crypto/ed25519"
	"encoding/base64"
	"strconv"
)

// approveSignBytes is the exact byte sequence signed/verified for a
// per-publish approve-hash attestation — length-prefixed the same way
// canonicalHash/checkpointSignBytes are, so the same delimiter-shift
// argument applies: no ambiguity between (unitID="1", hash="23") and
// (unitID="12", hash="3"), since each field carries its own length prefix.
func approveSignBytes(unitID int64, approveHash string) []byte {
	fields := []string{strconv.FormatInt(unitID, 10), approveHash}
	var buf []byte
	for _, f := range fields {
		buf = append(buf, byte(len(f)>>24), byte(len(f)>>16), byte(len(f)>>8), byte(len(f)))
		buf = append(buf, f...)
	}
	return buf
}

// SignApproveHash signs (unitID, approveHash) with the configured audit
// signing key and returns the base64 Ed25519 signature. This is the anchor a
// central-mode distribution response carries: it binds the EXACT approve
// hash a client's byte-gate already checked the received bytes against to a
// signature only the key-holder can produce, independent of the audit
// checkpoint/tip machinery (a checkpoint's own signature never covers any
// particular unit's approve-hash — only the chain tip at signing time).
// Returns ok=false when no signing key is configured (unsigned mode) —
// callers must treat that as "no anchor available", not as a valid anchor.
func SignApproveHash(unitID int64, approveHash string) (sigB64 string, ok bool) {
	priv, has := loadSigningKey()
	if !has {
		return "", false
	}
	sig := ed25519.Sign(priv, approveSignBytes(unitID, approveHash))
	return base64.StdEncoding.EncodeToString(sig), true
}

// VerifyApproveHash checks a base64 Ed25519 signature against
// (unitID, approveHash) and pub. This is the client-side anchor check for
// central-mode skill/kit pull: a malicious server can freely choose its own
// approveHash to match forged bytes (the byte-gate alone does not stop
// that — see verifyByteHash's doc comment), but it cannot produce a
// signature that verifies here without the private signing key, since the
// signed material includes the exact approve_hash the byte-gate already
// pinned the received bytes to.
func VerifyApproveHash(pub ed25519.PublicKey, unitID int64, approveHash, sigB64 string) bool {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, approveSignBytes(unitID, approveHash), sig)
}
