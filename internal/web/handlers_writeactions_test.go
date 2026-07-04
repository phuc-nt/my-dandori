package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store/testseed"
)

// --- UC2: Jira transition request -------------------------------------

func TestTransitionRequestMissingTaskKeyIs400(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 0) // no task_key
	rec := postForm(t, s, "/runs/r1/transition-request", url.Values{"transition_name": {"Done"}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no task_key → %d, want 400", rec.Code)
	}
}

func TestTransitionRequestMissingNameIs400(t *testing.T) {
	s := testServer(t)
	seedRunWithTask(t, s, "r1", "SCRUM-1")
	rec := postForm(t, s, "/runs/r1/transition-request", url.Values{"transition_name": {" "}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("blank transition_name → %d, want 400", rec.Code)
	}
}

func TestTransitionRequestCreatesPendingOperatorApproval(t *testing.T) {
	s := testServer(t)
	seedRunWithTask(t, s, "r1", "SCRUM-1")
	rec := postForm(t, s, "/runs/r1/transition-request", url.Values{"transition_name": {"Done"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("transition-request → %d body=%s", rec.Code, rec.Body)
	}
	assertPendingOperatorApproval(t, s, "jira-transition", `"transition_name":"Done"`)
}

// --- UC4: PR review request ---------------------------------------------

func TestPRReviewRequestInvalidInputIs400(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 0)
	cases := []url.Values{
		{"repo": {""}, "num": {"1"}, "decision": {"approve"}},
		{"repo": {"x/y"}, "num": {"0"}, "decision": {"approve"}},
		{"repo": {"x/y"}, "num": {"1"}, "decision": {"bogus"}},
		{"repo": {"x/y"}, "num": {"notanumber"}, "decision": {"approve"}},
	}
	for _, form := range cases {
		if rec := postForm(t, s, "/runs/r1/pr-review-request", form); rec.Code != http.StatusBadRequest {
			t.Errorf("form=%v → %d, want 400", form, rec.Code)
		}
	}
}

// A valid request must still fail cleanly (502) when there is no working `gh`
// on PATH to pin the live head SHA — it must never fabricate a pin.
func TestPRReviewRequestNoGhIsBadGateway(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 0)
	t.Setenv("PATH", t.TempDir()) // no `gh` resolvable
	rec := postForm(t, s, "/runs/r1/pr-review-request", url.Values{
		"repo": {"x/y"}, "num": {"1"}, "decision": {"approve"},
	})
	if rec.Code != http.StatusBadGateway {
		t.Errorf("no gh on PATH → %d, want 502", rec.Code)
	}
}

// --- UC9: calendar event request ----------------------------------------

func TestCalendarRequestMissingFieldsIs400(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 0)
	if rec := postForm(t, s, "/runs/r1/calendar-request", url.Values{"title": {""}}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty title/start/end → %d, want 400", rec.Code)
	}
}

func TestCalendarRequestInvalidTimezoneIs400(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 0)
	rec := postForm(t, s, "/runs/r1/calendar-request", url.Values{
		"title": {"Review"}, "start": {"2026-08-01T10:00"}, "end": {"2026-08-01T11:00"},
		"tz": {"Not/AZone"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad tz → %d, want 400", rec.Code)
	}
}

func TestCalendarRequestInvalidAttendeeEmailIs400(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 0)
	rec := postForm(t, s, "/runs/r1/calendar-request", url.Values{
		"title": {"Review"}, "start": {"2026-08-01T10:00"}, "end": {"2026-08-01T11:00"},
		"attendees": {"not-an-email"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid attendee → %d, want 400", rec.Code)
	}
}

func TestCalendarRequestTooManyAttendeesIs400(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 0)
	var many string
	for i := 0; i < maxCalendarAttendees+1; i++ {
		if i > 0 {
			many += ","
		}
		many += "a@x.com"
	}
	rec := postForm(t, s, "/runs/r1/calendar-request", url.Values{
		"title": {"Review"}, "start": {"2026-08-01T10:00"}, "end": {"2026-08-01T11:00"},
		"attendees": {many},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("too many attendees → %d, want 400", rec.Code)
	}
}

// Happy path: the datetime-local values (no offset) must round-trip through
// parseLocalDateTime into RFC3339 in the pinned evidence — the exact bug this
// phase caught by tracing the request/apply format contract end-to-end.
func TestCalendarRequestPinsRFC3339Evidence(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 0, 0)
	rec := postForm(t, s, "/runs/r1/calendar-request", url.Values{
		"title": {"Review r1"}, "start": {"2026-08-01T10:00"}, "end": {"2026-08-01T11:00"},
		"tz": {"Asia/Ho_Chi_Minh"}, "attendees": {"a@x.com, b@x.com"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("calendar-request → %d body=%s", rec.Code, rec.Body)
	}
	assertPendingOperatorApproval(t, s, "calendar-event", `"start":"2026-08-01T10:00:00+07:00"`)
}

// --- UB4: gate override request ------------------------------------------

func TestOverrideGateRequestMissingReasonIs400(t *testing.T) {
	s := testServer(t)
	seedFailingGateResult(t, s, "r1")
	rec := postForm(t, s, "/runs/r1/override-gate", url.Values{"check_name": {"exit 1"}, "reason": {" "}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty reason → %d, want 400", rec.Code)
	}
}

func TestOverrideGateRequestMissingCheckNameIs400(t *testing.T) {
	s := testServer(t)
	seedFailingGateResult(t, s, "r1")
	rec := postForm(t, s, "/runs/r1/override-gate", url.Values{"check_name": {""}, "reason": {"flaky"}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty check_name → %d, want 400", rec.Code)
	}
}

func TestOverrideGateRequestSucceedsAndAudits(t *testing.T) {
	s := testServer(t)
	seedFailingGateResult(t, s, "r1")
	rec := postForm(t, s, "/runs/r1/override-gate", url.Values{"check_name": {"exit 1"}, "reason": {"known flaky"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("override-gate → %d body=%s", rec.Code, rec.Body)
	}
	var overridden int
	s.Store.DB.QueryRow(`SELECT count(*) FROM gate_results WHERE run_id='r1' AND overridden_at IS NOT NULL`).Scan(&overridden)
	if overridden != 1 {
		t.Errorf("overridden rows = %d, want 1", overridden)
	}
	var audits int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='gate_overridden'`).Scan(&audits)
	if audits != 1 {
		t.Errorf("gate_overridden audits = %d, want 1", audits)
	}
}

// --- test seed helpers ----------------------------------------------------

func seedRunWithTask(t *testing.T, s *Server, runID, taskKey string) {
	t.Helper()
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, runID, "a1", "done", 0, 0)
	if _, err := s.Store.DB.Exec(`UPDATE runs SET task_key = ? WHERE id = ?`, taskKey, runID); err != nil {
		t.Fatal(err)
	}
}

func seedFailingGateResult(t *testing.T, s *Server, runID string) {
	t.Helper()
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, runID, "a1", "done", 0, 0)
	if _, err := s.Store.DB.Exec(`INSERT INTO gate_results(run_id, check_name, ok, output, ts)
		VALUES(?, 'exit 1', 0, 'boom', ?)`, runID, "2026-07-04T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}

// assertPendingOperatorApproval confirms a RequestAction call for the given
// type produced a real pending approval (H1: the request path never mutates
// state directly) whose insight is surfaced on the operator surface (so it
// renders in the existing /reviews inbox) and whose evidence contains want.
func assertPendingOperatorApproval(t *testing.T, s *Server, typ, want string) {
	t.Helper()
	var status, evidence, surface string
	err := s.Store.DB.QueryRow(`SELECT a.status, i.evidence, i.surface
		FROM approvals a JOIN insights i ON i.approval_id = a.id
		WHERE a.action LIKE ? ORDER BY a.id DESC LIMIT 1`, "observer:"+typ+":%").
		Scan(&status, &evidence, &surface)
	if err != nil {
		t.Fatalf("no pending approval for type %q: %v", typ, err)
	}
	if status != "pending" {
		t.Errorf("approval status = %q, want pending (request must never auto-apply)", status)
	}
	if surface != "operator" {
		t.Errorf("insight surface = %q, want operator", surface)
	}
	if !strings.Contains(evidence, want) {
		t.Errorf("evidence = %s, want substring %q", evidence, want)
	}
}
