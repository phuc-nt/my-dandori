package capture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A run whose commit gets `git revert`ed must gain a revert_detected event,
// and acceptance math downstream treats its edits as rejected.
func TestScanRevertsMapsToRun(t *testing.T) {
	g := testIngestor(t)
	dir := gitRepo(t)

	headBefore := strings.TrimSpace(gitRaw(t, dir, "rev-parse", "HEAD"))
	// "Agent session": commit a change.
	os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("new\n"), 0o644)
	commitAll(t, dir, "agent work")
	headAfter := strings.TrimSpace(gitRaw(t, dir, "rev-parse", "HEAD"))

	if _, err := g.EnsureRun("rev-run", dir, "", "hook"); err != nil {
		t.Fatal(err)
	}
	g.St.DB.Exec(`UPDATE runs SET head_before = ?, head_after = ? WHERE id = 'rev-run'`, headBefore, headAfter)

	// Human reverts the agent's commit.
	gitRaw(t, dir, "revert", "--no-edit", headAfter)

	n, err := g.ScanReverts(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("detections: %d, want 1", n)
	}
	var events int
	g.St.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='rev-run' AND kind='revert_detected'`).Scan(&events)
	if events != 1 {
		t.Errorf("revert_detected events: %d", events)
	}
	// Idempotent: second scan adds nothing.
	if n, _ := g.ScanReverts(dir); n != 0 {
		t.Errorf("rescan detections: %d, want 0 (dedup)", n)
	}
}

func gitRaw(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}
