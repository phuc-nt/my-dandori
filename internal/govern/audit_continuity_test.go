package govern

import (
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// P2 folds three distinct hardcoded actor helpers (execActor/launchActor/
// chatPrincipal) and several raw Cfg.UserName call sites into one request-aware
// s.actor(r). Verify walks hashes "as-is" (audit.go Verify) and never parses
// the actor string, so a chain mixing the pre-P2 "@console" shape with the
// new principal namespaces (real login, "slack:<id>", "system@applier") must
// stay intact — and a tamper must still be caught at the exact broken row.
func TestAuditChainMixedActorNamespaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "continuity.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	entries := []struct {
		actor, action, subject, detail string
	}{
		{"phuc@console", "run_launched", "run1", "pre-P2 local-trust entry"},
		{"phuc@dandori.dev", "approval_approved", "approval:1", "real logged-in principal"},
		{"slack:U123", "approval_approved", "approval:2", "via slack reaction"},
		{"system@applier", "context_saved", "team:eng", "background observer applier"},
	}

	var ids []int64
	for _, e := range entries {
		a := &Audit{St: st, Actor: e.actor}
		id, err := a.Append(e.action, e.subject, e.detail)
		if err != nil {
			t.Fatalf("append actor=%q: %v", e.actor, err)
		}
		ids = append(ids, id)
	}

	if broken, reason, err := Verify(st); err != nil || reason != "" {
		t.Fatalf("chain broken at %d reason=%q (err %v), want intact across mixed actor namespaces", broken, reason, err)
	}

	// Tamper the third entry's detail (slack: namespace) — Verify must report
	// exactly that row's id, not an earlier or later one.
	tamperID := ids[2]
	if _, err := st.DB.Exec(`UPDATE audit_log SET detail = 'tampered' WHERE id = ?`, tamperID); err != nil {
		t.Fatal(err)
	}
	broken, reason, err := Verify(st)
	if err != nil {
		t.Fatal(err)
	}
	if broken != tamperID || reason != "chain" {
		t.Errorf("Verify reported broken id %d reason=%q, want %d reason=chain", broken, reason, tamperID)
	}
}
