package govern

import (
	"crypto/ed25519"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// genTestKey generates a fresh Ed25519 keypair and returns the base64 form
// DANDORI_AUDIT_SIGNING_KEY expects (the full private key, seed+pubkey).
func genTestKey(t *testing.T) (privB64 string, pub ed25519.PublicKey) {
	t.Helper()
	pubKey, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(priv), pubKey
}

// openTestStore isolates each test in its own SQLite file and points the
// checkpoint sink at a per-test temp dir so tests never share checkpoint
// state (or the git-tracked docs/audit-checkpoints/).
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	t.Setenv(auditCheckpointDirEnv, filepath.Join(t.TempDir(), "checkpoints"))
	return st
}

// TestAppendSignVerifyRoundTrip: with a signing key configured, appended
// rows carry a valid Ed25519 signature and Verify reports intact.
func TestAppendSignVerifyRoundTrip(t *testing.T) {
	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	st := openTestStore(t)

	a := &Audit{St: st, Actor: "tester"}
	for i := 0; i < 3; i++ {
		if _, err := a.Append("act", "subj", "detail"); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	var sigCount int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE signature IS NOT NULL`).Scan(&sigCount)
	if sigCount != 3 {
		t.Fatalf("signed row count = %d, want 3", sigCount)
	}

	if broken, reason, err := Verify(st); err != nil || reason != "" {
		t.Fatalf("Verify on freshly signed chain: broken=%d reason=%q err=%v, want intact", broken, reason, err)
	}
}

// TestVerifyDetectsEditedRow: editing one row's detail (chain tamper) must
// report reason "chain" regardless of signing mode.
func TestVerifyDetectsEditedRow(t *testing.T) {
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

	if _, err := st.DB.Exec(`UPDATE audit_log SET detail = 'forged' WHERE id = ?`, ids[1]); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	broken, reason, err := Verify(st)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if broken != ids[1] || reason != "chain" {
		t.Errorf("Verify after edit: broken=%d reason=%q, want %d/chain", broken, reason, ids[1])
	}
}

// TestVerifyDetectsRebuildWithDifferentKey: rebuilding every row's signature
// with a DIFFERENT (attacker) key, while keeping the hash chain internally
// consistent, must be caught as reason "signature" — proving co-sign alone
// (not just the hash chain) is being checked.
func TestVerifyDetectsRebuildWithDifferentKey(t *testing.T) {
	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	st := openTestStore(t)

	a := &Audit{St: st, Actor: "tester"}
	for i := 0; i < 3; i++ {
		if _, err := a.Append("act", "subj", "detail"); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	// Attacker has a different key and re-signs every row with it — the
	// hash chain recomputes fine (content untouched), but key_id 1 still
	// points at the ORIGINAL public key, so the forged signature fails.
	_, fakePriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate fake key: %v", err)
	}
	rows, err := st.DB.Query(`SELECT id, hash FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	type idHash struct {
		id   int64
		hash string
	}
	var all []idHash
	for rows.Next() {
		var r idHash
		if err := rows.Scan(&r.id, &r.hash); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	rows.Close()
	for _, r := range all {
		forgedSig := ed25519.Sign(fakePriv, []byte(r.hash))
		if _, err := st.DB.Exec(`UPDATE audit_log SET signature = ? WHERE id = ?`, forgedSig, r.id); err != nil {
			t.Fatalf("forge signature: %v", err)
		}
	}

	broken, reason, err := Verify(st)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if reason != "signature" {
		t.Errorf("Verify after key-forged rebuild: broken=%d reason=%q, want reason=signature", broken, reason)
	}
}

// TestVerifyDetectsMonotonicDowngrade: rebuilding the chain with
// signature=NULL on every row after the first-signed row (attacker strips
// signatures instead of forging them) must be caught as reason "signature"
// via the monotonic-signing rule — THE key anti-rebuild-with-NULL test.
func TestVerifyDetectsMonotonicDowngrade(t *testing.T) {
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

	// Strip signatures from every row (as a from-scratch rebuild with no key
	// would produce) while leaving the hash chain itself internally
	// consistent — chain-only verification would report this as intact.
	if _, err := st.DB.Exec(`UPDATE audit_log SET signature = NULL, key_id = NULL`); err != nil {
		t.Fatalf("strip signatures: %v", err)
	}

	broken, reason, err := Verify(st)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if reason != "signature" {
		t.Fatalf("Verify after signature-NULL rebuild: broken=%d reason=%q, want reason=signature (monotonic check)", broken, reason)
	}
	// The FIRST signed row is exactly first_signed_id — it must not itself
	// be flagged (only rows AFTER it that are now unsigned are the attack).
	if broken == ids[0] {
		t.Errorf("Verify flagged the first-signed row itself (id %d); the monotonic rule should only flag rows strictly after first_signed_id", ids[0])
	}
}

// TestVerifyUnsignedModeBackwardCompat: with no signing key configured at
// all, Append works and Verify reports intact — unsigned installs are not
// broken by this feature.
func TestVerifyUnsignedModeBackwardCompat(t *testing.T) {
	st := openTestStore(t) // no signingKeyEnv set

	a := &Audit{St: st, Actor: "tester"}
	for i := 0; i < 3; i++ {
		if _, err := a.Append("act", "subj", "detail"); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	var sigCount int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE signature IS NOT NULL`).Scan(&sigCount)
	if sigCount != 0 {
		t.Errorf("unsigned mode wrote %d signatures, want 0", sigCount)
	}

	if broken, reason, err := Verify(st); err != nil || reason != "" {
		t.Fatalf("Verify in unsigned mode: broken=%d reason=%q err=%v, want intact", broken, reason, err)
	}
}

// TestAppendTxInsideOuterTransactionDoesNotDeadlock proves AppendTx can be
// called with a transaction the caller already opened, on the same
// single-connection write pool (store.Store.SetMaxOpenConns(1)) — a second
// Begin from inside would deadlock waiting for the first to release the only
// connection. This is the primitive a future ingest-batch applier needs.
func TestAppendTxInsideOuterTransactionDoesNotDeadlock(t *testing.T) {
	st := openTestStore(t)

	done := make(chan error, 1)
	go func() {
		tx, err := st.DB.Begin()
		if err != nil {
			done <- err
			return
		}
		a := &Audit{St: st, Actor: "tester"}
		if _, err := a.AppendTx(tx, "act", "subj", "detail"); err != nil {
			tx.Rollback()
			done <- err
			return
		}
		done <- tx.Commit()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AppendTx inside outer tx failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AppendTx inside outer tx hung (deadlock against SetMaxOpenConns(1))")
	}

	var count int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&count)
	if count != 1 {
		t.Errorf("row count after AppendTx-in-outer-tx = %d, want 1", count)
	}
}
