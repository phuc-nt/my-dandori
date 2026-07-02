package capture

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644)
	run("add", "-A")
	run("commit", "-m", "init")
	return dir
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func TestSessionDeltaCommitted(t *testing.T) {
	dir := gitRepo(t)
	before := SnapshotGit(dir)
	if !before.IsRepo {
		t.Fatal("repo not detected")
	}
	// Session adds 3 lines committed + 1 line dirty.
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x\ny\nz\n"), 0o644)
	commitAll(t, dir, "work")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644) // +1 dirty
	after := SnapshotGit(dir)

	added, deleted := SessionDelta(dir, before, after)
	if added != 4 || deleted != 0 {
		t.Errorf("delta: +%d/-%d, want +4/-0", added, deleted)
	}
}

func TestSnapshotNonRepo(t *testing.T) {
	if gs := SnapshotGit(t.TempDir()); gs.IsRepo {
		t.Error("non-repo must report IsRepo=false")
	}
}
