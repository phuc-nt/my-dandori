package skillreg

import (
	"os"
	"path/filepath"
	"testing"
)

// TestKitLocalPathRejectsUnsafe is the M5 security-contract table test,
// written BEFORE kit pull is wired to any write. It must cover both the
// lexical layer (kitpolicy.ValidateKitPath — deny-list, whitelist, segment
// charset, depth, .md suffix) and the NEW variable-depth symlink-safe
// filesystem walk this package adds on top (KitLocalPath is not a reuse of
// LocalPath's fixed 3-component walk: a kit path can be arbitrary depth, so
// every intermediate directory — not just a hardcoded 3-element slice — must
// be Lstat-ed for a symlink escape).
func TestKitLocalPathRejectsUnsafe(t *testing.T) {
	repo := t.TempDir()

	cases := []string{
		"../x.md",
		"agents/../hooks/x.md",
		"hooks/a.md",
		"scripts/b.md",
		"settings.json",
		"agents/.hidden.md",
		"agents/a.sh",    // non-md
		"a/b/c/d/e.md",   // depth 5 > MaxDepth(4)
		"/etc/passwd.md", // absolute
	}
	for _, rel := range cases {
		if _, err := KitLocalPath(repo, rel); err == nil {
			t.Errorf("KitLocalPath(%q): expected rejection, got nil error", rel)
		}
	}
}

// TestKitLocalPathSymlinkedRootRejected covers a symlinked WHITELIST ROOT
// (.claude/agents itself is a symlink escaping the repo) — the baseline
// symlink-escape case LocalPath already defends for skills.
func TestKitLocalPathSymlinkedRootRejected(t *testing.T) {
	repo := t.TempDir()
	claudeDir := filepath.Join(repo, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	agentsDir := filepath.Join(claudeDir, "agents")
	if err := os.Symlink(outside, agentsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := KitLocalPath(repo, "agents/x.md"); err == nil {
		t.Fatal("KitLocalPath: expected refusal for symlinked agents/ root escaping repo")
	}
}

// TestKitLocalPathSymlinkedDeepIntermediateRejected is the M5 case: the real
// attack is a symlinked DEEP intermediate directory, not just a symlinked
// whitelist root. skills/z is itself a symlink escaping the repo, and the
// manifest path descends further below it (skills/z/references/a.md) — a
// fixed 3-component walk (like LocalPath's) would never even look at
// "skills/z" as a distinct component from "skills/z/references", so this
// test would falsely pass under a naive reuse of LocalPath's walk.
func TestKitLocalPathSymlinkedDeepIntermediateRejected(t *testing.T) {
	repo := t.TempDir()
	skillsDir := filepath.Join(repo, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	// "z" is a symlink pointing outside the repo; the manifest path drills
	// further below it (references/a.md) — a walk that only checks whether
	// "skills" or the final target is a symlink (and not every intermediate
	// component in between) would miss this.
	zDir := filepath.Join(skillsDir, "z")
	if err := os.Symlink(outside, zDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "references", "a.md"), []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := KitLocalPath(repo, "skills/z/references/a.md"); err == nil {
		t.Fatal("KitLocalPath: expected refusal for symlinked DEEP intermediate dir (skills/z) escaping repo")
	}
}

// TestKitLocalPathTargetSymlinkRejected covers the target file ITSELF being
// a symlink (re-pull over an attacker-planted symlink) — refused even though
// every ancestor directory is a normal directory.
func TestKitLocalPathTargetSymlinkRejected(t *testing.T) {
	repo := t.TempDir()
	agentsDir := filepath.Join(repo, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "evil.md")
	if err := os.WriteFile(outsideFile, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(agentsDir, "x.md")
	if err := os.Symlink(outsideFile, target); err != nil {
		t.Fatal(err)
	}
	if _, err := KitLocalPath(repo, "agents/x.md"); err == nil {
		t.Fatal("KitLocalPath: expected refusal — target file itself is a symlink")
	}
}

// TestKitLocalPathAcceptsWhitelisted proves the happy path: whitelisted,
// well-formed, non-symlinked paths at varying depth resolve under
// realRoot/.claude/<...> and Write can create them.
func TestKitLocalPathAcceptsWhitelisted(t *testing.T) {
	repo := t.TempDir()
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("EvalSymlinks(repo): %v", err)
	}

	cases := map[string]string{
		"agents/x.md":              filepath.Join(realRepo, ".claude", "agents", "x.md"),
		"rules/y.md":               filepath.Join(realRepo, ".claude", "rules", "y.md"),
		"skills/z/references/a.md": filepath.Join(realRepo, ".claude", "skills", "z", "references", "a.md"),
	}
	for rel, want := range cases {
		got, err := KitLocalPath(repo, rel)
		if err != nil {
			t.Fatalf("KitLocalPath(%q): unexpected error: %v", rel, err)
		}
		if got != want {
			t.Errorf("KitLocalPath(%q) = %s, want %s", rel, got, want)
		}
		if err := Write(got, "body"); err != nil {
			t.Fatalf("Write(%q): %v", rel, err)
		}
		b, err := os.ReadFile(got)
		if err != nil || string(b) != "body" {
			t.Fatalf("read back %q: %v %q", rel, err, b)
		}
	}
}

// TestKitLocalPathRejectsBackslashTraversal is a defense-in-depth check
// mirroring kitpolicy's own normalization: a relPath using OS-foreign
// backslash separators must not be usable to smuggle a traversal segment
// past the lexical layer.
func TestKitLocalPathRejectsBackslashTraversal(t *testing.T) {
	repo := t.TempDir()
	if _, err := KitLocalPath(repo, "agents\\..\\..\\etc\\passwd.md"); err == nil {
		t.Fatal("KitLocalPath: expected rejection for backslash-traversal path")
	}
}

// TestKitLocalPathDenyListWinsOverWidenedWhitelist documents H3: even if a
// path's shape otherwise looks acceptable, a deny-listed top segment must
// never be reachable through KitLocalPath, independent of any future
// whitelist change — this is exercised through the same kitpolicy single
// source kit nominate uses (H3), not a duplicated check here.
func TestKitLocalPathDenyListWinsOverWidenedWhitelist(t *testing.T) {
	repo := t.TempDir()
	for _, rel := range []string{"hooks/pre.md", "scripts/run.md", "output-styles/x.md"} {
		if _, err := KitLocalPath(repo, rel); err == nil {
			t.Errorf("KitLocalPath(%q): expected deny-list rejection", rel)
		}
	}
}

func TestKitLocalPathEmptyOrDotSegmentsRejected(t *testing.T) {
	repo := t.TempDir()
	for _, rel := range []string{"", "agents/./x.md", "agents//x.md", "."} {
		if _, err := KitLocalPath(repo, rel); err == nil {
			t.Errorf("KitLocalPath(%q): expected rejection", rel)
		}
	}
}
