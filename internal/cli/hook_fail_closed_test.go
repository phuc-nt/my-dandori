package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// brokenDBPath returns a --db value that makes store.Open fail deterministically:
// a plain file sits where a parent directory needs to be created, so
// os.MkdirAll (called at the top of store.Open) errors with ENOTDIR. This
// exercises the real store-open failure path, not a mock.
func brokenDBPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(blocker, "sub", "cli.db")
}

// runHookCapture runs `dandori hook <event>` with the given stdin, capturing
// real os.Stdout — printVerdict writes with plain fmt.Println (like the rest
// of this package's direct-output commands), not cmd.Println, so execCLIIn's
// cobra buffer never sees it.
func runHookCapture(t *testing.T, db, stdin, event string) (stdout string, cmdErr error) {
	t.Helper()
	stdout = captureStdout(t, func() {
		cmdErr = func() error {
			_, err := execCLIIn(t, db, stdin, "hook", event)
			return err
		}()
	})
	return stdout, cmdErr
}

// TestHookPreToolStoreOpenFailDeniesMutating: openStore failing on the
// pre-tool event must deny a mutating tool (Bash) — decision JSON on stdout —
// instead of the old logAndAllow-everything behavior.
func TestHookPreToolStoreOpenFailDeniesMutating(t *testing.T) {
	db := brokenDBPath(t)
	hookJSON := `{"session_id":"fc-1","cwd":"/work/p","tool_name":"Bash","tool_input":{"command":"ls"}}`
	out, err := runHookCapture(t, db, hookJSON, "pre-tool")
	if err != nil {
		t.Fatalf("hook must still exit 0 (deny is communicated via stdout JSON, not a command error): %v", err)
	}
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny decision JSON on stdout, got: %q", out)
	}
	if !strings.Contains(out, "engine không khả dụng") {
		t.Errorf("expected fail-closed reason in output, got: %q", out)
	}
}

// TestHookPreToolStoreOpenFailAllowsReadOnly: same broken store, but a
// read-only tool (Read) must pass silently (no stdout JSON) — the deny is
// mutating-only, never a blanket block.
func TestHookPreToolStoreOpenFailAllowsReadOnly(t *testing.T) {
	db := brokenDBPath(t)
	hookJSON := `{"session_id":"fc-2","cwd":"/work/p","tool_name":"Read","tool_input":{"file_path":"/work/p/a.go"}}`
	out, err := runHookCapture(t, db, hookJSON, "pre-tool")
	if err != nil {
		t.Fatalf("read-only tool call must not error: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected silent allow (no stdout) for read-only tool, got: %q", out)
	}
}

// TestHookPreToolStoreOpenFailBreakGlassAllows: with DANDORI_GOVERN_FAIL_OPEN=1
// set, a broken store must fall back to the old allow-everything behavior
// (exit 0, no stdout) even for a mutating tool.
func TestHookPreToolStoreOpenFailBreakGlassAllows(t *testing.T) {
	t.Setenv("DANDORI_GOVERN_FAIL_OPEN", "1")
	db := brokenDBPath(t)
	hookJSON := `{"session_id":"fc-3","cwd":"/work/p","tool_name":"Bash","tool_input":{"command":"ls"}}`
	out, err := runHookCapture(t, db, hookJSON, "pre-tool")
	if err != nil {
		t.Fatalf("break-glass must allow (exit 0): %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("break-glass must allow silently, got: %q", out)
	}
}

// TestHookNonPreToolEventsStayFailOpenOnStoreError: session-start/post-tool/
// stop are pure capture — a broken store must still exit 0 with no verdict
// JSON for them, unlike pre-tool.
func TestHookNonPreToolEventsStayFailOpenOnStoreError(t *testing.T) {
	db := brokenDBPath(t)
	hookJSON := `{"session_id":"fc-4","cwd":"/work/p","tool_name":"Bash","tool_input":{"command":"ls"}}`
	for _, event := range []string{"session-start", "post-tool", "stop"} {
		out, err := runHookCapture(t, db, hookJSON, event)
		if err != nil {
			t.Errorf("%s must stay fail-open (exit 0): %v", event, err)
		}
		if strings.TrimSpace(out) != "" {
			t.Errorf("%s must produce no verdict JSON, got: %q", event, out)
		}
	}
}

// TestHookPreToolHappyPathUnchanged: with a working store, a plain read
// (e.g. "ls") on an unrestricted agent must still allow silently — proves
// the verdict/capture split in runPreTool didn't regress the normal path.
func TestHookPreToolHappyPathUnchanged(t *testing.T) {
	db := tempDB(t)
	hookJSON := `{"session_id":"fc-5","cwd":"/work/p","tool_name":"Bash","tool_input":{"command":"ls"}}`
	for _, event := range []string{"session-start", "pre-tool", "post-tool", "stop"} {
		out, err := runHookCapture(t, db, hookJSON, event)
		if err != nil {
			t.Fatalf("hook %s: %v", event, err)
		}
		if event == "pre-tool" && strings.TrimSpace(out) != "" {
			t.Errorf("happy path pre-tool must allow silently, got: %q", out)
		}
	}
}

// TestDenyMutatingOrAllow unit-tests the shared fallback helper directly:
// mutating tools deny with the given reason (printed as verdict JSON),
// read-only tools allow silently (no output, no error). This is the
// coverage boundary for scenarios too invasive to simulate via a real store
// failure (e.g. pre-tool-ingest failing after the store is already open —
// see report for the accepted gap).
func TestDenyMutatingOrAllow(t *testing.T) {
	cases := []struct {
		tool     string
		wantDeny bool
	}{
		{"Bash", true},
		{"Write", true},
		{"Edit", true},
		{"NotebookEdit", true},
		{"Read", false},
		{"Grep", false},
		{"Glob", false},
	}
	for _, c := range cases {
		var callErr error
		out := captureStdout(t, func() {
			callErr = denyMutatingOrAllow(c.tool, "test-reason")
		})
		if callErr != nil {
			t.Errorf("%s: denyMutatingOrAllow must never return a Go error, got %v", c.tool, callErr)
		}
		gotDeny := strings.Contains(out, `"permissionDecision":"deny"`)
		if gotDeny != c.wantDeny {
			t.Errorf("%s: deny=%v, want %v (output: %q)", c.tool, gotDeny, c.wantDeny, out)
		}
		if c.wantDeny && !strings.Contains(out, "test-reason") {
			t.Errorf("%s: expected reason in output, got: %q", c.tool, out)
		}
	}
}
