// KitLocalPath is the kit-pull (P5) counterpart to LocalPath (P3/skill-pull):
// it resolves a manifest-relative path from a kind=kit unit into a
// symlink-safe on-disk target under repoRoot/.claude/. It deliberately does
// NOT reuse LocalPath's walk — LocalPath Lstats a hardcoded 3-element slice
// (.claude, .claude/skills, .claude/skills/<name>) because a skill's target
// is always exactly that shape. A kit manifest path is arbitrary depth
// (e.g. skills/z/references/a.md is 4 segments under .claude), so the real
// attack (M5) is a symlinked INTERMEDIATE directory partway down the path —
// a fixed 3-component check would never even inspect a component like
// "skills/z" as distinct from "skills/z/references". KitLocalPath walks
// EVERY existing intermediate component instead.
package skillreg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/phuc-nt/dandori/internal/kitpolicy"
)

// KitLocalPath validates relPath (a kind=kit manifest entry's Path field,
// expected forward-slash-separated and repo-relative — the shape
// kitpolicy.ValidateKitPath and `git ls-files` both use) and returns the
// symlink-safe absolute path it should be written to under
// repoRoot/.claude/.
//
// Two layers, in order:
//  1. Lexical (kitpolicy.ValidateKitPath): deny-list (hooks/scripts/
//     output-styles/settings*.json) checked before whitelist (agents/rules/
//     skills/commands), per-segment charset, depth cap, .md suffix. This is
//     the SAME single source (H3) `kit nominate` uses, so the two
//     enforcement points can never drift apart.
//  2. Filesystem (M5, new here): EvalSymlinks(repoRoot) establishes the
//     resolved boundary realRoot/.claude. Every path component from
//     realRoot/.claude down through every intermediate directory to the
//     target's parent is Lstat-ed if it exists; any component that is a
//     symlink must resolve to somewhere still inside the boundary. A target
//     file that already exists AND is itself a symlink is refused outright
//     (no re-pull through an attacker-planted symlink).
func KitLocalPath(repoRoot, relPath string) (string, error) {
	if err := kitpolicy.ValidateKitPath(relPath); err != nil {
		return "", fmt.Errorf("path safety: %w", err)
	}

	// ValidateKitPath already rejected backslash-shaped traversal via its own
	// raw-segment charset check (backslash is not in segmentRe's charset),
	// but normalize defensively before deriving filesystem components so a
	// caller on a separator-sensitive OS still gets consistent segments.
	slashed := strings.ReplaceAll(relPath, "\\", "/")
	segments := strings.Split(slashed, "/")

	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	realRoot, err := realOrSelf(root)
	if err != nil {
		return "", err
	}
	boundary := filepath.Clean(filepath.Join(realRoot, ".claude"))

	// Build the full component chain: .claude, .claude/<seg0>,
	// .claude/<seg0>/<seg1>, ... down to the full target path (variable
	// depth — NOT a fixed 3-element slice).
	current := boundary
	if !isWithin(boundary, current) {
		return "", fmt.Errorf("refusing to write outside %s", boundary)
	}
	components := []string{current}
	for _, seg := range segments {
		current = filepath.Join(current, seg)
		if !isWithin(boundary, current) {
			return "", fmt.Errorf("refusing to write outside %s", boundary)
		}
		components = append(components, current)
	}
	target := current

	// M5: Lstat EVERY existing component from .claude down through every
	// intermediate directory to the target — a symlinked component partway
	// down (not just a symlinked whitelist root) must still resolve inside
	// boundary.
	for _, comp := range components {
		fi, statErr := os.Lstat(comp)
		if statErr != nil {
			continue // does not exist yet — nothing to check, neither do deeper components under it
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			continue
		}
		resolved, evalErr := filepath.EvalSymlinks(comp)
		if evalErr != nil {
			return "", fmt.Errorf("resolving %s: %w", comp, evalErr)
		}
		if !isWithin(boundary, resolved) {
			return "", fmt.Errorf("refusing to write through symlink: %s escapes %s", comp, boundary)
		}
	}

	// If the target file itself already exists (re-pull / update), refuse
	// writing through it if IT is a symlink — mirrors LocalPath's own final
	// check.
	if fi, err := os.Lstat(target); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to write through symlink: %s", target)
		}
	}

	return target, nil
}
