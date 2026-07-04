package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

func testLauncher(t *testing.T, projectsDir string) *Launcher {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{ProjectsDir: projectsDir, MaxConcurrentLaunches: 2,
		AgentBinaries: map[string]string{"claude": "/bin/echo"}}
	return New(cfg, st, &capture.Ingestor{Cfg: cfg, St: st})
}

// The core RCE defense: a prompt that looks like a dangerous flag must become
// the VALUE of -p (one argv element), never a flag of the agent.
func TestArgvFlagInjectionInert(t *testing.T) {
	evil := "--dangerously-bypass-approvals-and-sandbox"
	argv, err := argvFor("claude", "/abs/claude", evil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/abs/claude", "-p", evil}
	if len(argv) != 3 || argv[0] != want[0] || argv[1] != "-p" || argv[2] != evil {
		t.Fatalf("argv = %v, want prompt as -p VALUE %v", argv, want)
	}
	// The prompt is exactly argv[2] — nowhere it could be parsed as a flag.
	if argv[2] != evil {
		t.Error("prompt not isolated as flag value")
	}
}

func TestArgvRejectsNonClaude(t *testing.T) {
	for _, agent := range []string{"codex", "aider", "bash", ""} {
		if _, err := argvFor(agent, "/abs/x", "hi"); err == nil {
			t.Errorf("agent %q should be rejected (v6 = claude only)", agent)
		}
	}
}

func TestResolveAgentAllowlistOnly(t *testing.T) {
	l := testLauncher(t, t.TempDir())
	// Allowlisted + exists (/bin/echo) → ok.
	if _, err := l.resolveAgent("claude"); err != nil {
		t.Errorf("allowlisted claude: %v", err)
	}
	// Not in allowlist → error (never $PATH-resolved).
	if _, err := l.resolveAgent("codex"); err == nil {
		t.Error("non-allowlisted agent must error")
	}
	// Allowlisted but missing binary → error.
	l.Cfg.AgentBinaries["ghost"] = "/nonexistent/binary"
	if _, err := l.resolveAgent("ghost"); err == nil {
		t.Error("missing binary must error")
	}
}

func TestValidateWorkDirBoundary(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	os.MkdirAll(filepath.Join(proj, "sub"), 0o755)
	os.MkdirAll(base+"/proj-evil", 0o755)

	cases := []struct {
		cwd string
		ok  bool
	}{
		{proj, true},
		{filepath.Join(proj, "sub"), true},
		{base + "/proj-evil", false},       // sibling prefix attack
		{filepath.Join(proj, ".."), false}, // traversal out
		{"/etc", false},
	}
	for _, c := range cases {
		err := validateWorkDir(c.cwd, proj)
		if (err == nil) != c.ok {
			t.Errorf("validateWorkDir(%q) ok=%v, want %v (err=%v)", c.cwd, err == nil, c.ok, err)
		}
	}
}

func TestLaunchRefusedByKillSwitch(t *testing.T) {
	proj := t.TempDir()
	l := testLauncher(t, proj)
	l.St.SetSetting("kill_switch_global", "1")
	if _, err := l.Launch("claude", "hi", proj, "phuc@console", ""); err != errRefused {
		t.Errorf("kill switch ON: err=%v, want errRefused", err)
	}
}

// A real async launch of /bin/echo → run row gets pid, streams output, and
// finalizes to done (no stuck 'running').
func TestLaunchEchoLifecycle(t *testing.T) {
	proj := t.TempDir()
	l := testLauncher(t, proj)
	l.Cfg.AgentBinaries["claude"] = "/bin/echo" // echo -p "<prompt>" prints the prompt

	runID, err := l.Launch("claude", "xin chào dandori", proj, "phuc@console", "")
	if err != nil {
		t.Fatal(err)
	}
	// pid recorded immediately.
	var pid, launchedBy string
	l.St.DB.QueryRow(`SELECT COALESCE(pid,''), COALESCE(launched_by,'') FROM runs WHERE id=?`, runID).Scan(&pid, &launchedBy)
	if pid == "" || launchedBy != "phuc@console" {
		t.Errorf("pid=%q launched_by=%q", pid, launchedBy)
	}
	// Wait for the reaper to finalize.
	deadline := time.Now().Add(5 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		l.St.DB.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&status)
		if status == "done" || status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != "done" {
		t.Fatalf("run status=%q, want done (no stuck running)", status)
	}
	// echo printed the prompt to stdout → captured as a run_stdout event.
	var n int
	l.St.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id=? AND kind='run_stdout'`, runID).Scan(&n)
	if n == 0 {
		t.Error("no run_stdout events captured")
	}
	// launch_prompt event stored for retry prefill.
	var prompt string
	l.St.DB.QueryRow(`SELECT payload FROM events WHERE run_id=? AND kind='launch_prompt'`, runID).Scan(&prompt)
	if !strings.Contains(prompt, "xin chào") {
		t.Errorf("launch_prompt not stored: %q", prompt)
	}
	// Registry cleaned up after exit.
	if _, ok := l.Reg.Get(runID); ok {
		t.Error("registry entry not removed after exit")
	}
}

// A StartRun failure after semaphore acquire must restore the slot.
func TestSemaphoreNoLeakOnStartRunFailure(t *testing.T) {
	proj := t.TempDir()
	l := testLauncher(t, proj)
	// Close the DB so StartRun fails.
	l.St.Close()
	_, err := l.Launch("claude", "hi", proj, "phuc@console", "")
	if err == nil {
		t.Fatal("expected StartRun failure")
	}
	// Both slots must still be free (cap=2): acquire both.
	if !l.sem.TryAcquire(2) {
		t.Error("semaphore leaked a slot on StartRun failure")
	}
}
