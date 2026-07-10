package govern

import (
	"crypto/ed25519"
	"os/exec"
	"path/filepath"
	"testing"
)

// execLookPathGit reports whether a git binary is available, so the
// git-commit test can skip cleanly in a CI/sandbox image without one.
func execLookPathGit() (string, error) {
	return exec.LookPath("git")
}

// runGit runs a git command in dir and fails the test on error, returning
// stdout+stderr combined (trimmed of nothing — callers just check non-empty).
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestGitCommitCheckpointBestEffort: in a temp dir that IS a git repo,
// writing a checkpoint produces a commit; in a non-git dir, it degrades
// gracefully with no error. Proves the git-anchor automation actually runs
// without being the thing that fails a checkpoint write.
func TestGitCommitCheckpointBestEffort(t *testing.T) {
	if _, err := execLookPathGit(); err != nil {
		t.Skip("git not available in test environment")
	}

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	t.Run("git repo gets a commit", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@example.com")
		runGit(t, dir, "config", "user.name", "test")

		if err := WriteCheckpoint(priv, dir, "", 1, "hash-1", "2026-01-01T00:00:00Z", 1); err != nil {
			t.Fatalf("write checkpoint: %v", err)
		}

		out := runGit(t, dir, "log", "--oneline")
		if out == "" {
			t.Error("expected a git commit after WriteCheckpoint in a git repo, got none")
		}
	})

	t.Run("non-git dir degrades gracefully", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "not-a-repo")
		if err := WriteCheckpoint(priv, dir, "", 1, "hash-1", "2026-01-01T00:00:00Z", 1); err != nil {
			t.Fatalf("WriteCheckpoint in non-git dir should not error, got: %v", err)
		}
		if _, ok, err := LatestCheckpoint(dir); err != nil || !ok {
			t.Fatalf("checkpoint file itself should still be written: ok=%v err=%v", ok, err)
		}
	})
}
