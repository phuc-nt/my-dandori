package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// execCLIIn runs the root command with stdin content (hook commands).
func execCLIIn(t *testing.T, db, stdin string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(append([]string{"--db", db}, args...))
	err := rootCmd.Execute()
	return buf.String(), err
}

func TestCLIHookCycleViaStdin(t *testing.T) {
	db := tempDB(t)
	hookJSON := `{"session_id":"cli-h1","cwd":"/work/p","tool_name":"Bash","tool_input":{"command":"ls"}}`
	for _, event := range []string{"session-start", "pre-tool", "post-tool", "stop"} {
		if _, err := execCLIIn(t, db, hookJSON, "hook", event); err != nil {
			t.Fatalf("hook %s: %v", event, err)
		}
	}
	st, _ := store.Open(db)
	defer st.Close()
	var status string
	st.DB.QueryRow(`SELECT status FROM runs WHERE session_id='cli-h1'`).Scan(&status)
	if status != "done" {
		t.Errorf("run status: %q", status)
	}
	// Invalid event name must be rejected by arg validation.
	if _, err := execCLIIn(t, db, "{}", "hook", "bogus-event"); err == nil {
		t.Error("invalid hook event must error")
	}
	// Garbage stdin must NOT fail the session (fail-open capture).
	if _, err := execCLIIn(t, db, "not-json", "hook", "pre-tool"); err != nil {
		t.Errorf("bad stdin must fail open: %v", err)
	}
}

func TestCLIWrapSuccessPath(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "wrap", "--runtime", "generic", "--agent", "w-agent", "--task", "SCRUM-7",
		"--", "true"); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(db)
	defer st.Close()
	var agent, task, runtime, status string
	st.DB.QueryRow(`SELECT agent_id, task_key, runtime, status FROM runs ORDER BY started_at DESC LIMIT 1`).
		Scan(&agent, &task, &runtime, &status)
	if agent != "w-agent" || task != "SCRUM-7" || runtime != "generic" || status != "done" {
		t.Errorf("wrap run: agent=%s task=%s runtime=%s status=%s", agent, task, runtime, status)
	}
	flagWrapAgent, flagWrapRuntime, flagWrapTask = "", "", ""
}

func TestCLIWrapRefusesUnderKill(t *testing.T) {
	db := tempDB(t)
	execCLI(t, db, "kill", "--all")
	if _, err := execCLI(t, db, "wrap", "--runtime", "generic", "--", "true"); err == nil {
		t.Error("wrap must refuse under global kill")
	}
	execCLI(t, db, "kill", "--off")
	flagKillAll, flagKillOff = false, false
}

func TestCLIWatchAndSyncReverts(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "watch"); err != nil {
		t.Errorf("watch: %v", err)
	}
	// Revert scan on a non-git dir: silent no-op, not an error.
	if _, err := execCLI(t, db, "sync", "reverts", "--dir", t.TempDir()); err != nil {
		t.Errorf("sync reverts non-git: %v", err)
	}
	flagSyncDir = ""
}

func TestCLIMissingCredsErrors(t *testing.T) {
	db := tempDB(t)
	t.Setenv("ATLASSIAN_SITE_NAME", "")
	t.Setenv("ATLASSIAN_API_TOKEN", "")
	if _, err := execCLI(t, db, "report", "confluence"); err == nil {
		t.Error("report without creds must error")
	}
	if _, err := execCLI(t, db, "sync", "jira"); err == nil {
		t.Error("jira sync without creds must error")
	}
}

func TestCLIInitInstallsHooks(t *testing.T) {
	db := tempDB(t)
	project := t.TempDir()
	if _, err := execCLI(t, db, "init", "--project", project, "--agent", "ag"); err != nil {
		t.Fatal(err)
	}
	if _, err := filepathAbsCheck(project); err != nil {
		t.Fatal(err)
	}
	flagInitProject, flagInitAgent = "", ""
}

func filepathAbsCheck(project string) (string, error) {
	return filepath.Abs(filepath.Join(project, ".claude", "settings.json"))
}
