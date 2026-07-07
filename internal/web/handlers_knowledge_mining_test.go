package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// seedMiningRun mirrors internal/learn's own seeding helpers — duplicated
// here (package boundary) at the same minimal shape needed to make one
// signal fire (guardrail-block-then-done, the cheapest to seed for an HTTP
// round-trip test).
func seedMiningRun(t *testing.T, s *Server, runID, status string) {
	t.Helper()
	if _, err := s.Store.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a','a',datetime('now'))
		ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-5 * time.Minute)
	ended := started.Add(2 * time.Minute)
	if _, err := s.Store.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, status, started_at, ended_at, source)
		VALUES(?,?,?,'p',?,?,?,'hook')`,
		runID, runID, "a", status, started.Format(time.RFC3339), ended.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO events(run_id, ts, kind, ok) VALUES(?, datetime('now'), 'guardrail_block', 0)`,
		runID); err != nil {
		t.Fatal(err)
	}
}

// TestKnowledgeMiningPageViewerOK asserts GET /knowledge/mining renders for
// a viewer (read-only tab, no admin gate) and lists a seeded signal-matching
// run with its evidence badge — not a leaderboard, no operator column.
func TestKnowledgeMiningPageViewerOK(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4778")
	mustCreateAccount(t, s, "viewer1", "viewer")
	cookie := roleSession(t, s, "viewer1")
	seedMiningRun(t, s, "mine-run-1", "done")

	rec := getAs(t, s, cookie, "/knowledge/mining")
	if rec.Code != http.StatusOK {
		t.Fatalf("/knowledge/mining as viewer = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "mine-run-1") {
		t.Errorf("/knowledge/mining should list mine-run-1, body=%s", body)
	}
	if !strings.Contains(body, "guardrail") {
		t.Errorf("/knowledge/mining should show the guardrail signal badge, body=%s", body)
	}
}

// TestKnowledgeMiningDismissThenRunDetailStillShows drives the dismiss route
// over real HTTP and asserts the run vanishes from the mining tab but
// /runs/{id} (run detail) still renders it in full — the M2 contract
// end-to-end, not just at the learn-package level.
func TestKnowledgeMiningDismissThenRunDetailStillShows(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4779")
	mustCreateAccount(t, s, "viewer1", "viewer")
	cookie := roleSession(t, s, "viewer1")
	seedMiningRun(t, s, "mine-run-2", "done")

	before := getAs(t, s, cookie, "/knowledge/mining")
	if !strings.Contains(before.Body.String(), "mine-run-2") {
		t.Fatal("setup: mine-run-2 should appear before dismiss")
	}

	dismissRec := postFormAs(t, s, cookie, "/knowledge/mining/mine-run-2/dismiss", url.Values{"reason": {"noise"}})
	if dismissRec.Code == http.StatusForbidden {
		t.Fatalf("dismiss as viewer = 403, want allowed")
	}

	after := getAs(t, s, cookie, "/knowledge/mining")
	if strings.Contains(after.Body.String(), "mine-run-2") {
		t.Error("mine-run-2 should be absent from /knowledge/mining after dismiss")
	}

	detail := getAs(t, s, cookie, "/runs/mine-run-2")
	if detail.Code != http.StatusOK {
		t.Fatalf("/runs/mine-run-2 after dismiss = %d, want 200 (M2: dismiss must not hide from run detail)", detail.Code)
	}
	if !strings.Contains(detail.Body.String(), "mine-run-2") {
		t.Error("run detail must still show the dismissed run (M2 — dismiss is reading-list-only)")
	}
}
