package govern

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// A retried gated action must reuse the pending approval, not spam the queue.
func TestGateRetryReusesPendingApproval(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "g1", 0)
	tc := bashCall("g1", "git push origin main")
	e.Evaluate(context.Background(), tc) // timeout deny → pending
	e.Evaluate(context.Background(), tc) // retry
	var n int
	e.St.DB.QueryRow(`SELECT count(*) FROM approvals WHERE run_id='g1' AND status='pending'`).Scan(&n)
	if n != 1 {
		t.Errorf("pending approvals: %d, want 1 (reuse)", n)
	}
}

func TestApprovalExpiry(t *testing.T) {
	e := testEngine(t)
	e.Cfg.ApprovalTTLMinutes = 30
	seedRun(t, e, "g2", 0)
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	e.St.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('g2','git push','gate', ?)`, old)
	e.St.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('g2','deploy','gate', ?)`, store.Now())

	e.ExpireStale()

	var expired, pending int
	e.St.DB.QueryRow(`SELECT count(*) FROM approvals WHERE status='expired'`).Scan(&expired)
	e.St.DB.QueryRow(`SELECT count(*) FROM approvals WHERE status='pending'`).Scan(&pending)
	if expired != 1 || pending != 1 {
		t.Errorf("expired=%d pending=%d, want 1/1", expired, pending)
	}
	var audits int
	e.St.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='approvals_expired'`).Scan(&audits)
	if audits != 1 {
		t.Errorf("expiry audit entries: %d", audits)
	}
	// After expiry, a new gate attempt creates a fresh approval for that action.
	tc := bashCall("g2", "git push origin main")
	e.Evaluate(context.Background(), tc)
	var freshPending int
	e.St.DB.QueryRow(`SELECT count(*) FROM approvals WHERE run_id='g2' AND action LIKE 'git push%' AND status='pending'`).Scan(&freshPending)
	if freshPending != 1 {
		t.Errorf("fresh pending after expiry: %d", freshPending)
	}
}

// Band-demote proposals are human-paced review items — the tool-call gate
// TTL must never expire them (an expired proposal would be lost forever
// while the low-grade flag blocks re-proposal).
func TestExpirySparesBandProposals(t *testing.T) {
	e := testEngine(t)
	e.Cfg.ApprovalTTLMinutes = 30
	seedRun(t, e, "g3", 0)
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	e.St.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('g3','git push','gate', ?)`, old)
	e.St.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('g3','band-demote:a1:supervised','closed-loop', ?)`, old)

	e.ExpireStale()

	var gateStatus, proposalStatus string
	e.St.DB.QueryRow(`SELECT status FROM approvals WHERE action = 'git push'`).Scan(&gateStatus)
	e.St.DB.QueryRow(`SELECT status FROM approvals WHERE action LIKE 'band-demote:%'`).Scan(&proposalStatus)
	if gateStatus != "expired" || proposalStatus != "pending" {
		t.Errorf("gate=%s proposal=%s, want expired/pending", gateStatus, proposalStatus)
	}
}

func TestComplianceExportBundle(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "c1", 1.5)
	a := &Audit{St: e.St, Actor: "tester"}
	a.Append("act", "subj", "detail")
	e.St.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('c1','git push','gate', ?)`, store.Now())
	e.St.DB.Exec(`INSERT INTO flags(run_id, reason, created_at) VALUES('c1','test', ?)`, store.Now())

	b, err := BuildComplianceBundle(e.St, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Verify.OK {
		t.Error("fresh chain must verify")
	}
	// The export appended its own audit entry — bundle has the pre-export one.
	if len(b.AuditLog) < 1 || len(b.Approvals) != 1 || len(b.Flags) != 1 || len(b.RunsSummary) != 1 {
		t.Errorf("bundle sizes: audit=%d approvals=%d flags=%d runs=%d",
			len(b.AuditLog), len(b.Approvals), len(b.Flags), len(b.RunsSummary))
	}
	var exportAudits int
	e.St.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='compliance_export'`).Scan(&exportAudits)
	if exportAudits != 1 {
		t.Errorf("export audit: %d", exportAudits)
	}
	// Serialization sanity.
	var jsonBuf, csvBuf testWriter
	if err := b.WriteJSON(&jsonBuf); err != nil || len(jsonBuf) == 0 {
		t.Errorf("json: %v", err)
	}
	if err := b.WriteCSV(&csvBuf); err != nil || len(csvBuf) == 0 {
		t.Errorf("csv: %v", err)
	}
}

// TestFindOrCreateApprovalConcurrentDedup proves the partial unique index
// (migration 019) closes the SELECT-then-INSERT TOCTOU: many goroutines
// racing findOrCreateApproval for the same (run_id, action) must settle on
// exactly one pending approval row, not one per racing caller.
func TestFindOrCreateApprovalConcurrentDedup(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "race1", 0)

	const n = 20
	ids := make([]int64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = e.findOrCreateApproval("race1", "deploy prod", "concurrent gate")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	first := ids[0]
	for i, id := range ids {
		if id != first {
			t.Errorf("goroutine %d got approval id %d, want %d (all callers must share one row)", i, id, first)
		}
	}
	var n2 int
	e.St.DB.QueryRow(`SELECT count(*) FROM approvals WHERE run_id='race1' AND action='deploy prod' AND status='pending'`).Scan(&n2)
	if n2 != 1 {
		t.Errorf("pending approvals for (race1, deploy prod): %d, want 1", n2)
	}
}

type testWriter []byte

func (w *testWriter) Write(p []byte) (int, error) {
	*w = append(*w, p...)
	return len(p), nil
}
