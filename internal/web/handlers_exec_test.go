package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func seedExecFixture(t *testing.T, s *Server) {
	t.Helper()
	st := s.Store
	st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('bot','bot','now')`)
	st.ResolveOperator("alice@mac")
	tid, _ := st.CreateTeam("Đội Thanh Toán")
	st.AssignMember(tid, "agent", "bot")
	st.AssignMember(tid, "operator", "alice@mac")
	for i := 0; i < 4; i++ {
		id := "r" + string(rune('0'+i))
		st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, operator_id, status, started_at, ended_at, cost_usd)
			VALUES(?, ?, 'bot', 'alice@mac', 'done', 'now','now', 1)`, id, id)
	}
	// A CEO-surface business insight (approval) and an operator-surface one.
	st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES(NULL, 'observer:budget:1', 'Đề xuất nâng ngân sách', 'now')`)
	st.DB.Exec(`INSERT INTO insights(type, subject, summary, evidence, class, surface, status, approval_id, created_at)
		VALUES('budget_overshoot_trend','global','Đề xuất nâng ngân sách lên $80.', '{}', 'approval','ceo','open', 1, 'now')`)
	st.DB.Exec(`INSERT INTO insights(type, subject, summary, evidence, class, surface, status, created_at)
		VALUES('rule_candidate','bot','Đề xuất thêm rule chặn regex nội bộ.', '{}', 'auto','operator','surfaced', 'now')`)
}

func TestExecHomeRendersVietnameseNoJargon(t *testing.T) {
	s := testServer(t)
	seedExecFixture(t, s)
	rec := get(t, s, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("exec home: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Bảng điều hành", "Giá trị AI mang lại", "Đội Thanh Toán", "Việc cần bạn"} {
		if !strings.Contains(body, want) {
			t.Errorf("exec home missing %q", want)
		}
	}
	// The operator-surface technical insight must NOT reach the CEO home.
	if strings.Contains(body, "regex nội bộ") {
		t.Error("operator-surface technical insight leaked into CEO home")
	}
	// No raw technical jargon in the CEO body.
	for _, jargon := range []string{"acceptance", "composite", "percentile", "ROI_norm"} {
		if strings.Contains(body, jargon) {
			t.Errorf("jargon %q leaked to exec home", jargon)
		}
	}
	// The CEO business approval IS shown.
	if !strings.Contains(body, "nâng ngân sách") {
		t.Error("CEO business approval missing from inbox")
	}
}

func TestModeSwitchPersists(t *testing.T) {
	s := testServer(t)
	seedExecFixture(t, s)
	// Switch to tech.
	rec := postForm(t, s, "/mode?to=tech", nil)
	cookie := rec.Result().Cookies()
	if len(cookie) == 0 || cookie[0].Value != "tech" {
		t.Fatalf("mode cookie not set: %+v", cookie)
	}
	// A request carrying the tech cookie hits standup, not exec home.
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = s.Cfg.Listen
	req.AddCookie(&http.Cookie{Name: "dandori_mode", Value: "tech"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	// Tech mode must render the operator standup, not the exec home. The
	// exec home's headline card ("Giá trị AI mang lại") is unique to it —
	// the phrase "Bảng điều hành" also appears on the tech-mode return
	// button, so assert on the headline instead.
	if strings.Contains(rr.Body.String(), "Giá trị AI mang lại") {
		t.Error("tech mode still showed exec home")
	}
	// And the tech sidebar's 3-pillar grouping must be present.
	if !strings.Contains(rr.Body.String(), "Capture — ghi nhận") {
		t.Error("tech mode sidebar missing pillar grouping")
	}
}

func TestExecApproveUsesAuditedPath(t *testing.T) {
	s := testServer(t)
	seedExecFixture(t, s)
	rec := postForm(t, s, "/exec/approve/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d", rec.Code)
	}
	var status, decidedBy string
	s.Store.DB.QueryRow(`SELECT status, COALESCE(decided_by,'') FROM approvals WHERE id=1`).Scan(&status, &decidedBy)
	if status != "approved" {
		t.Errorf("approval status: %s", status)
	}
	// Single-principal attribution — no per-human claim.
	if !strings.HasSuffix(decidedBy, "@console") {
		t.Errorf("decided_by: %q, want single-principal @console", decidedBy)
	}
	// Approving routed through the audited Decide path.
	var audits int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action LIKE 'approval_%'`).Scan(&audits)
	if audits == 0 {
		t.Error("approve produced no audit entry")
	}
}

// TestExecApproveRejectsKnowledge (M3) proves a knowledge-* approval cannot
// be one-click-approved through the generic exec inbox path — same F1
// exclusion class as the Slack reaction bridge — since this route's card
// never renders the full pinned body.
func TestExecApproveRejectsKnowledge(t *testing.T) {
	s := testServer(t)
	seedExecFixture(t, s)
	s.Store.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at)
		VALUES(NULL, 'observer:knowledge-publish:1', 'Đề xuất publish tri thức', 'now')`)
	var id int64
	s.Store.DB.QueryRow(`SELECT id FROM approvals WHERE action = 'observer:knowledge-publish:1'`).Scan(&id)

	rec := postForm(t, s, "/exec/approve/"+strconv.FormatInt(id, 10), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("knowledge approve via exec inbox → %d, want 403", rec.Code)
	}
	var status string
	s.Store.DB.QueryRow(`SELECT status FROM approvals WHERE id = ?`, id).Scan(&status)
	if status != "pending" {
		t.Errorf("knowledge approval status = %q, must stay pending (decided only at /reviews)", status)
	}
}

func TestExecDismissResolvesInsight(t *testing.T) {
	s := testServer(t)
	st := s.Store
	res, _ := st.DB.Exec(`INSERT INTO insights(type, subject, summary, evidence, class, surface, status, created_at)
		VALUES('agent_underused','bot','Agent bot ít việc.', '{}', 'auto','ceo','surfaced', 'now')`)
	id, _ := res.LastInsertId()
	rec := postForm(t, s, "/exec/insight/"+strconv.FormatInt(id, 10)+"/dismiss", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d", rec.Code)
	}
	var status string
	st.DB.QueryRow(`SELECT status FROM insights WHERE id=?`, id).Scan(&status)
	if status != "dismissed" {
		t.Errorf("insight status: %s", status)
	}
}
