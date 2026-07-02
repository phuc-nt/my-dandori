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
	cfg := &config.Config{IngestToken: "secret-token"}
	cfg.Budget.GlobalMonthlyUSD = 50
	return NewServer(cfg, st), st
}

func postBatch(t *testing.T, h http.Handler, token string, batch Batch) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(batch)
	req := httptest.NewRequest(http.MethodPost, "/ingest/events", bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-Dandori-Principal", "alice@mac")
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
	if operator != "alice@mac" {
		t.Errorf("operator from principal header: %q", operator)
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
