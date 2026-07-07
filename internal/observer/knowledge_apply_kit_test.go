package observer

import (
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/learn"
)

// TestApplyKnowledgePublishKitHappyPath proves a clean kit (no tamper)
// publishes: state → published, audit records the manifest content_hash
// under subject kit:<name> (H1 — same independent-hash-source pattern as
// skill; the C1 ApproveHash fix that reads subject via u.Kind is P5's, but
// the audit WRITE side must already agree on kit:<name> here).
func TestApplyKnowledgePublishKitHappyPath(t *testing.T) {
	st, _ := testStore(t)
	files := []learn.KitFileInput{
		{Path: "agents/reviewer.md", Body: "review agent body"},
		{Path: "rules/dev.md", Body: "dev rules body"},
	}
	id, err := learn.NominateUnitTx(st, learn.KitNominateParams{
		Name: "happy-kit", Title: "happy-kit", Files: files, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(st, id, "tester"); err != nil {
		t.Fatal(err)
	}
	u, _ := learn.GetUnit(st, id)

	requestAndApprove(t, st, "knowledge-publish", "kit:happy-kit", "s", map[string]any{
		"unit_id": id, "kind": "kit", "name": "happy-kit",
		"body": u.Body, "content_hash": u.ContentHash,
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	got, _ := learn.GetUnit(st, id)
	if got.State != learn.StatePublished {
		t.Errorf("state=%q, want published", got.State)
	}
	var detail, subject string
	st.DB.QueryRow(`SELECT subject, detail FROM audit_log WHERE action='knowledge_published' ORDER BY id DESC LIMIT 1`).Scan(&subject, &detail)
	if subject != "kit:happy-kit" {
		t.Errorf("audit subject=%q, want kit:happy-kit", subject)
	}
	if !strings.Contains(detail, u.ContentHash) {
		t.Errorf("audit detail=%q, want manifest content_hash %q (H1)", detail, u.ContentHash)
	}
	if !strings.Contains(detail, "kit ") {
		t.Errorf("audit detail=%q, want kit-specific wording", detail)
	}
}

// TestApplyKnowledgePublishKitTamperedFileRowPermanentFail is the REQUIRED
// H1 security negative: a knowledge_kit_files row edited AFTER RequestPublish
// pinned the manifest (evidence frozen at request time) must fail the
// applier permanently — sha256(row.body) no longer agrees with the pinned
// manifest's content_hash for that path. No publish, no partial state
// change, and the failure must be classified errPermanentApply (never
// retried) matching every other tamper-after-approval scenario in this
// package (H1/H3 lesson from v12, enforced not just documented).
func TestApplyKnowledgePublishKitTamperedFileRowPermanentFail(t *testing.T) {
	st, _ := testStore(t)
	files := []learn.KitFileInput{
		{Path: "agents/reviewer.md", Body: "original safe body"},
		{Path: "rules/dev.md", Body: "dev rules body"},
	}
	id, err := learn.NominateUnitTx(st, learn.KitNominateParams{
		Name: "tampered-kit", Title: "tampered-kit", Files: files, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(st, id, "tester"); err != nil {
		t.Fatal(err)
	}
	u, _ := learn.GetUnit(st, id)

	// Pin the manifest as it stood at RequestPublish time (this is exactly
	// what learn's actionParams does via u.Body/u.ContentHash).
	requestAndApprove(t, st, "knowledge-publish", "kit:tampered-kit", "s", map[string]any{
		"unit_id": id, "kind": "kit", "name": "tampered-kit",
		"body": u.Body, "content_hash": u.ContentHash,
	})

	// Tamper ONE file row's body AFTER the manifest was pinned — even though
	// the row's own content_hash column could also be rewritten to "agree"
	// with the new body, the manifest embedded in the PINNED evidence still
	// carries the OLD hash for this path, so the applier's re-derive-and-
	// compare must catch it regardless of what the row's own column says.
	if _, err := st.DB.Exec(`UPDATE knowledge_kit_files SET body = ? WHERE unit_id = ? AND path = ?`,
		"evil instructions injected post-approval", id, "agents/reviewer.md"); err != nil {
		t.Fatal(err)
	}

	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 — tampered file row must permanently fail (H1)", n)
	}
	got, _ := learn.GetUnit(st, id)
	if got.State != learn.StateInReview {
		t.Errorf("state=%q, want unchanged in_review after permanent apply failure", got.State)
	}

	var failed int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='observer_apply_failed'`).Scan(&failed)
	if failed != 1 {
		t.Errorf("observer_apply_failed audits=%d, want 1 for the tampered-row permanent failure", failed)
	}
	var published int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='knowledge_published' AND subject='kit:tampered-kit'`).Scan(&published)
	if published != 0 {
		t.Errorf("knowledge_published audits=%d, want 0 — tampered kit must never publish", published)
	}
}

// TestApplyKnowledgePublishKitMissingFileRowPermanentFail: a file present in
// the pinned manifest but deleted from knowledge_kit_files (not just edited)
// must also fail permanently — the applier's manifest-vs-rows walk must
// catch a missing row, not just a content mismatch.
func TestApplyKnowledgePublishKitMissingFileRowPermanentFail(t *testing.T) {
	st, _ := testStore(t)
	files := []learn.KitFileInput{
		{Path: "agents/reviewer.md", Body: "body a"},
		{Path: "rules/dev.md", Body: "body b"},
	}
	id, err := learn.NominateUnitTx(st, learn.KitNominateParams{
		Name: "missing-file-kit", Title: "t", Files: files, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(st, id, "tester"); err != nil {
		t.Fatal(err)
	}
	u, _ := learn.GetUnit(st, id)
	requestAndApprove(t, st, "knowledge-publish", "kit:missing-file-kit", "s", map[string]any{
		"unit_id": id, "kind": "kit", "name": "missing-file-kit",
		"body": u.Body, "content_hash": u.ContentHash,
	})

	if _, err := st.DB.Exec(`DELETE FROM knowledge_kit_files WHERE unit_id = ? AND path = ?`, id, "rules/dev.md"); err != nil {
		t.Fatal(err)
	}

	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 — missing file row must permanently fail", n)
	}
	got, _ := learn.GetUnit(st, id)
	if got.State != learn.StateInReview {
		t.Errorf("state=%q, want unchanged in_review", got.State)
	}
}
