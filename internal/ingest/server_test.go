package ingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

func testServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{IngestToken: "secret-token", AllowLegacyIngestToken: true}
	cfg.Budget.GlobalMonthlyUSD = 50
	return NewServer(cfg, st), st
}

func postBatch(t *testing.T, h http.Handler, token string, batch Batch) *httptest.ResponseRecorder {
	t.Helper()
	req := jsonPostRequest(t, batch)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-Dandori-Principal-Hint", "alice@mac") // diagnostic only — server derives principal from the token
	return doRequest(h, req)
}

// jsonPostRequest builds a bare POST /ingest/events request with no auth
// header set yet — callers attach Authorization themselves.
func jsonPostRequest(t *testing.T, batch Batch) *http.Request {
	t.Helper()
	b, _ := json.Marshal(batch)
	req := httptest.NewRequest(http.MethodPost, "/ingest/events", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func doRequest(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func sampleBatch() Batch {
	ok := int64(1)
	return Batch{Records: []Record{
		{Type: "event", SessionID: "s1", AgentName: "My Agent", Project: "proj",
			ULID: "01-aaa", Kind: "tool_use", Tool: "Bash", Payload: "go test", ClientTS: "2026-07-02T10:00:00Z"},
		{Type: "event", SessionID: "s1", AgentName: "My Agent", Project: "proj",
			ULID: "01-bbb", Kind: "tool_result", Tool: "Bash", OK: &ok, Payload: "ok"},
		{Type: "finalize", SessionID: "s1", AgentName: "My Agent", Project: "proj",
			Finalize: &RunFinalize{Model: "m", InputTokens: 100, OutputTokens: 50, CostUSD: 0.5,
				MidRunMsgs: 2, PromptWords: 40, LinesAdded: 10, Status: "done"}},
	}}
}

func TestIngestAuth(t *testing.T) {
	s, st := testServer(t)
	h := s.Handler()
	if w := postBatch(t, h, "", sampleBatch()); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", w.Code)
	}
	if w := postBatch(t, h, "wrong", sampleBatch()); w.Code != http.StatusUnauthorized {
		t.Errorf("bad token: %d, want 401", w.Code)
	}
	var runs int
	st.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs)
	if runs != 0 {
		t.Errorf("unauthorized request created %d runs", runs)
	}
	if w := postBatch(t, h, "secret-token", sampleBatch()); w.Code != http.StatusOK {
		t.Errorf("good token: %d body=%s", w.Code, w.Body)
	}
}

// The ingest mux must know NOTHING about the console (red-team C1/C2).
func TestIngestCarriesNoConsoleRoutes(t *testing.T) {
	s, _ := testServer(t)
	h := s.Handler()
	for _, path := range []string{"/", "/api/kill", "/reviews", "/budgets", "/rules"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404 even with a valid token", path, w.Code)
		}
	}
}

// Spool replay = same batch POSTed twice. Events must count exactly once.
func TestIngestIdempotentReplay(t *testing.T) {
	s, st := testServer(t)
	h := s.Handler()
	for i := 0; i < 2; i++ {
		if w := postBatch(t, h, "secret-token", sampleBatch()); w.Code != http.StatusOK {
			t.Fatalf("post %d: %d %s", i, w.Code, w.Body)
		}
	}
	var events int
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE kind IN ('tool_use','tool_result')`).Scan(&events)
	if events != 2 {
		t.Errorf("events after replay: %d, want 2 (ULID dedup)", events)
	}
	var cost float64
	var operator, status string
	err := st.DB.QueryRow(`SELECT cost_usd, COALESCE(operator_id,''), status FROM runs WHERE id='s1'`).
		Scan(&cost, &operator, &status)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 0.5 || status != "done" {
		t.Errorf("finalize: cost=%v status=%s", cost, status)
	}
	// [H1] Legacy shared-token requests attribute to the fixed principal,
	// NEVER to the client-supplied X-Dandori-Principal-Hint header — a
	// spoofed header here must not change attribution.
	if operator != legacyPrincipal {
		t.Errorf("operator = %q, want fixed legacy principal %q (header must not be trusted)", operator, legacyPrincipal)
	}
	// Steering + prompt proxy landed as SET-semantics numeric events.
	var msgs, words string
	st.DB.QueryRow(`SELECT payload FROM events WHERE run_id='s1' AND kind='user_msg'`).Scan(&msgs)
	st.DB.QueryRow(`SELECT payload FROM events WHERE run_id='s1' AND kind='prompt_proxy'`).Scan(&words)
	if msgs != "2" || words != `{"w":40,"spec":0}` {
		t.Errorf("numeric events: user_msg=%q prompt_proxy=%q", msgs, words)
	}
}

// Payloads are redacted again server-side (defense in depth).
func TestIngestRedactsServerSide(t *testing.T) {
	s, st := testServer(t)
	batch := Batch{Records: []Record{{Type: "event", SessionID: "s2", AgentName: "a",
		ULID: "02-aaa", Kind: "tool_use", Tool: "Bash",
		Payload: `curl -H "Authorization: Bearer sk-verysecretkey12345"`}}}
	if w := postBatch(t, s.Handler(), "secret-token", batch); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	var payload string
	st.DB.QueryRow(`SELECT payload FROM events WHERE run_id='s2'`).Scan(&payload)
	if bytes.Contains([]byte(payload), []byte("verysecretkey")) {
		t.Errorf("secret survived ingest: %q", payload)
	}
}

// Events without an idempotency key are refused rather than double-counted.
func TestIngestRejectsKeylessEvents(t *testing.T) {
	s, st := testServer(t)
	batch := Batch{Records: []Record{{Type: "event", SessionID: "s3", AgentName: "a", Kind: "tool_use", Tool: "Bash"}}}
	if w := postBatch(t, s.Handler(), "secret-token", batch); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='s3'`).Scan(&n)
	if n != 0 {
		t.Errorf("keyless event inserted: %d", n)
	}
}

func TestPolicyEndpoint(t *testing.T) {
	s, st := testServer(t)
	st.SetSetting("kill_switch_global", "1")
	req := httptest.NewRequest(http.MethodGet, "/ingest/policy", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("policy: %d", w.Code)
	}
	var snap struct {
		KillGlobal bool `json:"kill_global"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil || !snap.KillGlobal {
		t.Errorf("snapshot: %+v err=%v", snap, err)
	}
}

// TestPolicyEndpointServerCached proves the server-side rebuild cache (fix
// G — handlePolicy must not recompute RiskScore for every active run on
// every single poll from every fleet machine): a state change made directly
// in the store between two rapid requests for the SAME operator must not be
// visible until the cache TTL elapses — the second response is served from
// cache, not rebuilt.
func TestPolicyEndpointServerCached(t *testing.T) {
	s, st := testServer(t)
	pollAs := func(token string) bool {
		req := httptest.NewRequest(http.MethodGet, "/ingest/policy", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("policy: %d", w.Code)
		}
		var snap struct {
			KillGlobal bool `json:"kill_global"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
			t.Fatal(err)
		}
		return snap.KillGlobal
	}

	if got := pollAs("secret-token"); got {
		t.Fatalf("first poll: kill_global = %v, want false (nothing set yet)", got)
	}
	st.SetSetting("kill_switch_global", "1") // mutate directly, bypassing the cache
	if got := pollAs("secret-token"); got {
		t.Errorf("second rapid poll (same operator) = %v, want false — cache must serve the stale build, not rebuild", got)
	}

	// A genuinely different operator gets its own cache slot: it has never
	// polled before, so its first request is a cache MISS and must rebuild
	// fresh — seeing the kill switch that was already set above, proving the
	// legacy principal's cached (stale) entry did not leak into or block a
	// different operator's slot.
	otherToken := seedOperatorToken(t, s, "operator-other", "other-laptop")
	if got := pollAs(otherToken); !got {
		t.Errorf("new operator's first poll = %v, want true (fresh build reflects current state, own cache slot)", got)
	}
}

// TestPolicyEndpointScopedPerOperator is THE leak-J end-to-end test: operator
// B's /ingest/policy response must never contain operator A's run id, even
// though both runs exist in the same database and both operators poll the
// same endpoint.
func TestPolicyEndpointScopedPerOperator(t *testing.T) {
	s, st := testServer(t)
	tokenA := seedOperatorToken(t, s, "operator-a", "a-laptop")
	tokenB := seedOperatorToken(t, s, "operator-b", "b-laptop")

	// Only tool_use/tool_result events, no finalize record — the run stays
	// 'running' (finalize is what would flip it to 'done'), so it is
	// score-eligible under the active-run 24h bound.
	toolEventBatch := func(runID string) Batch {
		ok := int64(1)
		return Batch{Records: []Record{
			{Type: "event", SessionID: runID, AgentName: "My Agent", Project: "proj",
				ULID: runID + "-1", Kind: "tool_use", Tool: "Bash", Payload: "go test", ClientTS: "2026-07-02T10:00:00Z"},
			{Type: "event", SessionID: runID, AgentName: "My Agent", Project: "proj",
				ULID: runID + "-2", Kind: "tool_result", Tool: "Bash", OK: &ok, Payload: "ok"},
		}}
	}
	if w := postBatch(t, s.Handler(), tokenA, toolEventBatch("run-a")); w.Code != http.StatusOK {
		t.Fatalf("post A: %d %s", w.Code, w.Body)
	}
	if w := postBatch(t, s.Handler(), tokenB, toolEventBatch("run-b")); w.Code != http.StatusOK {
		t.Fatalf("post B: %d %s", w.Code, w.Body)
	}
	// sampleBatch's tool_use event has ts in 2026 — outside the 24h active
	// window from "now" in a real clock, so force started_at to now for both
	// runs to make them score-eligible under the zombie bound.
	if _, err := st.DB.Exec(`UPDATE runs SET started_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')`); err != nil {
		t.Fatal(err)
	}

	fetchPolicy := func(token string) map[string]int {
		req := httptest.NewRequest(http.MethodGet, "/ingest/policy", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("policy: %d %s", w.Code, w.Body)
		}
		var snap struct {
			RiskScores map[string]int `json:"risk_scores"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
			t.Fatal(err)
		}
		return snap.RiskScores
	}

	scoresB := fetchPolicy(tokenB)
	if _, leaked := scoresB["run-a"]; leaked {
		t.Errorf("operator B's snapshot leaks operator A's run: %+v", scoresB)
	}
	if _, present := scoresB["run-b"]; !present {
		t.Errorf("operator B's snapshot missing its own run: %+v", scoresB)
	}

	scoresA := fetchPolicy(tokenA)
	if _, leaked := scoresA["run-b"]; leaked {
		t.Errorf("operator A's snapshot leaks operator B's run: %+v", scoresA)
	}
}
