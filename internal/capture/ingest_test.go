package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

func testIngestor(t *testing.T) *Ingestor {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "no.yaml"))
	return &Ingestor{Cfg: cfg, St: st}
}

func TestTaskKeyValidationAndProvenance(t *testing.T) {
	g := testIngestor(t)
	if _, err := g.St.DB.Exec(`INSERT INTO work_items(source, key, title) VALUES('jira','SCRUM-42','x')`); err != nil {
		t.Fatal(err)
	}
	transcript := writeFixture(t) // prompt: "Fix SCRUM-42 login bug"
	if _, err := g.EnsureRun("wsess", "/work/proj", transcript, "watcher"); err != nil {
		t.Fatal(err)
	}
	if err := g.ReconcileUsage("wsess", transcript); err != nil {
		t.Fatal(err)
	}
	var key, src string
	g.St.DB.QueryRow(`SELECT COALESCE(task_key,'') FROM runs WHERE id='wsess'`).Scan(&key)
	g.St.DB.QueryRow(`SELECT COALESCE(payload,'') FROM events WHERE run_id='wsess' AND kind='task_key_source'`).Scan(&src)
	if key != "SCRUM-42" {
		t.Errorf("validated key should link: %q", key)
	}
	if src != "source=transcript key=SCRUM-42" {
		t.Errorf("provenance event wrong: %q", src)
	}

	// Reconcile again: idempotent, no duplicate provenance event.
	if err := g.ReconcileUsage("wsess", transcript); err != nil {
		t.Fatal(err)
	}
	var n int
	g.St.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='wsess' AND kind='task_key_source'`).Scan(&n)
	if n != 1 {
		t.Errorf("provenance event duplicated: %d", n)
	}
}

func TestTaskKeyDroppedWhenUntracked(t *testing.T) {
	g := testIngestor(t)
	// No work_items seeded → SCRUM-42 does not validate → no link, no mislink.
	transcript := writeFixture(t)
	if _, err := g.EnsureRun("usess", "/work/proj", transcript, "watcher"); err != nil {
		t.Fatal(err)
	}
	if err := g.ReconcileUsage("usess", transcript); err != nil {
		t.Fatal(err)
	}
	var key string
	g.St.DB.QueryRow(`SELECT COALESCE(task_key,'') FROM runs WHERE id='usess'`).Scan(&key)
	if key != "" {
		t.Errorf("untracked key must not link: %q", key)
	}
}

func TestSteeringTextCaptureAndIdempotent(t *testing.T) {
	g := testIngestor(t)
	// tsFixture: opening "start" then one mid-run "steer".
	path := filepath.Join(t.TempDir(), "steer.jsonl")
	if err := os.WriteFile(path, []byte(tsFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.EnsureRun("ssess", "/work/proj", path, "watcher"); err != nil {
		t.Fatal(err)
	}
	if err := g.ReconcileUsage("ssess", path); err != nil {
		t.Fatal(err)
	}

	var texts []string
	rows, _ := g.St.DB.Query(`SELECT payload FROM events WHERE run_id='ssess' AND kind='steering_msg'`)
	for rows.Next() {
		var p string
		rows.Scan(&p)
		texts = append(texts, p)
	}
	rows.Close()
	if len(texts) != 1 || texts[0] != "steer" {
		t.Errorf("steering texts = %v, want [steer] (opening prompt excluded)", texts)
	}

	// Reparse: idempotent, no duplicate.
	if err := g.ReconcileUsage("ssess", path); err != nil {
		t.Fatal(err)
	}
	var n int
	g.St.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='ssess' AND kind='steering_msg'`).Scan(&n)
	if n != 1 {
		t.Errorf("steering events duplicated on reparse: %d", n)
	}
}

func TestSteeringTextRedacted(t *testing.T) {
	g := testIngestor(t)
	secret := "use token sk-ant-api03-SHOULDREDACT12345 now"
	body := `{"type":"user","timestamp":"2026-07-06T10:00:00.000Z","message":{"role":"user","content":"open"}}
{"type":"assistant","timestamp":"2026-07-06T10:00:01.000Z","message":{"id":"a1","model":"m","usage":{"input_tokens":1,"output_tokens":1}}}
{"type":"user","timestamp":"2026-07-06T10:01:00.000Z","message":{"role":"user","content":"` + secret + `"}}
`
	path := filepath.Join(t.TempDir(), "sec.jsonl")
	os.WriteFile(path, []byte(body), 0o644)
	if _, err := g.EnsureRun("secsess", "/work/proj", path, "watcher"); err != nil {
		t.Fatal(err)
	}
	if err := g.ReconcileUsage("secsess", path); err != nil {
		t.Fatal(err)
	}
	var payload string
	g.St.DB.QueryRow(`SELECT payload FROM events WHERE run_id='secsess' AND kind='steering_msg'`).Scan(&payload)
	if strings.Contains(payload, "sk-ant-api03-SHOULDREDACT12345") {
		t.Errorf("secret not redacted in steering payload: %q", payload)
	}
}

func hookIn(session, transcript string) HookInput {
	return HookInput{
		SessionID:      session,
		TranscriptPath: transcript,
		CWD:            "/work/proj",
		ToolName:       "Bash",
		ToolInput:      json.RawMessage(`{"command":"ls"}`),
		ToolResponse:   json.RawMessage(`{"stdout":"ok"}`),
	}
}

func TestFullHookCycle(t *testing.T) {
	g := testIngestor(t)
	// The task key from the transcript only links when it names a tracked
	// work item — validation is what keeps a stray key from mislinking.
	if _, err := g.St.DB.Exec(`INSERT INTO work_items(source, key, title) VALUES('jira','SCRUM-42','x')`); err != nil {
		t.Fatal(err)
	}
	transcript := writeFixture(t)
	in := hookIn("sess-1", transcript)

	if err := g.SessionStart(in); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.PreTool(in); err != nil {
		t.Fatal(err)
	}
	if err := g.PostTool(in); err != nil {
		t.Fatal(err)
	}
	if err := g.Stop(in); err != nil {
		t.Fatal(err)
	}

	var status, taskKey string
	var cost float64
	var input int64
	err := g.St.DB.QueryRow(`SELECT status, COALESCE(task_key,''), cost_usd, input_tokens
		FROM runs WHERE session_id = 'sess-1'`).Scan(&status, &taskKey, &cost, &input)
	if err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Errorf("status: %s", status)
	}
	if taskKey != "SCRUM-42" {
		t.Errorf("task_key: %s", taskKey)
	}
	if input != 110 || cost <= 0 {
		t.Errorf("usage not reconciled: input=%d cost=%f", input, cost)
	}

	var nEvents int
	g.St.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id = 'sess-1'
		AND kind IN ('tool_use','tool_result')`).Scan(&nEvents)
	if nEvents != 2 {
		t.Errorf("events: %d", nEvents)
	}
}

func TestStopIdempotentUsage(t *testing.T) {
	g := testIngestor(t)
	in := hookIn("sess-2", writeFixture(t))
	if err := g.SessionStart(in); err != nil {
		t.Fatal(err)
	}
	// Stop twice — SET semantics must not double tokens.
	g.Stop(in)
	g.Stop(in)
	var input int64
	g.St.DB.QueryRow(`SELECT input_tokens FROM runs WHERE session_id='sess-2'`).Scan(&input)
	if input != 110 {
		t.Errorf("double-counted usage: %d", input)
	}
}

func TestIsErrorResponse(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`{"stdout":"fine"}`, false},
		{`{"is_error":true,"content":"boom"}`, true},
		{`{"success":false}`, true},
	}
	for _, c := range cases {
		if got := isErrorResponse([]byte(c.raw)); got != c.want {
			t.Errorf("isErrorResponse(%s) = %v", c.raw, got)
		}
	}
}
