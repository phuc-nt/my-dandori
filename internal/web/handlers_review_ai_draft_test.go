package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/chat"
)

// seedDraftableRun inserts a minimal run row so /knowledge/draft has
// something to build an evidence bundle from (mirrors seedMiningRun's
// minimal-agent-then-run shape from handlers_knowledge_mining_test.go).
func seedDraftableRun(t *testing.T, s *Server, runID string) {
	t.Helper()
	if _, err := s.Store.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a','a',datetime('now'))
		ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-5 * time.Minute)
	ended := started.Add(2 * time.Minute)
	if _, err := s.Store.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, status, started_at, ended_at, model, cost_usd, lines_added, lines_deleted, source)
		VALUES(?,?,?,'p','done',?,?,'m',1.0,1,1,'hook')`,
		runID, runID, "a", started.Format(time.RFC3339), ended.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
}

// TestKnowledgeDraftViewerBlocked asserts the token-spending draft route is
// gated to "not viewer" — a read-only viewer must never be able to trigger
// an OpenRouter call.
func TestKnowledgeDraftViewerBlocked(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4780")
	s.Cfg.OpenRouterKey = "k"
	s.Cfg.OpenRouterModel = "test/model"
	mustCreateAccount(t, s, "viewer1", "viewer")
	cookie := roleSession(t, s, "viewer1")
	seedDraftableRun(t, s, "draft-run-1")

	rec := postFormAs(t, s, cookie, "/knowledge/draft", url.Values{"run_id": {"draft-run-1"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer POST /knowledge/draft = %d, want 403 (spends tokens, viewer-blocked)", rec.Code)
	}
}

// TestKnowledgeDraftAdminAllowedFailOpen drives the full round trip as an
// admin with NO OpenRouter model configured (hard-fail path inside
// chat.DraftPractice) and asserts the handler still renders 200 with an
// editable, submittable nominate form rather than an error page — the
// handler-level fail-open contract, distinct from chat.DraftPractice's own
// unit tests.
func TestKnowledgeDraftAdminAllowedFailOpen(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4781")
	s.Cfg.OpenRouterKey = "k"
	s.Cfg.OpenRouterModel = "" // forces chat.DraftPractice's hard-fail message
	mustCreateAccount(t, s, "admin1", "admin")
	cookie := roleSession(t, s, "admin1")
	seedDraftableRun(t, s, "draft-run-2")

	rec := postFormAs(t, s, cookie, "/knowledge/draft", url.Values{"run_id": {"draft-run-2"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin POST /knowledge/draft = %d, want 200 (fail-open, never an error page)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<form") {
		t.Errorf("fail-open response must still contain a submittable nominate form, body=%s", body)
	}
	if strings.Contains(body, `name="origin"`) {
		t.Errorf("no origin=ai-draft hidden field expected when draft failed (Model empty), body=%s", body)
	}
	if !strings.Contains(body, "viết tay") {
		t.Errorf("fail-open response must show the write-by-hand message, body=%s", body)
	}
}

// TestKnowledgeDraftSuccessHasHiddenOriginFields drives the full round trip
// with a fake OpenRouter server standing in for the real one, asserting the
// returned fragment carries the hidden origin=ai-draft + origin_model +
// provenance_run_ids fields P2's /knowledge/nominate handler reads.
func TestKnowledgeDraftSuccessHasHiddenOriginFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "# Practice title\nBody text here."}}},
			"usage": map[string]any{"total_tokens": 42},
		})
	}))
	defer srv.Close()
	chat.SetTestOpenRouterBaseURL(srv.URL)
	defer chat.SetTestOpenRouterBaseURL("")

	s := testServerWithListen(t, "127.0.0.1:4782")
	s.Cfg.OpenRouterKey = "k"
	s.Cfg.OpenRouterModel = "test/model"
	mustCreateAccount(t, s, "admin2", "admin")
	cookie := roleSession(t, s, "admin2")
	seedDraftableRun(t, s, "draft-run-3")

	rec := postFormAs(t, s, cookie, "/knowledge/draft", url.Values{"run_id": {"draft-run-3"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin POST /knowledge/draft = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="origin" value="ai-draft"`) {
		t.Errorf("successful draft fragment must carry hidden origin=ai-draft, body=%s", body)
	}
	if !strings.Contains(body, `name="origin_model" value="test/model"`) {
		t.Errorf("successful draft fragment must carry hidden origin_model, body=%s", body)
	}
	if !strings.Contains(body, `name="provenance_run_ids" value="draft-run-3"`) {
		t.Errorf("successful draft fragment must carry hidden provenance_run_ids, body=%s", body)
	}
	if !strings.Contains(body, "Practice title") {
		t.Errorf("draft title should be pre-filled into the form, body=%s", body)
	}
}

// TestKnowledgeMiningButtonKeyGated asserts the "Soạn nháp (AI)" button on
// the mining tab only renders when Cfg.OpenRouterKey is set — otherwise the
// tab must render with no way to spend tokens the operator never configured.
func TestKnowledgeMiningButtonKeyGated(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4783")
	mustCreateAccount(t, s, "viewer3", "viewer")
	cookie := roleSession(t, s, "viewer3")
	seedMiningRun(t, s, "mine-gate-1", "done")

	noKey := getAs(t, s, cookie, "/knowledge/mining")
	if strings.Contains(noKey.Body.String(), "Soạn nháp") {
		t.Errorf("draft button must be absent when OpenRouterKey is unset, body=%s", noKey.Body.String())
	}

	s.Cfg.OpenRouterKey = "k"
	withKey := getAs(t, s, cookie, "/knowledge/mining")
	if !strings.Contains(withKey.Body.String(), "Soạn nháp") {
		t.Errorf("draft button must render when OpenRouterKey is set, body=%s", withKey.Body.String())
	}
}

// TestRunDetailDraftButtonKeyGated mirrors the mining-tab gate check for the
// run detail page's own "Soạn nháp practice (AI)" button.
func TestRunDetailDraftButtonKeyGated(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4784")
	mustCreateAccount(t, s, "viewer4", "viewer")
	cookie := roleSession(t, s, "viewer4")
	seedDraftableRun(t, s, "draft-run-detail-1")

	noKey := getAs(t, s, cookie, "/runs/draft-run-detail-1")
	if noKey.Code != http.StatusOK {
		t.Fatalf("/runs/draft-run-detail-1 = %d, want 200", noKey.Code)
	}
	if strings.Contains(noKey.Body.String(), "Soạn nháp practice") {
		t.Errorf("run detail draft button must be absent when OpenRouterKey is unset")
	}

	s.Cfg.OpenRouterKey = "k"
	withKey := getAs(t, s, cookie, "/runs/draft-run-detail-1")
	if !strings.Contains(withKey.Body.String(), "Soạn nháp practice") {
		t.Errorf("run detail draft button must render when OpenRouterKey is set")
	}
}
