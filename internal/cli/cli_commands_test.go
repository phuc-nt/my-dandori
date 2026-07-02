package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// execCLI runs the root command against a temp DB and captures stdout.
// Cobra keeps flag state between calls; every invocation passes --db
// explicitly so tests stay independent.
func execCLI(t *testing.T, db string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	// --db must come BEFORE the command args: anything after a `--` separator
	// (wrap) belongs to the wrapped command, not to dandori.
	rootCmd.SetArgs(append([]string{"--db", db}, args...))
	err := rootCmd.Execute()
	return buf.String(), err
}

func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "cli.db")
}

func TestCLIDemoSeedAndReadCommands(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "demo-seed"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"stats"}, {"leaderboard"}, {"runs", "-n", "5"}, {"budget"},
		{"approvals", "--pending"}, {"audit", "list"}, {"audit", "verify"},
		{"attribution"},
	} {
		if _, err := execCLI(t, db, args...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	// Data actually landed.
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var runs int
	st.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs)
	if runs == 0 {
		t.Error("demo-seed inserted no runs")
	}
}

func TestCLIKillAndAudit(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "kill", "--all"); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(db)
	defer st.Close()
	if st.Setting("kill_switch_global") != "1" {
		t.Error("kill --all did not set the switch")
	}
	if _, err := execCLI(t, db, "kill", "--off"); err != nil {
		t.Fatal(err)
	}
	// audit verify must pass on the fresh chain the kill commands created.
	if _, err := execCLI(t, db, "audit", "verify"); err != nil {
		t.Errorf("audit verify: %v", err)
	}
	// Reset flags for other tests (cobra persists them).
	flagKillAll, flagKillOff = false, false
}

func TestCLIExportCompliance(t *testing.T) {
	db := tempDB(t)
	out := filepath.Join(t.TempDir(), "bundle.json")
	execCLI(t, db, "demo-seed")
	if _, err := execCLI(t, db, "export", "compliance", "--out", out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"verify"`, `"audit_log"`, `"runs_summary"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("bundle missing %s", want)
		}
	}
	flagExportOut = "" // reset shared flag state
}

func TestCLIGateChecks(t *testing.T) {
	db := tempDB(t)
	if _, err := execCLI(t, db, "gate", "--checks", "true"); err != nil {
		t.Errorf("gate true: %v", err)
	}
	if _, err := execCLI(t, db, "gate", "--checks", "false"); err == nil {
		t.Error("gate false must return an error")
	}
	flagGateChecks = ""
}

func TestCLIUnknownSyncTarget(t *testing.T) {
	if _, err := execCLI(t, tempDB(t), "sync", "nonsense"); err == nil {
		t.Error("invalid sync target must error")
	}
}
