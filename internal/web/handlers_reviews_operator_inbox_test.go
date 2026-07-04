package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/observer"
)

// H1 (phase-02 Step 0 prerequisite): every operator-surface write action
// (UC2/UC4/UC9) is proposed via observer.RequestAction(..., surface:
// "operator") exactly like the pre-existing budget/kill/band observer
// actions. This test proves the existing /reviews page and its decide route
// already serve as the operator inbox those actions need — no separate
// inbox was built, because none is required: queryApprovals (viewdata.go)
// reads the approvals table directly with no surface filter, so ANY
// observer-namespaced pending approval (operator or otherwise) already
// renders there, and handleReviewDecide already calls RunObserverApplier on
// approve (handlers_reviews.go). A "budget" action is used here (not
// jira/pr/calendar) because it needs no external client fake to prove the
// generic propose → surface → approve → consume contract end-to-end.
func TestReviewsPageIsTheOperatorInbox(t *testing.T) {
	s := testServer(t)

	approvalID, err := observer.RequestAction(s.Store, "budget", "agent:a1",
		"Đề xuất tăng ngân sách agent a1 lên $80 (chờ duyệt).",
		map[string]any{"scope_type": "agent", "scope_id": "a1", "suggested_limit": 80.0},
		"tester@console", "operator")
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}

	// Must render as a pending approval in the ordinary (non-HTMX) page —
	// this is the "tech mode" surface the operator actually browses.
	rec := get(t, s, "/reviews")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /reviews → %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Đề xuất tăng ngân sách") {
		t.Errorf("pending operator-surface request not rendered on /reviews: %s", rec.Body.String())
	}

	// Approve via the existing generic decide route — must apply (not just
	// mark approved) because handleReviewDecide runs RunObserverApplier
	// synchronously on approve.
	decideRec := postForm(t, s, "/reviews/"+strconv.FormatInt(approvalID, 10)+"/decide",
		url.Values{"decision": {"approve"}, "note": {"ok"}})
	if decideRec.Code != http.StatusSeeOther {
		t.Fatalf("decide → %d body=%s", decideRec.Code, decideRec.Body)
	}

	var status string
	var consumedAt *string
	s.Store.DB.QueryRow(`SELECT status, consumed_at FROM approvals WHERE id = ?`, approvalID).Scan(&status, &consumedAt)
	if status != "approved" {
		t.Errorf("approval status = %q, want approved", status)
	}
	if consumedAt == nil || *consumedAt == "" {
		t.Error("approval was not consumed — RunObserverApplier must run synchronously on approve")
	}
	var limit float64
	if err := s.Store.DB.QueryRow(`SELECT limit_usd FROM budgets WHERE scope_type='agent' AND scope_id='a1'`).Scan(&limit); err != nil {
		t.Fatalf("budget was not applied: %v", err)
	}
	if limit != 80 {
		t.Errorf("limit_usd = %v, want 80 (the exact value pinned at request time)", limit)
	}
}
