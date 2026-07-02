package capture

import (
	"os"
	"path/filepath"
	"testing"
)

// fixture mirrors real ~/.claude/projects transcript lines (trimmed).
const fixtureTranscript = `{"type":"custom-title","customTitle":"x","sessionId":"s1"}
{"type":"user","cwd":"/work/proj","message":{"role":"user","content":"Fix SCRUM-42 login bug"}}
{"type":"assistant","message":{"id":"msg_1","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":1000,"cache_creation_input_tokens":200}}}
{"type":"assistant","message":{"id":"msg_1","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":1000,"cache_creation_input_tokens":200}}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"also update the docs"}]}}
{"type":"assistant","message":{"id":"msg_2","model":"claude-sonnet-5","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
{"type":"file-history-snapshot"}
`

func writeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sess-1.jsonl")
	if err := os.WriteFile(path, []byte(fixtureTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseTranscript(t *testing.T) {
	u, err := ParseTranscript(writeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// msg_1 appears twice → counted once. msg_1 + msg_2 totals:
	if u.Input != 110 || u.Output != 55 || u.CacheRead != 1000 || u.CacheWrite != 200 {
		t.Errorf("usage dedup wrong: %+v", u)
	}
	if u.Model != "claude-sonnet-5" {
		t.Errorf("model: %s", u.Model)
	}
	if u.FirstUser != "Fix SCRUM-42 login bug" {
		t.Errorf("first user: %q", u.FirstUser)
	}
	// "also update the docs" arrives after the assistant started → 1 mid-run
	// steering message; the opening prompt is not counted.
	if u.MidRunMsgs != 1 {
		t.Errorf("mid-run msgs: %d, want 1", u.MidRunMsgs)
	}
	if u.CWD != "/work/proj" {
		t.Errorf("cwd: %q", u.CWD)
	}
	if got := FindTaskKey(u.FirstUser); got != "SCRUM-42" {
		t.Errorf("task key: %q", got)
	}
}

func TestParseTranscriptIdempotent(t *testing.T) {
	path := writeFixture(t)
	u1, _ := ParseTranscript(path)
	u2, _ := ParseTranscript(path)
	if u1 != u2 {
		t.Errorf("reparse must be deterministic: %+v vs %+v", u1, u2)
	}
}
