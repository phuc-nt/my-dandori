package observer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/store"
)

// withFakeGh prepends a fake `gh` CLI (shared fixture under
// internal/integrations/testdata) onto PATH so exec.Command("gh", ...)
// resolves to it — used by both PRCurrentState (this package) and
// ghub.PRReview (called from applyPRReview) so a single fake drives the
// whole PR-review apply path end-to-end.
func withFakeGh(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs("../integrations/testdata")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	if err := os.Symlink(filepath.Join(repoRoot, "fake-gh"), filepath.Join(binDir, "gh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argvOut := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("FAKE_ARGV_OUT", argvOut)
	return argvOut
}

// withFakeGws points gws.NewRunner at the shared fake-gws fixture.
func withFakeGws(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs("../integrations/testdata")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DANDORI_GWS_BIN", filepath.Join(repoRoot, "fake-gws"))
	argvOut := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("FAKE_ARGV_OUT", argvOut)
	return argvOut
}

func readArgvLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func requestAndApprove(t *testing.T, st *store.Store, typ, subject, summary string, params map[string]any) int64 {
	t.Helper()
	if _, err := RequestAction(st, typ, subject, summary, params, "tester@console", "operator"); err != nil {
		t.Fatal(err)
	}
	var approvalID int64
	// MAX(id): a test may call this helper more than once for the same
	// action type (e.g. idempotent-retry tests) — always decide the row
	// just created, never an earlier already-consumed one.
	if err := st.DB.QueryRow(`SELECT id FROM approvals WHERE action LIKE ? ORDER BY id DESC LIMIT 1`, "observer:"+typ+":%").
		Scan(&approvalID); err != nil {
		t.Fatal(err)
	}
	if _, err := govern.Decide(st, approvalID, true, "tester@console", "ok"); err != nil {
		t.Fatal(err)
	}
	return approvalID
}

// --- jira-transition -------------------------------------------------

// Invalid/empty params never reach a client call and are permanently
// rejected — the approval is consumed, not retried forever.
func TestApplyJiraTransitionInvalidParams(t *testing.T) {
	st, _ := testStore(t)
	requestAndApprove(t, st, "jira-transition", "SCRUM-1", "s", map[string]any{"key": "", "transition_name": ""})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 for invalid params", n)
	}
	var consumed int
	st.DB.QueryRow(`SELECT count(*) FROM approvals WHERE consumed_at IS NOT NULL`).Scan(&consumed)
	if consumed != 1 {
		t.Errorf("invalid params must still consume (no infinite retry): consumed=%d", consumed)
	}
	var applied int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='jira_transition_applied'`).Scan(&applied)
	if applied != 0 {
		t.Error("invalid params must never reach an applied audit")
	}
}

// With no Atlassian credentials configured (the deterministic state of this
// sandboxed test environment — no ATLASSIAN_* env vars, .env not on the
// package's CWD), the apply case must fail permanently rather than panic or
// hang attempting a real network call.
func TestApplyJiraTransitionNoCredsIsPermanent(t *testing.T) {
	st, _ := testStore(t)
	requestAndApprove(t, st, "jira-transition", "SCRUM-1", "s",
		map[string]any{"key": "SCRUM-1", "transition_name": "Done"})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 without credentials", n)
	}
	var failed int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='observer_apply_failed'`).Scan(&failed)
	if failed != 1 {
		t.Errorf("expected one observer_apply_failed audit, got %d", failed)
	}
}

// --- pr-review (H3: head SHA / merged-closed re-validation) ----------

func TestApplyPRReviewInvalidParams(t *testing.T) {
	st, _ := testStore(t)
	requestAndApprove(t, st, "pr-review", "x/y#1", "s", map[string]any{
		"repo": "", "num": 0, "decision": "bogus", "head_sha": "",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 for invalid pr-review params", n)
	}
	var applied int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='pr_review_applied'`).Scan(&applied)
	if applied != 0 {
		t.Error("invalid params must never reach an applied audit")
	}
}

// Happy path: pinned head SHA still matches the (fake) PR's current head →
// applies via ghub.PRReview and audits pr_review_applied. DRY_RUN must be
// forced false — config.Load's real default is DryRun=true, which would
// otherwise make ghub.PRReview's internal Guard skip the call silently.
func TestApplyPRReviewHappyPath(t *testing.T) {
	st, _ := testStore(t)
	t.Setenv("DRY_RUN", "false")
	withFakeGh(t)
	const fakeHead = "fakehead0000000000000000000000000000000"
	requestAndApprove(t, st, "pr-review", "phuc-nt/dandori#7", "s", map[string]any{
		"repo": "phuc-nt/dandori", "num": 7, "decision": "approve", "body": "lgtm", "head_sha": fakeHead,
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	var applied int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='pr_review_applied'`).Scan(&applied)
	if applied != 1 {
		t.Errorf("pr_review_applied audits=%d", applied)
	}
}

// H3: the PR's head moved since the request (pinned SHA no longer current)
// → refused, audited as stale, and a fresh advisory insight is filed so the
// human can re-request. The spent approval itself is never retried.
func TestApplyPRReviewStaleHeadRefused(t *testing.T) {
	st, _ := testStore(t)
	withFakeGh(t)
	requestAndApprove(t, st, "pr-review", "phuc-nt/dandori#7", "s", map[string]any{
		"repo": "phuc-nt/dandori", "num": 7, "decision": "approve", "body": "", "head_sha": "deadbeef-stale",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 for stale head", n)
	}
	var stale, advisory int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='pr_review_apply_stale'`).Scan(&stale)
	st.DB.QueryRow(`SELECT count(*) FROM insights WHERE type='pr_review_stale'`).Scan(&advisory)
	if stale != 1 {
		t.Errorf("pr_review_apply_stale audits=%d, want 1", stale)
	}
	if advisory != 1 {
		t.Errorf("pr_review_stale advisory insights=%d, want 1 (must not silently lose the action)", advisory)
	}
	var applied int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='pr_review_applied'`).Scan(&applied)
	if applied != 0 {
		t.Error("stale target must never be applied")
	}
}

// --- calendar-event (H3: idem_key idempotency) ------------------------

func TestApplyCalendarEventInvalidParams(t *testing.T) {
	st, _ := testStore(t)
	requestAndApprove(t, st, "calendar-event", "run:r1", "s", map[string]any{
		"title": "", "start": "", "end": "", "idem_key": "",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 for invalid calendar params", n)
	}
	var applied int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='calendar_event_applied'`).Scan(&applied)
	if applied != 0 {
		t.Error("invalid params must never reach an applied audit")
	}
}

func TestApplyCalendarEventHappyPath(t *testing.T) {
	st, _ := testStore(t)
	t.Setenv("DRY_RUN", "false")
	argvOut := withFakeGws(t)
	requestAndApprove(t, st, "calendar-event", "run:r1", "s", map[string]any{
		"title": "Review r1", "start": "2026-08-01T10:00:00+07:00", "end": "2026-08-01T11:00:00+07:00",
		"tz": "Asia/Ho_Chi_Minh", "attendees": []string{"a@x.com"}, "send_updates": "none", "idem_key": "cal_abc123",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	var applied, notifRows int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='calendar_event_applied'`).Scan(&applied)
	st.DB.QueryRow(`SELECT count(*) FROM notifications WHERE dedup='cal_abc123'`).Scan(&notifRows)
	if applied != 1 || notifRows != 1 {
		t.Errorf("applied audits=%d notifications=%d, want 1/1", applied, notifRows)
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Errorf("gws invocations=%d, want exactly 1", len(lines))
	}
}

// H3: a transient retry of an already-applied idem_key must be a no-op —
// single notifications row, no second gws invocation, no second audit.
func TestApplyCalendarEventIdempotentRetry(t *testing.T) {
	st, _ := testStore(t)
	t.Setenv("DRY_RUN", "false")
	argvOut := withFakeGws(t)
	params := map[string]any{
		"title": "Review r1", "start": "2026-08-01T10:00:00+07:00", "end": "2026-08-01T11:00:00+07:00",
		"tz": "Asia/Ho_Chi_Minh", "attendees": []string{}, "send_updates": "none", "idem_key": "cal_retry_key",
	}
	requestAndApprove(t, st, "calendar-event", "run:r1", "s", params)
	if n, err := RunObserverApplier(st); err != nil || n != 1 {
		t.Fatalf("first apply: n=%d err=%v", n, err)
	}
	// Simulate a second, independent request that resolves to the SAME
	// idem_key (e.g. a retried form submit) — apply must no-op, not double-book.
	requestAndApprove(t, st, "calendar-event", "run:r1", "s", params)
	n2, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 1 {
		// applyInsightAction returns nil (success) on the dedup no-op path, so
		// RunObserverApplier still counts the second, distinct approval as
		// "applied" even though no second gws call happened — the load-bearing
		// assertions are the invariant checks below (single notification row,
		// single gws invocation), not this count.
		t.Errorf("second apply count=%d, want 1 (dedup no-op is still a successful apply)", n2)
	}
	var notifRows int
	st.DB.QueryRow(`SELECT count(*) FROM notifications WHERE dedup='cal_retry_key'`).Scan(&notifRows)
	if notifRows != 1 {
		t.Errorf("notifications rows=%d, want exactly 1 (idempotent)", notifRows)
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Errorf("gws invocations=%d, want exactly 1 (second apply must skip the client call)", len(lines))
	}
}

// --- context-import (C1: approval-gated Drive import for ALL layers) ----

// Approving a context-import must SaveContext with the pinned FULL content
// and a Drive provenance note, and record the dedicated audit action —
// mirrors applyContextWrite's discipline (H3 pinned bytes, defense-in-depth
// re-scan) for the new UC6 import path.
func TestApplyContextImportSavesWithProvenance(t *testing.T) {
	st, _ := testStore(t)
	requestAndApprove(t, st, "context-import", "team:9", "s", map[string]any{
		"layer": "team", "target": "9", "content": "Nội dung nhập từ Drive.",
		"doc_id": "doc-1", "doc_name": "Playbook.docx",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d, want 1", n)
	}
	hub := contexthub.New(st)
	head, err := hub.Head(contexthub.LayerTeam, "9")
	if err != nil {
		t.Fatal(err)
	}
	if head == nil || head.Content != "Nội dung nhập từ Drive." {
		t.Fatalf("head = %+v, want pinned FULL content saved", head)
	}
	if !strings.Contains(head.Note, "imported from Drive") || !strings.Contains(head.Note, "doc-1") {
		t.Errorf("note = %q, want Drive provenance with doc id", head.Note)
	}
	var audited int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='context_imported_drive'`).Scan(&audited)
	if audited != 1 {
		t.Errorf("context_imported_drive audits=%d, want 1", audited)
	}
}

// Empty pinned content is a permanent failure (never happens legitimately —
// the request handler always pins a non-empty body — but a malformed/edited
// approval row must not silently succeed or retry forever).
func TestApplyContextImportEmptyContentPermanent(t *testing.T) {
	st, _ := testStore(t)
	requestAndApprove(t, st, "context-import", "team:9", "s", map[string]any{
		"layer": "team", "target": "9", "content": "", "doc_id": "doc-1", "doc_name": "Empty",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 for empty content", n)
	}
	var failed int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='observer_apply_failed'`).Scan(&failed)
	if failed != 1 {
		t.Errorf("observer_apply_failed audits=%d, want 1", failed)
	}
}

// Defense in depth: even if a secret-shaped string somehow reached the
// pinned params (e.g. the pre-render scan missed a variant), the apply-time
// re-scan must refuse the write rather than let it reach SaveContext.
func TestApplyContextImportSecretRefused(t *testing.T) {
	st, _ := testStore(t)
	requestAndApprove(t, st, "context-import", "agent:bot", "s", map[string]any{
		"layer": "agent", "target": "bot", "content": "token: ghp_abcdef1234567890",
		"doc_id": "doc-2", "doc_name": "Leaky",
	})
	n, err := RunObserverApplier(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("applied=%d, want 0 for secret-bearing content", n)
	}
	hub := contexthub.New(st)
	head, _ := hub.Head(contexthub.LayerAgent, "bot")
	if head != nil {
		t.Errorf("secret content must never be saved: %+v", head)
	}
}

// C1: every layer (team, agent, company) imports through RequestAction only
// — zero rows appear in context_versions before the approval is decided,
// for every layer, not just company.
func TestContextImportNeverWritesBeforeApproval(t *testing.T) {
	st, _ := testStore(t)
	for _, layer := range []string{"team", "agent", "company"} {
		target := "x"
		if layer == "company" {
			target = "*"
		}
		if _, err := RequestAction(st, "context-import", layer+":"+target, "s",
			map[string]any{"layer": layer, "target": target, "content": "c", "doc_id": "d", "doc_name": "n"},
			"tester@console", "operator"); err != nil {
			t.Fatal(err)
		}
	}
	var versions int
	st.DB.QueryRow(`SELECT count(*) FROM context_versions`).Scan(&versions)
	if versions != 0 {
		t.Errorf("context_versions rows=%d before any approval, want 0 (C1: no direct SaveContext)", versions)
	}
	var pending int
	st.DB.QueryRow(`SELECT count(*) FROM approvals WHERE action LIKE 'observer:context-import:%' AND status='pending'`).Scan(&pending)
	if pending != 3 {
		t.Errorf("pending context-import approvals=%d, want 3 (one per layer)", pending)
	}
}

// ValidateEmail (M5) rejects display-name-wrapped and malformed addresses,
// not just missing '@'.
func TestValidateEmail(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"a@x.com", true},
		{"", false},
		{"not-an-email", false},
		{"Name <a@x.com>", false}, // must reject display-name wrapping
	}
	for _, c := range cases {
		if got := ValidateEmail(c.in); got != c.want {
			t.Errorf("ValidateEmail(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
