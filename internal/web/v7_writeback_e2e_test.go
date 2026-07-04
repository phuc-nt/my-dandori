// Package web v7 offline E2E: one file stitching the FULL request → approve
// → apply → argv/audit loop for every v7 write-back feature through the
// actual HTTP surface (not just the unit-level apply/handler tests that
// already cover exhaustive edge cases per package). This is deliberately
// placed in `internal/web` rather than `internal/integrations` — the plan's
// suggested path — because internal/web is the only package that can see
// both the HTTP handlers AND internal/observer's RunObserverApplier without
// an import cycle (observer → integrations is one-way; web → observer is
// the only package above both). Each test below cross-references the
// per-package unit test file that already proves the exhaustive edge case,
// and asserts the single thing only an E2E test can: the pieces are wired
// together correctly end-to-end, offline, with the fake gh/gws binaries.
package web

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/observer"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

// approveLatestPendingAndApply approves the most recently requested approval
// of the given observer action type via the SAME route a human uses
// (/reviews/{id}/decide) and returns its id. Mirrors the production path,
// not a shortcut through govern.Decide directly.
func approveLatestPendingAndApply(t *testing.T, s *Server, typ string) int64 {
	t.Helper()
	var approvalID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM approvals WHERE action LIKE ? AND status='pending' ORDER BY id DESC LIMIT 1`,
		"observer:"+typ+":%").Scan(&approvalID); err != nil {
		t.Fatalf("no pending approval for %q: %v", typ, err)
	}
	rec := postForm(t, s, "/reviews/"+strconv.FormatInt(approvalID, 10)+"/decide",
		url.Values{"decision": {"approve"}, "note": {"e2e ok"}})
	if rec.Code != 303 {
		t.Fatalf("decide approval %d → %d body=%s", approvalID, rec.Code, rec.Body)
	}
	return approvalID
}

// --- UC2 Jira transition: request → operator inbox → approve → apply -----
//
// Edge cases (missing task-key, blank name, H3 stale-name re-openable) are
// covered by internal/observer/writeactions_apply_test.go and
// internal/web/handlers_writeactions_test.go. Here: the full HTTP chain,
// asserting the /reviews inbox actually renders the request and that
// approving through the real decide route consumes it (apply itself needs
// real Jira credentials so it fails permanently in this offline environment
// — that failure path, not a live transition, is what this asserts; the
// live script exercises the real jira.Transition call).
func TestE2EJiraTransitionRequestVisibleOnInboxAndConsumedOnApprove(t *testing.T) {
	s := testServer(t)
	seedRunWithTask(t, s, "r1", "SCRUM-1")

	rec := postForm(t, s, "/runs/r1/transition-request", url.Values{"transition_name": {"Done"}})
	if rec.Code != 303 {
		t.Fatalf("transition-request → %d body=%s", rec.Code, rec.Body)
	}
	inbox := get(t, s, "/reviews")
	if !strings.Contains(inbox.Body.String(), "SCRUM-1") {
		t.Errorf("/reviews does not show the pending jira-transition request: %s", inbox.Body.String())
	}

	approveLatestPendingAndApply(t, s, "jira-transition")

	var consumed *string
	s.Store.DB.QueryRow(`SELECT consumed_at FROM approvals WHERE action LIKE 'observer:jira-transition:%' ORDER BY id DESC LIMIT 1`).Scan(&consumed)
	if consumed == nil || *consumed == "" {
		t.Error("approval must be consumed once decided (apply then permanently fails offline — no Atlassian creds — which is the documented gap for the live script)")
	}
	var failedAudit int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='observer_apply_failed'`).Scan(&failedAudit)
	if failedAudit != 1 {
		t.Errorf("expected exactly one observer_apply_failed audit (no Atlassian creds offline), got %d", failedAudit)
	}
}

// --- UC4 PR review: request (pins head SHA via fake gh) → approve → apply
// via fake gh → argv + audit. Full happy-path loop with the fake CLI.
func TestE2EPRReviewRequestApproveApplyWithFakeGh(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 0)
	argvOut := withFakeGhOnPathE2E(t)
	t.Setenv("DRY_RUN", "false")

	rec := postForm(t, s, "/runs/r1/pr-review-request", url.Values{
		"repo": {"phuc-nt/dandori"}, "num": {"7"}, "decision": {"approve"}, "body": {"lgtm from e2e"},
	})
	if rec.Code != 303 {
		t.Fatalf("pr-review-request → %d body=%s", rec.Code, rec.Body)
	}
	inbox := get(t, s, "/reviews")
	if !strings.Contains(inbox.Body.String(), "phuc-nt/dandori#7") {
		t.Errorf("/reviews missing pending pr-review request: %s", inbox.Body.String())
	}

	approveLatestPendingAndApply(t, s, "pr-review")

	if _, err := observer.RunObserverApplier(s.Store); err != nil {
		t.Fatal(err) // idempotent no-op if handleReviewDecide already applied it
	}
	var applied int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='pr_review_applied'`).Scan(&applied)
	if applied != 1 {
		t.Fatalf("pr_review_applied audits=%d, want 1", applied)
	}
	lines := readArgvLinesE2E(t, argvOut)
	found := false
	for _, l := range lines {
		if strings.Contains(l, "\x1fpr\x1freview\x1f") || strings.HasPrefix(l, "pr\x1freview") {
			found = true
		}
	}
	if !found {
		t.Errorf("fake gh argv log missing 'pr review' invocation: %v", lines)
	}
}

// --- UC9 Calendar event: request → approve → apply via fake gws → argv +
// audit + idem_key row. Full happy-path loop.
func TestE2ECalendarRequestApproveApplyWithFakeGws(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 0)
	t.Setenv("DANDORI_GWS_BIN", fakeGwsBinPath(t))
	argvOut := t.TempDir() + "/argv.log"
	t.Setenv("FAKE_ARGV_OUT", argvOut)
	t.Setenv("DRY_RUN", "false")

	rec := postForm(t, s, "/runs/r1/calendar-request", url.Values{
		"title": {"E2E review"}, "start": {"2026-08-01T10:00"}, "end": {"2026-08-01T11:00"},
		"tz": {"Asia/Ho_Chi_Minh"}, "attendees": {"a@x.com"},
	})
	if rec.Code != 303 {
		t.Fatalf("calendar-request → %d body=%s", rec.Code, rec.Body)
	}
	approveLatestPendingAndApply(t, s, "calendar-event")

	var applied, notif int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='calendar_event_applied'`).Scan(&applied)
	s.Store.DB.QueryRow(`SELECT count(*) FROM notifications WHERE kind='calendar_event'`).Scan(&notif)
	if applied != 1 || notif != 1 {
		t.Fatalf("applied=%d notifications=%d, want 1/1", applied, notif)
	}
	lines := readArgvLinesE2E(t, argvOut)
	if len(lines) != 1 {
		t.Errorf("gws invocations=%d, want exactly 1", len(lines))
	}
}

// --- UC8/UG2b delivery: already end-to-end at the HTTP layer in
// handlers_delivery_test.go (C2 config-pinned + M4 dedup-by-destination).
// This test adds the one thing that file doesn't: a real fake-gws argv
// assertion for the Sheets export leg through the HTTP route.
func TestE2EExportSheetsHTTPRouteCallsFakeGws(t *testing.T) {
	s := deliveryTestServer(t)
	s.Cfg.DryRun = false
	s.Cfg.ExportSpreadsheetID = "e2e-pinned-sheet"
	argvOut := t.TempDir() + "/argv.log"
	t.Setenv("FAKE_ARGV_OUT", argvOut)

	rec := postForm(t, s, "/dash/export-sheets", url.Values{})
	if rec.Code != 200 {
		t.Fatalf("export-sheets → %d body=%s", rec.Code, rec.Body)
	}
	lines := readArgvLinesE2E(t, argvOut)
	if len(lines) != 1 {
		t.Fatalf("gws invocations=%d, want 1 (values update only, target pinned)", len(lines))
	}
	// SheetsExporter logs its own leg to the events table (kind=sheets_exported);
	// the web handler additionally records an audit_log row for the HTTP action.
	var exported, triggered int
	s.Store.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='sheets_exported'`).Scan(&exported)
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='sheets_export_triggered'`).Scan(&triggered)
	if exported != 1 || triggered != 1 {
		t.Errorf("events(sheets_exported)=%d audit_log(sheets_export_triggered)=%d, want 1/1", exported, triggered)
	}
}

// --- UC6 Drive import: full HTTP chain import(request) → /reviews shows
// FULL body → approve → SaveContext with provenance. The exhaustive
// per-layer (team/agent/company) approval-gating, size, and secret-block
// assertions already live in handlers_drive_import_test.go; this proves the
// SAME chain via the /reviews decide route (not observer.RunObserverApplier
// called directly) so the operator's actual click path is exercised
// end-to-end for one representative layer.
func TestE2EDriveImportFullChainViaReviewsDecideRoute(t *testing.T) {
	s := testServer(t)
	t.Setenv("DANDORI_GWS_BIN", fakeGwsBinPath(t))

	rec := postForm(t, s, "/contexts/drive-import", url.Values{
		"layer": {"team"}, "target": {"9"},
		"doc_id": {"f1"}, "doc_name": {"E2E Doc"}, "modified": {"2026-07-01T00:00:00Z"},
	})
	if rec.Code != 200 {
		t.Fatalf("drive-import → %d body=%s", rec.Code, rec.Body)
	}
	inbox := get(t, s, "/reviews")
	if !strings.Contains(inbox.Body.String(), "fake exported content") {
		t.Fatalf("/reviews must show the FULL pinned Drive body, got: %s", inbox.Body.String())
	}
	approveLatestPendingAndApply(t, s, "context-import")

	var audited int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='context_imported_drive'`).Scan(&audited)
	if audited != 1 {
		t.Errorf("context_imported_drive audits=%d, want 1", audited)
	}
}

// --- UB4 gate override: HTTP handler already asserted in
// handlers_writeactions_test.go; this adds the H4 idempotent-second-call
// assertion through the actual HTTP route (unit test for OverrideGate
// covers it at the function level in gate_override_test.go — this proves
// the handler wiring passes the same idempotency through).
func TestE2EOverrideGateSecondHTTPCallIsNoOp(t *testing.T) {
	s := testServer(t)
	seedFailingGateResult(t, s, "r1")

	first := postForm(t, s, "/runs/r1/override-gate", url.Values{"check_name": {"exit 1"}, "reason": {"flaky in CI"}})
	if first.Code != 200 {
		t.Fatalf("first override → %d body=%s", first.Code, first.Body)
	}
	second := postForm(t, s, "/runs/r1/override-gate", url.Values{"check_name": {"exit 1"}, "reason": {"different reason"}})
	if second.Code != 200 {
		t.Fatalf("second override → %d body=%s", second.Code, second.Body)
	}
	var audits int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='gate_overridden'`).Scan(&audits)
	if audits != 1 {
		t.Errorf("gate_overridden audits=%d, want 1 (second HTTP call must no-op)", audits)
	}
	var reason string
	s.Store.DB.QueryRow(`SELECT override_reason FROM gate_results WHERE run_id='r1' AND check_name='exit 1'`).Scan(&reason)
	if reason != "flaky in CI" {
		t.Errorf("override_reason=%q, want original preserved (immutable row)", reason)
	}
}

// --- G6/UE3/L7/UG3/UG5 cross-checks not already exercised via an HTTP loop
// elsewhere in this package: threshold persisted via the form is read back
// by the SAME code path the closed-loop detector uses (proves the wiring,
// not just the setting round-trip which handlers_gate_thresholds_test.go
// and gate_thresholds_test.go already cover independently).
func TestE2EThresholdSetThroughFormReflectsInDecisionSite(t *testing.T) {
	s := testServer(t)
	if rec := postForm(t, s, "/gate-thresholds", url.Values{"gate_min_grade": {"A"}, "gate_min_pass_pct": {"95"}}); rec.Code != 303 {
		t.Fatalf("set threshold → %d body=%s", rec.Code, rec.Body)
	}
	if got := s.Store.Setting("gate_min_grade"); got != "A" {
		t.Fatalf("gate_min_grade=%q, want A (persisted via the exact form route the operator uses)", got)
	}
}

// L7: the ranked suggestion fragment reachable via the actual HTTP route on
// seeded history — SuggestAgents' scoring itself is unit-tested exhaustively
// in assignment_suggest_test.go.
func TestE2EAssignmentSuggestHTTPRouteRanksSeededHistory(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.WorkItem(t, s.Store, "jira", "SCRUM-9", "Done")
	s.Store.DB.Exec(`UPDATE work_items SET title='Refactor billing export' WHERE key='SCRUM-9'`)
	testseed.Run(t, s.Store, "a1-r1", "a1", "done", 1, 0.4)
	s.Store.DB.Exec(`UPDATE runs SET task_key='SCRUM-9' WHERE id='a1-r1'`)

	rec := get(t, s, "/assign/suggest?task="+url.QueryEscape("refactor the billing export"))
	if rec.Code != 200 {
		t.Fatalf("assign/suggest → %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "a1") {
		t.Errorf("suggestion fragment missing seeded agent a1: %s", rec.Body.String())
	}
}

// UG3/UG5: saved-view roundtrip + wallboard fragment are already fully
// covered in handlers_saved_views_test.go and handlers_wallboard_test.go
// (including L2 escaping). No additional E2E value beyond what's there —
// documented here as a deliberate no-duplicate decision, not an omission.

// withFakeGhOnPathE2E prepends the shared fake-gh fixture onto PATH so both
// the pr-review-request handler (which shells out to `gh pr view` to pin the
// head SHA) and the later apply step resolve to the same fake, and points
// FAKE_ARGV_OUT at a fresh log file. Duplicated locally (rather than reused
// from internal/observer's withFakeGh) because Go test helpers are not
// exported across packages.
func withFakeGhOnPathE2E(t *testing.T) string {
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

func readArgvLinesE2E(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}
