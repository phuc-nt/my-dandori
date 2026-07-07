// Package kitpolicy is the SINGLE SOURCE (H3) of the path-safety rules a
// kind=kit knowledge unit's manifest must obey: which top-level .claude/
// segments are NEVER distributable (deny-list, load-bearing security
// control), which are eligible at all (whitelist), and the lexical shape
// every kept path must have (per-segment charset, depth cap, .md extension).
//
// ValidatePath is LEXICAL ONLY — string/regex checks against a relative path
// string, no filesystem access, no symlink resolution. P4's `kit nominate`
// scan (internal/cli/kit_cmd.go) and P5's pull-side `KitLocalPath` both
// import this package so the two enforcement points can never drift apart;
// a filesystem/symlink-escape layer on top belongs to P5 alone (mirrors
// skillreg.LocalPath's own split between lexical containment and on-disk
// symlink checks).
package kitpolicy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Deny-list (H3, load-bearing): these top-level .claude/ segments can NEVER
// be distributed via a kit — each one is guardrail/hook/settings wiring that
// a self-published kit could use to self-disarm the very guardrails meant to
// contain it (G2/v10 lesson). Checked BEFORE the whitelist (defense in
// depth): even a future loosened whitelist regex cannot resurrect one of
// these segments by accident.
var denySegments = map[string]bool{
	"hooks":         true,
	"scripts":       true,
	"output-styles": true,
}

// denyFilenames blocks Claude Code settings files regardless of which
// directory they appear in — these carry the same self-disarm risk as the
// deny-listed directories above (hook wiring, permission overrides) and are
// filename-based rather than directory-based.
var denyFilenames = map[string]bool{
	"settings.json":       true,
	"settings.local.json": true,
}

// Whitelist: the only top-level .claude/ segments eligible for kit
// distribution at all. Everything else (deny-listed or simply unknown) is
// silently excluded from the kit — not an error, just "not a kit candidate."
var whitelistSegments = map[string]bool{
	"agents":   true,
	"rules":    true,
	"skills":   true,
	"commands": true,
}

// MaxDepth caps path segment depth (".claude/<top>/.../file.md") at 4 —
// generous for a real skill dir (skills/<name>/SKILL.md is depth 3) while
// bounding how deep a manifest path can nest.
const MaxDepth = 4

// segmentRe matches one path segment: must not be empty, must not be "." or
// "..", and is restricted to a safe charset (letters, digits, dash,
// underscore, dot) — blocks traversal segments and shell-hostile characters
// without needing filesystem access to prove it.
var segmentRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ErrDenied marks a deny-first hit (H3): the whole nominate must ABORT, not
// skip just this file — the caller names the offending path in its own
// error/banner text.
var ErrDenied = errors.New("path is in the kit deny-list — guardrail/hook/settings wiring is never distributable")

// ErrNotWhitelisted marks a path whose top-level segment is not one of the
// eligible kit directories — the caller treats this as "not a kit
// candidate," silently skipped, never an abort.
var ErrNotWhitelisted = errors.New("path is not under a whitelisted kit directory")

// ErrInvalidSegment marks a per-segment regex/traversal failure.
var ErrInvalidSegment = errors.New("path contains an invalid segment")

// ErrTooDeep marks a path exceeding MaxDepth segments.
var ErrTooDeep = errors.New("path exceeds max kit depth")

// ErrNonMarkdownInSkill is the H4 sentinel: a file lives under a whitelisted
// directory, passes every structural check, but is not a .md file (e.g.
// skills/x/run.py, agents/y/icon.png). This is DISTINCT from a hard
// rejection — the caller (kit_cmd.go) treats this as WARN-and-continue: the
// file is excluded from the kit body, the operator is told by name, and the
// kit stays instruction-only rather than the nominate aborting outright.
var ErrNonMarkdownInSkill = errors.New("non-markdown file under a whitelisted kit directory — excluded, kit stays instruction-only")

// ValidateKitPath validates relPath (expected to be repo-relative, forward-
// slash separated — the shape `git ls-files` emits) against the deny-list,
// whitelist, per-segment charset, depth cap, and .md extension, in that
// order (H3: deny-first is defense-in-depth even against a future loosened
// whitelist).
//
// Returns nil only for a path that should be KEPT in the kit body.
// Returns ErrDenied for a deny-list hit — caller MUST abort the whole
// nominate naming this path.
// Returns ErrNotWhitelisted for a path outside the eligible directories —
// caller silently skips (not a kit candidate).
// Returns ErrInvalidSegment / ErrTooDeep for a structurally malformed path
// under a whitelisted directory — caller treats as a hard reject naming the
// file (same abort-and-name behavior as deny, since a whitelisted dir with a
// malformed path is unexpected enough to warrant stopping, not silent skip).
// Returns ErrNonMarkdownInSkill (H4) for a non-.md file that otherwise
// passes every structural check under a whitelisted dir — caller WARNS
// naming the file and continues, excluding it from the kept set.
func ValidateKitPath(relPath string) error {
	slashed := filepathToSlash(relPath)
	if slashed == "" {
		return fmt.Errorf("%w: empty path", ErrInvalidSegment)
	}
	// Validate the RAW (pre-Clean) segments first: path.Clean collapses
	// ".." traversal and empty "//" segments away, which would otherwise let
	// a traversal-shaped input slip past this check disguised as a
	// perfectly normal cleaned path (e.g. "rules/../../../etc/passwd.md"
	// cleans to "../../etc/passwd.md" — informative for a human, but the
	// RAW segments already reveal the ".." shape directly). Every segment
	// (including the deny/whitelist top-level one) must pass the charset
	// check before any deny/whitelist/depth decision is made.
	rawSegments := strings.Split(slashed, "/")
	for _, seg := range rawSegments {
		if seg == "" || seg == "." || seg == ".." || !segmentRe.MatchString(seg) {
			// Deny-first still applies even to a malformed path (H3 doc
			// comment/test): a denied top segment must surface as ErrDenied
			// rather than ErrInvalidSegment, since deny-list membership is
			// the more severe, load-bearing signal.
			if len(rawSegments) > 0 && denySegments[rawSegments[0]] {
				return fmt.Errorf("%w: %q", ErrDenied, relPath)
			}
			return fmt.Errorf("%w: %q (segment %q)", ErrInvalidSegment, relPath, seg)
		}
	}

	segments := rawSegments

	// Deny-first (H3): checked before whitelist logic, so a denied segment
	// anywhere at top level short-circuits everything else, including
	// whitelist membership.
	if denySegments[segments[0]] {
		return fmt.Errorf("%w: %q", ErrDenied, relPath)
	}
	base := segments[len(segments)-1]
	if denyFilenames[base] {
		return fmt.Errorf("%w: %q", ErrDenied, relPath)
	}

	if !whitelistSegments[segments[0]] {
		return fmt.Errorf("%w: %q", ErrNotWhitelisted, relPath)
	}

	// A hidden (dotfile) basename — e.g. "agents/.hidden.md" — is rejected
	// even though segmentRe's charset allows a leading dot (needed for the
	// ".md" extension itself): a dotfile is exactly the shape editors/tools
	// use for config/state files (.env, .DS_Store, .bashrc-style overrides),
	// so a kit distributing one is unexpected enough under a whitelisted
	// dir to warrant a hard reject naming the file, same as ErrTooDeep.
	if strings.HasPrefix(base, ".") {
		return fmt.Errorf("%w: %q (hidden file)", ErrInvalidSegment, relPath)
	}

	if len(segments) > MaxDepth {
		return fmt.Errorf("%w: %q (depth %d > %d)", ErrTooDeep, relPath, len(segments), MaxDepth)
	}

	if !strings.HasSuffix(base, ".md") {
		return fmt.Errorf("%w: %q", ErrNonMarkdownInSkill, relPath)
	}

	return nil
}

// filepathToSlash normalizes any stray backslash separators to forward
// slashes before Clean/Split — defensive only: every real caller today feeds
// paths from `git ls-files` (already forward-slash) on darwin/linux, but
// this keeps the lexical validator's contract explicit rather than assuming
// the OS separator matches "/".
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}
