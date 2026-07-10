package govern

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// checkpointEveryN is how often (in appended rows) a checkpoint is written
// automatically. Signing-enable time and export also write one on demand
// regardless of this cadence — see recordFirstSignedID and
// writeExportCheckpoint.
const checkpointEveryN = 100

// DefaultCheckpointDir is the checkpoint sink. Writing here alone is NOT the
// trust-root — a plain file in a directory the same process/uid can write is
// rewritable by anyone with that access, same as the database itself. What
// actually raises the bar over the in-DB hash chain and per-row signatures:
//
//  1. The checkpoint's OWN signature is verified (VerifySignature) before
//     it is trusted for anything — a forged or unsigned checkpoint file is
//     rejected outright, so tampering requires possessing the signing key,
//     not just write access to this directory.
//  2. This directory is (best-effort) committed to git on every write. On a
//     single box where the signing key also lives, an attacker with both
//     the key AND box access can still re-sign a forged checkpoint and force
//     a new commit — git alone does not defend against that combination.
//     The real defense against a fully-compromised box is copying checkpoints
//     somewhere the attacker cannot rewrite: a remote git push, or the
//     offsite path via DANDORI_AUDIT_CHECKPOINT_OFFSITE pointed at storage
//     the attacker does not control. Local git history only helps against a
//     weaker attacker who can edit files but not rewrite pushed git history.
const DefaultCheckpointDir = "docs/audit-checkpoints"

// Checkpoint is a signed attestation of the chain's state at a point in
// time: its tip id/hash, and whether signing had been turned on by then (and
// if so, at which row). FirstSignedID is part of the SIGNED payload
// specifically so that deleting the in-DB first_signed_id marker cannot
// erase the fact that signing was enabled — the last checkpoint taken while
// signing was on still attests to it independently of the database.
type Checkpoint struct {
	TipID         int64  `json:"tip_id"`
	TipHash       string `json:"tip_hash"`
	TS            string `json:"ts"`
	FirstSignedID int64  `json:"first_signed_id,omitempty"`
	KeyID         int    `json:"key_id"`
	Signature     string `json:"signature"` // base64 ed25519 signature over the canonical checkpoint bytes
}

// checkpointSignBytes is the exact byte sequence signed/verified for a
// checkpoint — length-prefixed the same way canonicalHash is, so the same
// delimiter-shift argument applies (all of these are variable-width text
// representations here).
func checkpointSignBytes(tipID int64, tipHash, ts string, firstSignedID int64) []byte {
	fields := []string{
		strconv.FormatInt(tipID, 10),
		tipHash,
		ts,
		strconv.FormatInt(firstSignedID, 10),
	}
	var buf []byte
	for _, f := range fields {
		buf = append(buf, byte(len(f)>>24), byte(len(f)>>16), byte(len(f)>>8), byte(len(f)))
		buf = append(buf, f...)
	}
	return buf
}

// signCheckpoint signs (tipID, tipHash, ts, firstSignedID) with priv, key id
// DefaultKeyID.
func signCheckpoint(priv ed25519.PrivateKey, tipID int64, tipHash, ts string, firstSignedID int64) Checkpoint {
	sig := ed25519.Sign(priv, checkpointSignBytes(tipID, tipHash, ts, firstSignedID))
	return Checkpoint{
		TipID:         tipID,
		TipHash:       tipHash,
		TS:            ts,
		FirstSignedID: firstSignedID,
		KeyID:         DefaultKeyID,
		Signature:     base64.StdEncoding.EncodeToString(sig),
	}
}

// VerifySignature checks the checkpoint's own signature against pub. A
// checkpoint file is only a trust anchor once this passes — a checkpoint
// whose signature does not verify must be treated as forged/tampered, never
// as "no checkpoint" (which would silently drop back to trusting the
// in-database chain alone).
func (c Checkpoint) VerifySignature(pub ed25519.PublicKey) bool {
	sig, err := base64.StdEncoding.DecodeString(c.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, checkpointSignBytes(c.TipID, c.TipHash, c.TS, c.FirstSignedID), sig)
}

// WriteCheckpoint signs (tipID, tipHash, ts, firstSignedID) and writes it to
// dir as "<tipID>.json", plus a copy to offsiteDir when non-empty, plus a
// best-effort git commit of dir. All three failure modes (offsite copy, git
// add/commit) are logged but never fail the checkpoint write itself — an
// operator who cares about the offsite/git guarantee monitors those log
// lines; a missing guarantee must not silently block audit appends.
func WriteCheckpoint(priv ed25519.PrivateKey, dir, offsiteDir string, tipID int64, tipHash, ts string, firstSignedID int64) error {
	cp := signCheckpoint(priv, tipID, tipHash, ts, firstSignedID)
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create checkpoint dir %s: %w", dir, err)
	}
	name := strconv.FormatInt(tipID, 10) + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write checkpoint %s: %w", path, err)
	}
	gitCommitCheckpoint(dir, name)
	if offsiteDir != "" {
		if err := os.MkdirAll(offsiteDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "audit: checkpoint offsite dir %s unavailable: %v\n", offsiteDir, err)
			return nil
		}
		if err := os.WriteFile(filepath.Join(offsiteDir, name), b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "audit: checkpoint offsite copy to %s failed: %v\n", offsiteDir, err)
		}
	}
	return nil
}

// gitCommitCheckpoint best-effort stages and commits the checkpoint file
// within dir's git repo, if any. This is automation for the common case
// (dir lives inside a git-tracked docs/ folder); it is not itself the
// security boundary — see the DefaultCheckpointDir doc comment. Silently
// does nothing (beyond a log line) when dir is not inside a git repo, git is
// unavailable, or the commit fails for any reason — never blocks the caller.
func gitCommitCheckpoint(dir, name string) {
	if _, err := exec.LookPath("git"); err != nil {
		return // no git binary — not an error, just no automation available
	}
	checkGit := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	if err := checkGit.Run(); err != nil {
		return // dir is not inside a git work tree — nothing to commit to
	}
	addCmd := exec.Command("git", "-C", dir, "add", name)
	if out, err := addCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "audit: git add checkpoint %s failed: %v (%s)\n", name, err, strings.TrimSpace(string(out)))
		return
	}
	commitCmd := exec.Command("git", "-C", dir, "commit", "-m", "audit checkpoint "+name, "--", name)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		// A no-op commit (file unchanged) is a normal, expected failure mode
		// here — e.g. re-running WriteCheckpoint for the same tipID — not
		// worth logging as an error.
		if !strings.Contains(string(out), "nothing to commit") {
			fmt.Fprintf(os.Stderr, "audit: git commit checkpoint %s failed: %v (%s)\n", name, err, strings.TrimSpace(string(out)))
		}
	}
}

// LatestCheckpoint reads the checkpoint with the highest TipID from dir.
// Returns ok=false when dir has no checkpoint files (fresh install / no
// signing key ever configured — not an error).
func LatestCheckpoint(dir string) (cp Checkpoint, ok bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Checkpoint{}, false, nil
		}
		return Checkpoint{}, false, fmt.Errorf("read checkpoint dir %s: %w", dir, err)
	}
	var best Checkpoint
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		var cur Checkpoint
		if jerr := json.Unmarshal(b, &cur); jerr != nil {
			continue
		}
		if !found || cur.TipID > best.TipID {
			best = cur
			found = true
		}
	}
	if !found {
		return Checkpoint{}, false, nil
	}
	return best, true, nil
}
