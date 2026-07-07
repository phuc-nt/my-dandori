package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

// draftFixture opens a fresh store + a fake OpenRouter that always returns
// draftText, counting calls so single-flight/budget tests can assert 0/1
// calls were actually made upstream.
func draftFixture(t *testing.T, draftText string) (*config.Config, *store.Store, *int32) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond) // widen the concurrency window for H2 test
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": draftText}}},
			"usage": map[string]any{"total_tokens": 50},
		})
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{OpenRouterKey: "k", OpenRouterModel: "test/model", ChatDailyTokenBudget: 200_000}
	// draft.go builds its own *Client internally via NewClient+BaseURL
	// override isn't exposed there — swap Client.BaseURL is not settable
	// from outside DraftPractice, so tests instead point OpenRouter's own
	// well-known base at the fake server via a package-level test hook.
	testOpenRouterBaseURL = srv.URL
	t.Cleanup(func() { testOpenRouterBaseURL = "" })
	return cfg, st, &calls
}

func seedDraftRun(t *testing.T, st *store.Store, runID string) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a','a',?) ON CONFLICT(id) DO NOTHING`, store.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, task_key, status, started_at, ended_at, model, cost_usd, lines_added, lines_deleted, source)
		VALUES(?,?,?,'proj','TASK-1','done',?,?,'m',1.23,10,2,'hook')`,
		runID, runID, "a", store.Now(), store.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestDraftModelEmptyHardFails(t *testing.T) {
	cfg, st, calls := draftFixture(t, "# Title\nbody")
	cfg.OpenRouterModel = ""
	seedDraftRun(t, st, "r1")

	title, body, model, err := DraftPractice(cfg, st, "op1", "r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(body, "chưa cấu hình model") {
		t.Errorf("body = %q, want model-empty message", body)
	}
	if title != "" || model != "" {
		t.Errorf("title=%q model=%q, want empty on hard-fail message", title, model)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Errorf("OpenRouter called %d times, want 0 (model empty must short-circuit)", *calls)
	}
}

func TestDraftBudgetExceededReturnsMessageNoCall(t *testing.T) {
	cfg, st, calls := draftFixture(t, "# Title\nbody")
	seedDraftRun(t, st, "r1")

	sessionID, err := Session(st, "draft:op1")
	if err != nil {
		t.Fatal(err)
	}
	AddTokens(st, sessionID, 999_999)

	_, body, _, err := DraftPractice(cfg, st, "op1", "r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(body, "ngân sách") {
		t.Errorf("body = %q, want budget message", body)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Errorf("OpenRouter called %d times, want 0 (budget guard must short-circuit)", *calls)
	}
}

func TestDraftSparseRunNoStandardEvidenceStillYieldsBundle(t *testing.T) {
	cfg, st, calls := draftFixture(t, "# Practice title\nbody text")
	seedDraftRun(t, st, "r-sparse") // no steering, no gate, no guardrail, no revert

	title, body, model, err := DraftPractice(cfg, st, "op-sparse", "r-sparse")
	if err != nil {
		t.Fatalf("sparse run must not error (M4): %v", err)
	}
	if model != "test/model" {
		t.Errorf("model = %q, want test/model", model)
	}
	if title == "" || body == "" {
		t.Errorf("sparse run draft empty: title=%q body=%q", title, body)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Errorf("OpenRouter called %d times, want exactly 1", *calls)
	}
}

func TestDraftPromptFramesEvidenceAsData(t *testing.T) {
	var capturedBody string
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := readAll(r)
		capturedBody = raw
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "# T\nB"}}},
			"usage": map[string]any{"total_tokens": 10},
		})
	}))
	defer srv.Close()
	testOpenRouterBaseURL = srv.URL
	defer func() { testOpenRouterBaseURL = "" }()

	cfg := &config.Config{OpenRouterKey: "k", OpenRouterModel: "test/model", ChatDailyTokenBudget: 200_000}
	seedDraftRun(t, st, "r-prompt")
	if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, ?, 'steering_msg', 'thử lại đi, sai rồi')`,
		"r-prompt", store.Now()); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := DraftPractice(cfg, st, "op-prompt", "r-prompt"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(capturedBody, "DỮ LIỆU") {
		t.Errorf("prompt must frame evidence as DATA, got: %s", capturedBody)
	}
	if !strings.Contains(capturedBody, "corrective") {
		t.Errorf("prompt must include classified steering label, got: %s", capturedBody)
	}
	if strings.Contains(capturedBody, "transcript_path") {
		t.Errorf("prompt must never mention transcript_path, got: %s", capturedBody)
	}
}

func TestDraftNeverReadsTranscriptPath(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedDraftRun(t, st, "r-transcript")
	secretPath := "/private/should-never-appear-in-evidence.jsonl"
	if _, err := st.DB.Exec(`UPDATE runs SET transcript_path = ? WHERE id = ?`, secretPath, "r-transcript"); err != nil {
		t.Fatal(err)
	}

	bundle, err := gatherEvidence(st, "r-transcript")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bundle, secretPath) {
		t.Errorf("evidence bundle leaked transcript_path: %s", bundle)
	}
	if strings.Contains(strings.ToLower(bundle), "transcript_path") {
		t.Errorf("evidence bundle must never reference transcript_path, got: %s", bundle)
	}
}

// H2: two concurrent drafts for the SAME principal — only one may reach
// OpenRouter; the other must be blocked with the single-flight message and
// make zero upstream calls.
func TestDraftSingleFlightBlocksConcurrentSamePrincipal(t *testing.T) {
	cfg, st, calls := draftFixture(t, "# T\nB")
	seedDraftRun(t, st, "r1")

	var wg sync.WaitGroup
	results := make([]string, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			_, body, _, err := DraftPractice(cfg, st, "same-op", "r1")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			results[i] = body
		}(i)
	}
	wg.Wait()

	if atomic.LoadInt32(calls) != 1 {
		t.Errorf("OpenRouter called %d times for concurrent same-principal drafts, want exactly 1", *calls)
	}
	blockedCount := 0
	for _, r := range results {
		if strings.Contains(r, "đang soạn") || strings.Contains(strings.ToLower(r), "thử lại sau") {
			blockedCount++
		}
	}
	if blockedCount != 1 {
		t.Errorf("expected exactly one blocked single-flight response, got %d among %v", blockedCount, results)
	}
}

// A different principal must NOT be blocked by another principal's in-flight
// draft — single-flight is keyed per-principal, not global.
func TestDraftSingleFlightDoesNotBlockDifferentPrincipal(t *testing.T) {
	cfg, st, calls := draftFixture(t, "# T\nB")
	seedDraftRun(t, st, "r1")
	seedDraftRun(t, st, "r2")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		DraftPractice(cfg, st, "op-a", "r1")
	}()
	go func() {
		defer wg.Done()
		DraftPractice(cfg, st, "op-b", "r2")
	}()
	wg.Wait()

	if atomic.LoadInt32(calls) != 2 {
		t.Errorf("OpenRouter called %d times for 2 distinct principals, want 2", *calls)
	}
}

func readAll(r *http.Request) (string, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf), nil
}
