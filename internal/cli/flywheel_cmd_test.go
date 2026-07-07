package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// captureStdout redirects the real os.Stdout for the duration of fn — needed
// because flywheelAdoptionCmd (like the rest of this file) writes with plain
// fmt.Println/Printf, not cmd.Println, so execCLI's cobra-buffer capture
// never sees this command's output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestFlywheelAdoptionAppendsRegressionCaveat covers M4 (F10): the private
// coaching output for `dandori flywheel adoption` must always carry the
// regression-to-mean caveat, since a single before/after delta can move
// either direction on its own — never a causal conclusion by itself.
func TestFlywheelAdoptionAppendsRegressionCaveat(t *testing.T) {
	db := tempDB(t)
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('agent-a', 'agent-a', ?)`, store.Now()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO runs(id, agent_id, project, status, started_at, ended_at)
		VALUES('gold', 'agent-a', 'proj', 'done', ?, ?)`, store.Now(), store.Now()); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	res, err := st.DB.Exec(`INSERT INTO playbooks(name, run_id, agent_id, created_at, created_by)
		VALUES('Pattern: agent-a', 'gold', 'agent-a', ?, 'phucnt')`, store.Now())
	if err != nil {
		t.Fatalf("seed playbook: %v", err)
	}
	pbID, _ := res.LastInsertId()
	if _, err := learn.RecordAdoption(st, pbID, "bob@dev", "", 30); err != nil {
		t.Fatalf("RecordAdoption: %v", err)
	}
	st.Close()

	out := captureStdout(t, func() {
		if _, err := execCLI(t, db, "flywheel", "adoption", "1"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, learn.RegressionToMeanCaveat) {
		t.Errorf("flywheel adoption output must carry the F10 regression-to-mean caveat, got: %q", out)
	}
}
