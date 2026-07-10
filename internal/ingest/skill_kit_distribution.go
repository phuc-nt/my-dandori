// Central-mode (P5) skill/kit distribution: GET /ingest/skill/{unit} and GET
// /ingest/kit/{unit} let a dev machine pull a unit published on a DIFFERENT
// machine's local store, closing the "central mode = [Sau]" gap `skill
// pull`/`kit pull` previously had. Kept in its own file (mirrors
// kit_path.go/kit_projection.go's split) rather than growing server.go.
//
// Threat model: serving the body and the approve-hash from the SAME
// self-reported response is not enough — a malicious or MITM'd server can
// freely choose an approve_hash that matches ANY bytes it wants to serve
// (approve_hash is just sha256 of whatever it decides to send), so a response
// being internally self-consistent proves nothing. The fix is client-side
// (internal/ingest/skill_kit_anchor.go + internal/cli's pull commands): the
// client recomputes sha256 over the ACTUAL bytes received and compares
// against approve_hash, THEN verifies approve_sig — an Ed25519 signature over
// exactly (unit_id, approve_hash) — against an independently-fetched public
// key. A server without the private signing key can forge approve_hash to
// match any bytes it likes, but cannot produce a signature that verifies for
// its forged hash. This file's job is only to serve every ingredient that
// check needs: body, approve-hash, approve-hash's per-unit signature, and
// (as a secondary, non-authoritative sanity check) the latest checkpoint.
package ingest

import (
	"encoding/json"
	"net/http"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/skillreg"
)

// checkpointPayload is the wire shape of a govern.Checkpoint — duplicated
// (not govern.Checkpoint directly) so this package's JSON contract is
// explicit and stable independent of any future field govern adds for
// server-internal use; every field here is one govern.Checkpoint already
// exports via JSON tags, so the two stay trivially in sync by construction.
type checkpointPayload struct {
	TipID         int64  `json:"tip_id"`
	TipHash       string `json:"tip_hash"`
	TS            string `json:"ts"`
	FirstSignedID int64  `json:"first_signed_id,omitempty"`
	KeyID         int    `json:"key_id"`
	Signature     string `json:"signature"`
}

func toCheckpointPayload(cp govern.Checkpoint) checkpointPayload {
	return checkpointPayload{
		TipID: cp.TipID, TipHash: cp.TipHash, TS: cp.TS,
		FirstSignedID: cp.FirstSignedID, KeyID: cp.KeyID, Signature: cp.Signature,
	}
}

// skillFetchResponse is what GET /ingest/skill/{unit} serves. Body is the
// verbatim skill body (the bytes the client must hash) — every other field is
// anchor material the client-side gate verifies BEFORE trusting Body.
// ApproveSig is the binding anchor: an Ed25519 signature over exactly
// (unit_id, approve_hash), so the client's verification is tied to the SAME
// approve_hash the byte-gate checked, not to an independent, forgeable
// range check. Checkpoint is served only as a secondary sanity signal (see
// VerifyAnchor) — it is not itself sufficient to trust Body.
type skillFetchResponse struct {
	Name        string             `json:"name"`
	UnitID      int64              `json:"unit_id"` // the unit approve_sig is signed over, together with ApproveHash
	Body        string             `json:"body"`
	ContentHash string             `json:"content_hash"`
	ApproveHash string             `json:"approve_hash"`
	ApproveID   int64              `json:"approve_id"` // audit_log row id the approve_hash was read from
	ApproveSig  string             `json:"approve_sig,omitempty"`
	Required    bool               `json:"required"`
	Checkpoint  *checkpointPayload `json:"checkpoint,omitempty"`
}

// kitFileFetch is one manifest file's verbatim body served alongside its
// path, for the client to hash and verify against the manifest's own
// per-file content_hash (unchanged from the local pull path).
type kitFileFetch struct {
	Path string `json:"path"`
	Body string `json:"body"`
}

// kitFetchResponse is what GET /ingest/kit/{unit} serves — the manifest body
// (Body, canonical KitManifest JSON) plus every per-file body, so the client
// can run the SAME 3-way manifest check (verifyKitManifest) and per-file hash
// check (runKitPull's existing loop) it already runs for a local pull, just
// fed with network-received bytes instead of local-store bytes.
type kitFetchResponse struct {
	Name        string             `json:"name"`
	UnitID      int64              `json:"unit_id"` // the unit approve_sig is signed over, together with ApproveHash
	Body        string             `json:"body"`
	Files       []kitFileFetch     `json:"files"`
	ContentHash string             `json:"content_hash"`
	ApproveHash string             `json:"approve_hash"`
	ApproveID   int64              `json:"approve_id"`
	ApproveSig  string             `json:"approve_sig,omitempty"`
	Checkpoint  *checkpointPayload `json:"checkpoint,omitempty"`
}

// latestCheckpointPayload best-effort reads the latest signed checkpoint —
// nil when no signing key was ever configured on this server (unsigned
// mode), which is a legitimate state the client-side gate must treat as "no
// anchor available" rather than an error serving the unit itself.
func latestCheckpointPayload() *checkpointPayload {
	cp, ok, err := govern.LatestCheckpoint(govern.CheckpointDir())
	if err != nil || !ok {
		return nil
	}
	p := toCheckpointPayload(cp)
	return &p
}

// handleFetchSkill serves one published skill unit for central-mode pull —
// the network counterpart to skillreg.Get + skillreg.ApproveHash the local
// pull path already runs. Bearer-authed like every other /ingest route; any
// authenticated operator may fetch any published skill (same posture as
// handleContext/handlePolicy — single shared trust boundary at MVP).
func (s *Server) handleFetchSkill(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("unit")
	sk, err := skillreg.Get(s.St, key)
	if err != nil {
		if err == skillreg.ErrNotFound {
			http.Error(w, "skill unit not found or not published", http.StatusNotFound)
			return
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	approveHash, approveID, err := skillreg.ApproveHashRow(s.St, sk.UnitID)
	if err != nil {
		http.Error(w, "cannot read audit approve-hash: "+err.Error(), http.StatusInternalServerError)
		return
	}
	approveSig, _ := govern.SignApproveHash(sk.UnitID, approveHash) // "" in unsigned mode — client refuses that case
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(skillFetchResponse{
		Name: sk.Name, UnitID: sk.UnitID, Body: sk.Body, ContentHash: sk.Hash,
		ApproveHash: approveHash, ApproveID: approveID, ApproveSig: approveSig, Required: sk.Required,
		Checkpoint: latestCheckpointPayload(),
	})
}

// handleFetchKit serves one published kit unit (manifest + every per-file
// body) for central-mode pull — the network counterpart to skillreg.GetKit +
// learn.KitFiles + skillreg.ApproveHash the local pull path already runs.
func (s *Server) handleFetchKit(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("unit")
	k, err := skillreg.GetKit(s.St, key)
	if err != nil {
		if err == skillreg.ErrNotFound {
			http.Error(w, "kit unit not found or not published", http.StatusNotFound)
			return
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	kitFiles, err := learn.KitFiles(s.St, k.UnitID)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	approveHash, approveID, err := skillreg.ApproveHashRow(s.St, k.UnitID)
	if err != nil {
		http.Error(w, "cannot read audit approve-hash: "+err.Error(), http.StatusInternalServerError)
		return
	}
	approveSig, _ := govern.SignApproveHash(k.UnitID, approveHash) // "" in unsigned mode — client refuses that case
	files := make([]kitFileFetch, 0, len(kitFiles))
	for _, f := range kitFiles {
		files = append(files, kitFileFetch{Path: f.Path, Body: f.Body})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(kitFetchResponse{
		Name: k.Name, UnitID: k.UnitID, Body: k.Body, Files: files, ContentHash: k.Hash,
		ApproveHash: approveHash, ApproveID: approveID, ApproveSig: approveSig,
		Checkpoint: latestCheckpointPayload(),
	})
}
