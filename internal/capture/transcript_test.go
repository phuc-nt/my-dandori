package capture

import (
	"os"
	"path/filepath"
	"reflect"
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
	if !reflect.DeepEqual(u1, u2) {
		t.Errorf("reparse must be deterministic: %+v vs %+v", u1, u2)
	}
}

// tsFixture carries per-line timestamps (real transcripts always do) plus a
// gitBranch — the phase-1 end-time and phase-2 branch sources.
const tsFixture = `{"type":"user","cwd":"/work/proj","gitBranch":"feature/SCRUM-42","timestamp":"2026-07-06T10:00:00.000Z","message":{"role":"user","content":"start"}}
{"type":"assistant","timestamp":"2026-07-06T10:00:05.000Z","message":{"id":"m1","model":"claude-sonnet-5","usage":{"input_tokens":10,"output_tokens":5}}}
{"type":"user","timestamp":"2026-07-06T10:02:00.000Z","message":{"role":"user","content":[{"type":"text","text":"steer"}]}}
{"type":"assistant","timestamp":"2026-07-06T10:03:30.500Z","message":{"id":"m2","model":"claude-sonnet-5","usage":{"input_tokens":1,"output_tokens":1}}}
`

func TestParseTranscriptTimestamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ts.jsonl")
	if err := os.WriteFile(path, []byte(tsFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := ParseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.FirstTS.Format("15:04:05"); got != "10:00:00" {
		t.Errorf("FirstTS = %s, want 10:00:00", got)
	}
	if got := u.LastTS.Format("15:04:05"); got != "10:03:30" {
		t.Errorf("LastTS = %s, want 10:03:30", got)
	}
	if !u.LastTS.After(u.FirstTS) {
		t.Error("LastTS must be after FirstTS")
	}
	if u.GitBranch != "feature/SCRUM-42" {
		t.Errorf("GitBranch = %q", u.GitBranch)
	}
}

func TestParseTranscriptNoTimestamps(t *testing.T) {
	// The original fixture has no timestamp fields — bounds must stay zero
	// so reconcile treats end-time as unknown rather than fabricating it.
	u, err := ParseTranscript(writeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if !u.FirstTS.IsZero() || !u.LastTS.IsZero() {
		t.Errorf("missing timestamps must yield zero bounds: %v / %v", u.FirstTS, u.LastTS)
	}
}

func TestParseTSGarbage(t *testing.T) {
	for _, s := range []string{"", "not-a-time", "2026-13-99"} {
		if _, ok := parseTS(s); ok {
			t.Errorf("parseTS(%q) should fail", s)
		}
	}
	if _, ok := parseTS("2026-07-06T10:00:00Z"); !ok {
		t.Error("parseTS should accept RFC3339")
	}
}
