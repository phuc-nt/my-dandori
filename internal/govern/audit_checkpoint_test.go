package govern

import (
	"crypto/ed25519"
	"path/filepath"
	"testing"
)

// TestCheckpointWriteAndRead: WriteCheckpoint signs (chainLen, tipHash, ts)
// and LatestCheckpoint reads back the highest chain_len file present.
func TestCheckpointWriteAndRead(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "checkpoints")

	if err := WriteCheckpoint(priv, dir, "", 10, "hash-at-10", "2026-01-01T00:00:00Z", 0); err != nil {
		t.Fatalf("write checkpoint 10: %v", err)
	}
	if err := WriteCheckpoint(priv, dir, "", 20, "hash-at-20", "2026-01-02T00:00:00Z", 0); err != nil {
		t.Fatalf("write checkpoint 20: %v", err)
	}

	cp, ok, err := LatestCheckpoint(dir)
	if err != nil {
		t.Fatalf("LatestCheckpoint: %v", err)
	}
	if !ok {
		t.Fatal("LatestCheckpoint reported no checkpoint found")
	}
	if cp.TipID != 20 || cp.TipHash != "hash-at-20" {
		t.Errorf("LatestCheckpoint = {%d, %q}, want {20, hash-at-20}", cp.TipID, cp.TipHash)
	}
	if !cp.VerifySignature(pub) {
		t.Error("checkpoint signature failed to verify against its own signing key")
	}
}

// TestCheckpointOffsiteCopy: when offsiteDir is set, WriteCheckpoint copies
// the same checkpoint file there too.
func TestCheckpointOffsiteCopy(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "checkpoints")
	offsite := filepath.Join(t.TempDir(), "offsite")

	if err := WriteCheckpoint(priv, dir, offsite, 5, "hash-at-5", "2026-01-01T00:00:00Z", 0); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}

	cp, ok, err := LatestCheckpoint(offsite)
	if err != nil || !ok {
		t.Fatalf("LatestCheckpoint(offsite): ok=%v err=%v", ok, err)
	}
	if cp.TipID != 5 {
		t.Errorf("offsite checkpoint tip_id = %d, want 5", cp.TipID)
	}
}

// TestLatestCheckpointMissingDirIsNotError: no checkpoint ever written
// (fresh install, or signing never enabled) must return ok=false, not error.
func TestLatestCheckpointMissingDirIsNotError(t *testing.T) {
	_, ok, err := LatestCheckpoint(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("LatestCheckpoint on missing dir: %v", err)
	}
	if ok {
		t.Error("LatestCheckpoint on missing dir reported ok=true")
	}
}

// TestVerifyDetectsTailTruncation: deleting tail rows leaves the remaining
// chain internally hash- AND signature-consistent, but the chain is now
// shorter than the latest checkpoint already written — Verify must catch
// this via the checkpoint comparison (reason "truncated"). This is the one
// attack neither the hash chain nor per-row signatures can detect on their
// own — only an external reference outside the database can.
func TestVerifyDetectsTailTruncation(t *testing.T) {
	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	st := openTestStore(t)

	a := &Audit{St: st, Actor: "tester"}
	var ids []int64
	for i := 0; i < 5; i++ {
		id, err := a.Append("act", "subj", "detail")
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		ids = append(ids, id)
	}

	// Checkpoint the full 5-row chain (simulating the every-N cadence or an
	// export having already anchored this length).
	priv, ok := loadSigningKey()
	if !ok {
		t.Fatal("signing key not loaded")
	}
	var tipHash string
	st.DB.QueryRow(`SELECT hash FROM audit_log WHERE id = ?`, ids[4]).Scan(&tipHash)
	dir, offsite := resolveCheckpointDirs()
	if err := WriteCheckpoint(priv, dir, offsite, ids[4], tipHash, "2026-01-01T00:00:00Z", ids[0]); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}

	// Delete the tail two rows — the remaining chain (1..3) is still
	// perfectly hash- and signature-consistent on its own.
	if _, err := st.DB.Exec(`DELETE FROM audit_log WHERE id > ?`, ids[2]); err != nil {
		t.Fatalf("delete tail rows: %v", err)
	}

	broken, reason, err := Verify(st)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if reason != "truncated" {
		t.Fatalf("Verify after tail deletion: broken=%d reason=%q, want reason=truncated", broken, reason)
	}
}

// TestCheckpointWrittenAfterNAppends: the every-N-rows cadence in AppendTx
// actually produces a checkpoint file once the row count crosses the
// threshold, and LatestCheckpoint can read it back. Also proves the periodic
// checkpoint's embedded first_signed_id points at the TRUE first signed row
// (row 1), not this checkpoint's own tip — a checkpoint written on the
// periodic branch must not narrow the monotonic floor it attests to.
func TestCheckpointWrittenAfterNAppends(t *testing.T) {
	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	st := openTestStore(t)

	a := &Audit{St: st, Actor: "tester"}
	var firstID int64
	for i := 0; i < checkpointEveryN; i++ {
		id, err := a.Append("act", "subj", "detail")
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if i == 0 {
			firstID = id
		}
	}

	dir, _ := resolveCheckpointDirs()
	cp, ok, err := LatestCheckpoint(dir)
	if err != nil {
		t.Fatalf("LatestCheckpoint: %v", err)
	}
	if !ok {
		t.Fatal("no checkpoint written after checkpointEveryN appends")
	}
	if cp.TipID != checkpointEveryN {
		t.Errorf("checkpoint tip_id = %d, want %d", cp.TipID, checkpointEveryN)
	}
	if cp.FirstSignedID != firstID {
		t.Errorf("periodic checkpoint first_signed_id = %d, want %d (the true first signed row, not the current tip)", cp.FirstSignedID, firstID)
	}
}

// TestCheckpointWrittenImmediatelyOnSigningEnable: the very first signed
// row must produce a checkpoint right away, not only once row count crosses
// checkpointEveryN — otherwise a rebuild-and-strip-signatures attack in the
// window before row 100 (or before any export runs) would have no external
// anchor to be caught against at all.
func TestCheckpointWrittenImmediatelyOnSigningEnable(t *testing.T) {
	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	st := openTestStore(t)

	a := &Audit{St: st, Actor: "tester"}
	id, err := a.Append("act", "subj", "detail")
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	dir, _ := resolveCheckpointDirs()
	cp, ok, err := LatestCheckpoint(dir)
	if err != nil {
		t.Fatalf("LatestCheckpoint: %v", err)
	}
	if !ok {
		t.Fatal("no checkpoint written after the first signed row")
	}
	if cp.TipID != id {
		t.Errorf("checkpoint tip_id = %d, want %d (the first signed row)", cp.TipID, id)
	}
	if cp.FirstSignedID != id {
		t.Errorf("checkpoint first_signed_id = %d, want %d", cp.FirstSignedID, id)
	}
}
