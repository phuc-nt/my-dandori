package capture

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
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

func TestGitBranchAndCommitMessages(t *testing.T) {
	dir := gitRepo(t)
	if b := GitBranch(dir); b != "main" {
		t.Errorf("branch = %q, want main", b)
	}
	before := SnapshotGit(dir).Head
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("z\n"), 0o644)
	commitAll(t, dir, "fix SCRUM-15 thing")
	after := SnapshotGit(dir).Head

	msgs := CommitMessages(dir, before, after)
	found := false
	for _, m := range msgs {
		if m == "fix SCRUM-15 thing" {
			found = true
		}
	}
	if !found {
		t.Errorf("commit message not returned: %v", msgs)
	}
	if CommitMessages(dir, "", after) != nil {
		t.Error("empty before must yield nil")
	}
	if CommitMessages(dir, after, after) != nil {
		t.Error("same before/after must yield nil")
	}
}

// TestHookPathRecordsLines proves the SessionStart→edit→Stop hook cycle
// records a non-zero code delta. The 0-lines seen in production is a hook
// INSTALL gap (SessionStart never fired → head_before NULL), not a bug in
// this path — this test locks the path itself as correct.
func TestHookPathRecordsLines(t *testing.T) {
	g := testIngestor(t)
	dir := gitRepo(t)
	transcript := writeFixture(t)
	in := hookIn("hooklines", transcript)
	in.CWD = dir

	if err := g.SessionStart(in); err != nil { // snapshots head_before
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package x\n\nvar a = 1\n"), 0o644)
	commitAll(t, dir, "add code")
	if err := g.Stop(in); err != nil { // records delta
		t.Fatal(err)
	}
	var added int
	g.St.DB.QueryRow(`SELECT lines_added FROM runs WHERE id='hooklines'`).Scan(&added)
	if added <= 0 {
		t.Errorf("hook path recorded no lines: +%d (path is broken)", added)
	}
}

// TestWatcherPathRecordsLines proves a watcher-sourced run gets a git delta:
// snapshot on first sight, delta at stale-close.
func TestWatcherPathRecordsLines(t *testing.T) {
	g := testIngestor(t)
	dir := gitRepo(t)
	transcript := filepath.Join(dir, "wlines.jsonl")
	// Transcript cwd must be the repo so snapshot/delta run against it.
	line := `{"type":"user","cwd":"` + dir + `","timestamp":"2026-07-06T10:00:00.000Z","message":{"role":"user","content":"start"}}
{"type":"assistant","timestamp":"2026-07-06T10:03:00.000Z","message":{"id":"m1","model":"claude-sonnet-5","usage":{"input_tokens":10,"output_tokens":5}}}
`
	os.WriteFile(transcript, []byte(line), 0o644)

	// First sight (not stale) — snapshots head_before.
	if _, err := g.ingestTranscript(transcript, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Session does work.
	os.WriteFile(filepath.Join(dir, "impl.go"), []byte("package x\n\nfunc F() {}\n"), 0o644)
	commitAll(t, dir, "impl")
	// Second sight, stale — records delta at close.
	stale := time.Now().Add(-2 * staleAfter)
	if _, err := g.ingestTranscript(transcript, stale); err != nil {
		t.Fatal(err)
	}
	var added int
	g.St.DB.QueryRow(`SELECT lines_added FROM runs WHERE transcript_path=?`, transcript).Scan(&added)
	if added <= 0 {
		t.Errorf("watcher path recorded no lines: +%d", added)
	}
}
