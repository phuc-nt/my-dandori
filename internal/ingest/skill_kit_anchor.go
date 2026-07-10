// Client-side anchor verification for central-mode skill/kit pull.
//
// Threat model: a malicious or fully compromised server controls EVERY field
// in its own response, including approve_hash and approve_id — these are NOT
// independently verifiable facts, they are just numbers the server chose to
// send. So a byte-gate that only checks "sha256(received bytes) ==
// server-supplied approve_hash" is trivially satisfiable: the server picks
// approve_hash = sha256(whatever malicious bytes it wants to serve). Serving
// a self-consistent response (body/content_hash/approve_hash all agreeing
// with each other) proves nothing, because the server authored all of them.
//
// The real anchor has to bind the approve_hash itself to something the
// server cannot forge: a signature, produced by the audit signing private
// key, over EXACTLY (unit_id, approve_hash) — see
// govern.SignApproveHash/VerifyApproveHash. The client fetches the public
// key over the same authenticated channel (AuditPubkey) and verifies that
// signature against the SAME approve_hash the byte-gate already checked the
// received bytes against. A server without the private key can still choose
// any approve_hash it likes, but it cannot produce a signature for that
// hash that verifies against the real public key — so a forged
// (body, approve_hash) pair fails here even though it passes the byte-gate.
//
// The checkpoint/tip fields are served only as a secondary, non-authoritative
// sanity signal (a checkpoint existing and covering the row is reassuring,
// but its absence or staleness is not itself proof of forgery, and its
// presence is not itself proof of legitimacy for any SPECIFIC unit — a
// checkpoint only attests to the chain's tip, never to any one unit's
// approve_hash). The approve_sig check above is the actual security
// boundary.
//
// On a single box where an attacker holds BOTH the private signing key and
// write access to the server process, this reduces to the same residual risk
// Phase 1 already documents for the checkpoint mechanism itself: signing
// forged content with the real key is indistinguishable from a legitimate
// publish. This phase does not solve key custody — it only makes it
// impossible to forge content WITHOUT the key, whether the attacker is a
// MITM, a compromised server missing the key, or a spoofed endpoint.
package ingest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/phuc-nt/dandori/internal/govern"
)

// ErrByteHashMismatch marks the byte-gate failing: the bytes received over
// the network do not hash to the server-claimed approve_hash. This is the
// specific error the RCE-closing test asserts on.
var ErrByteHashMismatch = fmt.Errorf("received bytes do not match the claimed approve-hash — refusing to write")

// ErrAnchorInvalid marks the approve-hash signature check failing: no valid
// signature (over exactly this unit's approve_hash) verifies against the
// server's own advertised public key.
var ErrAnchorInvalid = fmt.Errorf("audit anchor invalid — approve-hash is not validly signed by the audit key")

// verifyByteHash is the first gate: sha256(receivedBytes) must equal
// approveHash. Called on the manifest/skill body FIRST, before any other
// check (including manifest-internal consistency) — see kit_cmd.go/
// skill_cmd.go's central pull path for where this is wired in. On its own
// this only proves internal self-consistency (see file doc comment) — it
// must always be paired with VerifyAnchor's signature check.
func verifyByteHash(receivedBytes []byte, approveHash string) error {
	sum := sha256.Sum256(receivedBytes)
	got := hex.EncodeToString(sum[:])
	if got != approveHash {
		return fmt.Errorf("%w: sha256(received)=%s approve_hash=%s", ErrByteHashMismatch, got, approveHash)
	}
	return nil
}

// AuditPubkey fetches the server's audit signing public key over the
// authenticated ingest channel (GET /ingest/audit-pubkey). Returns ok=false
// (not an error) when the server has no signing key configured — unsigned
// mode is a legitimate, if weaker, deployment state; VerifyAnchor treats
// "no key" as "no anchor available" and the caller decides whether to
// proceed (central pull refuses in that case — see kit_cmd.go/skill_cmd.go).
func (c *Client) AuditPubkey() (pub ed25519.PublicKey, ok bool, err error) {
	req, err := http.NewRequest(http.MethodGet, c.cfg.ServerURL+"/ingest/audit-pubkey", nil)
	if err != nil {
		return nil, false, err
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("ingest: fetch audit pubkey: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil // no signing key configured on this server
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("ingest: audit-pubkey returned %s", resp.Status)
	}
	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, false, fmt.Errorf("ingest: decode audit-pubkey response: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(body.PublicKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, false, fmt.Errorf("ingest: audit-pubkey response is not a valid ed25519 public key")
	}
	return ed25519.PublicKey(raw), true, nil
}

// VerifyAnchor is the real security boundary: it requires a valid Ed25519
// signature, produced by pub's matching private key, over exactly
// (unitID, approveHash) — see govern.VerifyApproveHash. A server without the
// private key can set approveHash to whatever it wants (to match forged
// bytes), but cannot produce a signature that verifies here for that forged
// value, since the signed material is pinned to the SAME approveHash the
// byte-gate already checked the received bytes against.
//
// cp (the checkpoint) is accepted only as a secondary, non-authoritative
// signal: when present its own signature is checked, but a missing or
// signature-invalid checkpoint does NOT by itself fail the anchor — a
// checkpoint never attested to any specific unit's approve_hash in the first
// place (it only ever covered the audit chain's tip), so it cannot serve as
// proof either way for this one unit. Keeping the check here (rather than
// dropping it entirely) still catches a server whose checkpoint file is
// internally corrupt or was signed with a different key than pub, which is
// worth surfacing even though it is not the primary gate.
func VerifyAnchor(pub ed25519.PublicKey, unitID int64, approveHash, approveSig string, cp *checkpointPayload) error {
	if approveSig == "" {
		return fmt.Errorf("%w: server served no approve-hash signature", ErrAnchorInvalid)
	}
	if !govern.VerifyApproveHash(pub, unitID, approveHash, approveSig) {
		return fmt.Errorf("%w: approve-hash signature does not verify for this unit", ErrAnchorInvalid)
	}
	if cp != nil {
		full := govern.Checkpoint{
			TipID: cp.TipID, TipHash: cp.TipHash, TS: cp.TS,
			FirstSignedID: cp.FirstSignedID, KeyID: cp.KeyID, Signature: cp.Signature,
		}
		if !full.VerifySignature(pub) {
			return fmt.Errorf("%w: served checkpoint signature does not verify", ErrAnchorInvalid)
		}
	}
	return nil
}

// VerifySkillFetch runs the full gate on a central-mode skill fetch, IN
// ORDER: (1) byte-gate — sha256(received body) == approve_hash, on the bytes
// as received, before anything else; (2) anchor — a valid signature over
// exactly (unit_id, approve_hash) verifies against the server's own
// advertised public key. Both must pass before the caller may treat
// resp.Body as safe to write. Manifest-internal consistency (content_hash
// agreeing with approve_hash) is intentionally NOT checked here — that would
// only prove the response is self-consistent, which is exactly what a
// forged response can also achieve; skillreg.Verify (called by the CLI
// afterward with the SAME fetched fields) still runs the existing 3-way
// check for defense in depth, but it is not a substitute for this gate.
func VerifySkillFetch(c *Client, resp *skillFetchResponse) error {
	if err := verifyByteHash([]byte(resp.Body), resp.ApproveHash); err != nil {
		return err
	}
	pub, ok, err := c.AuditPubkey()
	if err != nil {
		return fmt.Errorf("ingest: fetch audit pubkey for anchor check: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: server has no audit signing key configured", ErrAnchorInvalid)
	}
	return VerifyAnchor(pub, resp.UnitID, resp.ApproveHash, resp.ApproveSig, resp.Checkpoint)
}

// VerifyKitFetch is VerifySkillFetch's kit counterpart: the byte-gate runs
// over the manifest body (per-file bodies are hashed against the manifest's
// OWN per-file content_hash by the existing runKitPull loop — this function
// only gates the manifest body itself against the approve_hash, matching
// what verifyKitManifest already expected to receive). Per-file bytes are
// verified downstream by kit_cmd.go's unchanged per-file loop, reusing
// skillreg.KitLocalPath / LocalHash exactly as the local pull path does.
func VerifyKitFetch(c *Client, resp *kitFetchResponse) error {
	if err := verifyByteHash([]byte(resp.Body), resp.ApproveHash); err != nil {
		return err
	}
	pub, ok, err := c.AuditPubkey()
	if err != nil {
		return fmt.Errorf("ingest: fetch audit pubkey for anchor check: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: server has no audit signing key configured", ErrAnchorInvalid)
	}
	return VerifyAnchor(pub, resp.UnitID, resp.ApproveHash, resp.ApproveSig, resp.Checkpoint)
}
