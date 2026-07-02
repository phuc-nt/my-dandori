package capture

import (
	"encoding/json"
	"path/filepath"
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
