package govern

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

// TestVerifyKeyRequiredModeCatchesMarkerDeletion: the strongest version of
// the rebuild attack — strip every row's signature AND delete the in-DB
// first_signed_id marker, in the window before any checkpoint exists (fewer
// than checkpointEveryN rows, no export run yet). Without key-required mode
// this passes as "intact": the hash chain is self-consistent and there is no
// marker left, no checkpoint to compare against. With a signing key
// currently configured, Verify must refuse to call an unsigned tail "intact".
func TestVerifyKeyRequiredModeCatchesMarkerDeletion(t *testing.T) {
	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	st := openTestStore(t)

	a := &Audit{St: st, Actor: "tester"}
	for i := 0; i < 3; i++ {
		if _, err := a.Append("act", "subj", "detail"); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	// A checkpoint WAS written immediately at signing-enable time (row 1) —
	// delete it too, to simulate an attacker who also controls the
	// checkpoint directory and removed the anchor along with the marker.
	dir, _ := resolveCheckpointDirs()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read checkpoint dir: %v", err)
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			t.Fatalf("remove checkpoint %s: %v", e.Name(), err)
		}
	}

	if _, err := st.DB.Exec(`UPDATE audit_log SET signature = NULL, key_id = NULL`); err != nil {
		t.Fatalf("strip signatures: %v", err)
	}
	if _, err := st.DB.Exec(`DELETE FROM settings WHERE key = ?`, firstSignedIDSettingKey); err != nil {
		t.Fatalf("delete first_signed_id marker: %v", err)
	}

	broken, reason, err := Verify(st)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if reason != "signature" {
		t.Fatalf("Verify with signing key configured, unsigned tail, no checkpoint, no marker: broken=%d reason=%q, want reason=signature (key-required mode)", broken, reason)
	}
}

// TestVerifyCatchesMarkerDeletionViaCheckpoint: same attack as
// TestVerifyKeyRequiredModeCatchesMarkerDeletion (strip signatures + delete
// the in-DB marker), but this time the checkpoint written at signing-enable
// time is LEFT IN PLACE. The checkpoint's own signed payload still asserts
// first_signed_id, independent of the now-deleted in-DB marker, so Verify
// must catch the unsigned tail via the checkpoint's attestation rather than
// relying on key-required mode.
func TestVerifyCatchesMarkerDeletionViaCheckpoint(t *testing.T) {
	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	st := openTestStore(t)

	a := &Audit{St: st, Actor: "tester"}
	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := a.Append("act", "subj", "detail")
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		ids = append(ids, id)
	}

	if _, err := st.DB.Exec(`UPDATE audit_log SET signature = NULL, key_id = NULL WHERE id > ?`, ids[0]); err != nil {
		t.Fatalf("strip signatures on tail: %v", err)
	}
	if _, err := st.DB.Exec(`DELETE FROM settings WHERE key = ?`, firstSignedIDSettingKey); err != nil {
		t.Fatalf("delete first_signed_id marker: %v", err)
	}

	broken, reason, err := Verify(st)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if reason != "signature" {
		t.Fatalf("Verify after marker deletion with checkpoint still present: broken=%d reason=%q, want reason=signature (checkpoint attests first_signed_id independently)", broken, reason)
	}
}

// TestVerifyRejectsForgedCheckpoint: a checkpoint file whose TipID/TipHash
// were rewritten to match a rebuilt chain, but signed with a DIFFERENT key,
// must be rejected outright — Verify must never silently fall back to "no
// checkpoint" when a checkpoint file exists but fails its own signature
// check.
func TestVerifyRejectsForgedCheckpoint(t *testing.T) {
	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	st := openTestStore(t)

	a := &Audit{St: st, Actor: "tester"}
	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := a.Append("act", "subj", "detail")
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		ids = append(ids, id)
	}

	// Attacker rebuilds the chain (re-signs every row correctly with the
	// REAL key, so per-row signature checks all pass) but wants to also
	// forge the checkpoint to match — using a DIFFERENT key, since the real
	// key is what an operator would rotate away/hold separately in a
	// stronger threat model. The forged checkpoint's tip_id/tip_hash matches
	// the current (rebuilt) chain exactly, so a signature-blind comparison
	// would report "intact".
	_, forgedPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate forged key: %v", err)
	}
	var tipHash string
	st.DB.QueryRow(`SELECT hash FROM audit_log WHERE id = ?`, ids[2]).Scan(&tipHash)
	dir, offsite := resolveCheckpointDirs()
	if err := WriteCheckpoint(forgedPriv, dir, offsite, ids[2], tipHash, "2026-01-01T00:00:00Z", ids[0]); err != nil {
		t.Fatalf("write forged checkpoint: %v", err)
	}

	broken, reason, err := Verify(st)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if reason != "checkpoint" {
		t.Fatalf("Verify with a checkpoint signed by the wrong key: broken=%d reason=%q, want reason=checkpoint", broken, reason)
	}
}
