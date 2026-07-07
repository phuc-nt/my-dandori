// Package skillreg implements the SECURITY-SENSITIVE pull-only distribution
// surface for skill-kind knowledge units (P5, v12): reading a reviewed,
// hash-pinned skill body out of the local store and writing it into a
// repo-local .claude/skills/<name>/SKILL.md — the only write path Dandori has
// onto an engineer's machine. Threat model: an instruction-like payload
// (skill = prompt/script an agent runs) reaching an engineer's checkout, via
// either a malicious/careless nomination or a DB-writer tampering the row
// after human approval. Mitigations live at three layers: (1) full-body human
// review at approve time (P3, outside this package), (2) hash-pin verified
// against the APPEND-ONLY audit chain rather than the same row being written
// (F7 — Verify), (3) path-safety against traversal and symlink escape (F8 —
// LocalPath/Write). This package never writes to the audit-chain-adjacent
// knowledge_units table itself; RecordUnitAdoption (P6, internal/learn) and
// the audit "skill_pulled" append (CLI layer) are the only side effects of a
// pull.
package skillreg

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// Skill is the pull-facing projection of a published skill-kind knowledge
// unit — deliberately narrower than learn.KnowledgeUnit (only what `skill
// list`/`skill pull` need).
type Skill struct {
	UnitID   int64
	Name     string
	Body     string
	Hash     string // content_hash pinned on the knowledge_units row
	Required bool
}

// ErrNotFound is returned by Get when no published skill unit matches key —
// callers (CLI) turn this into the F3 fail-open message rather than a crash.
var ErrNotFound = errors.New("skill unit not found or not published")

// ErrHashMismatch marks any of the three-way hash check's legs failing (F7).
var ErrHashMismatch = errors.New("hash mismatch — refusing to write")

// nameRe is the F2/F8 slug validator: kebab-case starting with a lowercase
// letter or digit. This blocks both "/" (path traversal via extra segments)
// and ".." (blocked because "." is not in the character class at all).
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidName reports whether name is safe to use as a single path segment
// under .claude/skills/. Same shape as learn.ValidSlug, duplicated here (F8
// is a path-safety concern local to this package, not a knowledge-unit
// naming concern) rather than importing learn's helper as the security
// contract — an unrelated future change to learn.ValidSlug must not silently
// loosen skillreg's own path-safety guarantee.
func ValidName(name string) bool {
	return name != "" && len(name) <= learn.MaxSlugLen && nameRe.MatchString(name)
}

// Published lists all currently-published skill-kind knowledge units.
func Published(st *store.Store) ([]Skill, error) {
	units, err := learn.ListUnits(st, learn.StatePublished)
	if err != nil {
		return nil, err
	}
	out := make([]Skill, 0, len(units))
	for _, u := range units {
		if u.Kind != learn.KindSkill {
			continue
		}
		out = append(out, Skill{UnitID: u.ID, Name: u.Name, Body: u.Body, Hash: u.ContentHash, Required: u.Required})
	}
	return out, nil
}

// Get fetches one published skill unit by name or numeric unit-id. F3:
// returns ErrNotFound (not a crash, not a silently-empty result) when the
// unit does not exist, is not kind=skill, or is not in state=published —
// central mode ("published elsewhere, not in this local store") looks
// identical to "never published" from here, and the CLI surfaces the same
// fail-open message for both per spec.
func Get(st *store.Store, key string) (*Skill, error) {
	var u *learn.KnowledgeUnit
	if id, err := parseUnitID(key); err == nil {
		got, err := learn.GetUnit(st, id)
		if err != nil {
			return nil, err
		}
		u = got
	} else {
		units, err := learn.ListUnits(st, learn.StatePublished)
		if err != nil {
			return nil, err
		}
		for i := range units {
			if units[i].Kind == learn.KindSkill && units[i].Name == key {
				u = &units[i]
				break
			}
		}
	}
	if u == nil || u.Kind != learn.KindSkill || u.State != learn.StatePublished {
		return nil, ErrNotFound
	}
	return &Skill{UnitID: u.ID, Name: u.Name, Body: u.Body, Hash: u.ContentHash, Required: u.Required}, nil
}

func parseUnitID(key string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(key, "%d", &id)
	if err != nil {
		return 0, err
	}
	// Sscanf accepts a numeric prefix ("12abc" -> 12); require the whole
	// string to round-trip so a skill literally named e.g. "12" is looked up
	// by name, and a stray numeric-looking name isn't silently misrouted.
	if fmt.Sprintf("%d", id) != key {
		return 0, fmt.Errorf("not a bare unit id")
	}
	if id <= 0 {
		return 0, fmt.Errorf("not a positive unit id")
	}
	return id, nil
}

// approveHashRe extracts the content_hash the audit detail string records at
// publish time — see observer.applyKnowledgePublish's `detail` format:
// `skill %q v%d published, unit_id=%d, content_hash=%s (insight #%d)`.
// Matching only the hex run keeps this independent of exact surrounding
// wording as long as the content_hash=<hex> token is present.
var approveHashRe = regexp.MustCompile(`content_hash=([0-9a-f]{64})`)

// approveUnitIDRe extracts the unit_id=<n> token H1's fix pins into the audit
// detail at publish time (observer.applyKnowledgePublish). Pre-fix rows
// (published before this change shipped) never carry this token — ApproveHash
// falls back to newest-entry-wins for those, see below.
var approveUnitIDRe = regexp.MustCompile(`unit_id=(\d+)`)

// ApproveHash reads the hash recorded in the audit hash-chain at the moment
// this skill unit was published (action="knowledge_published", subject=
// "skill:<name>") — F7's INDEPENDENT verification source. The audit_log
// table is append-only and each row's hash covers the previous row's hash
// (govern.Audit), so a DB-writer who edits the knowledge_units row (body
// and/or content_hash) after approval cannot also rewrite this historical
// entry without breaking the chain (detectable via govern.Verify).
//
// H1 fix: subject "skill:<name>" is shared across the ENTIRE (kind,name)
// lineage (every version ever published under that name), so scanning for
// "any historical entry whose hash agrees with the row" (the old behavior)
// let a DB-writer who rewrites the row back to an OLDER published version's
// body+hash pass verification — the check degraded from "current approved
// bytes" to "bytes ever approved for this name." The fix holds the row to
// the NEWEST entry belonging to THIS unit id's own lineage: walk newest-first
// and return the first entry whose unit_id token matches unitID. Older
// pre-fix rows that never got a unit_id token (published before this change)
// fall back to newest-entry-wins for the name — the newest audit row overall
// is treated as authoritative for any of them, matching the doc's original
// "latest is authoritative" intent as closely as history allows.
//
// Residual risk (documented, not fixed here — central mode [Sau]): the audit
// chain has no secret; chainHash is computable by anyone with DB write, so a
// DB-writer who understands the audit format can validly APPEND a forged
// knowledge_published entry (or edit one and only be caught if someone runs
// govern.Verify, which the pull path never calls). This still stops the
// tested threat (row-only tamper, no chain-append) but is not proof against a
// chain-aware attacker; before central mode, verification needs an external
// anchor (e.g. a signed head or an external log) — the DB itself is no longer
// a trust boundary once it's remote-writable by more than one operator.
func ApproveHash(st *store.Store, unitID int64) (string, error) {
	u, err := learn.GetUnit(st, unitID)
	if err != nil {
		return "", err
	}
	if u == nil {
		return "", fmt.Errorf("unit %d not found", unitID)
	}
	subject := u.Kind + ":" + u.Name
	rows, err := st.Read().Query(
		`SELECT COALESCE(detail,'') FROM audit_log WHERE action = ? AND subject = ? ORDER BY id DESC`,
		"knowledge_published", subject)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var newestHash string // first (newest) row with a content_hash at all — pre-fix fallback
	haveNewestHash := false
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			return "", err
		}
		hm := approveHashRe.FindStringSubmatch(detail)
		if hm == nil {
			continue
		}
		if !haveNewestHash {
			newestHash = hm[1]
			haveNewestHash = true
		}
		if idm := approveUnitIDRe.FindStringSubmatch(detail); idm != nil {
			var rowUnitID int64
			if _, err := fmt.Sscanf(idm[1], "%d", &rowUnitID); err == nil && rowUnitID == unitID {
				// Newest entry for THIS unit id's own lineage — authoritative,
				// stop scanning (never fall through to an older entry even if
				// it also matches, since this unit id can only ever have one
				// publish record and rows are walked newest-first).
				return hm[1], nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if haveNewestHash {
		// No entry carried a unit_id matching unitID (either this unit
		// predates the unit_id fix, or — defensively — genuinely has none) —
		// fall back to the newest entry overall for this name, never an older
		// one, so a pre-fix lineage still gets "latest is authoritative"
		// rather than "first hash that happens to match."
		return newestHash, nil
	}
	return "", fmt.Errorf("no knowledge_published audit entry found for skill %q (unit %d)", u.Name, unitID)
}

// Verify performs the F7 three-way hash check: sha256(s.Body) == s.Hash ==
// auditHash. Any mismatch is a hard FAIL — this is the load-bearing
// supply-chain check: a DB-writer tampering both the body and the
// content_hash column on the SAME row would pass a naive "recompute and
// compare to the row" check, but still cannot forge the independent
// audit-chain hash without breaking govern.Verify's continuity.
func Verify(s Skill, auditHash string) error {
	sum := sha256.Sum256([]byte(s.Body))
	bodyHash := hex.EncodeToString(sum[:])
	if bodyHash != s.Hash {
		return fmt.Errorf("%w: body hash %s != row content_hash %s", ErrHashMismatch, bodyHash, s.Hash)
	}
	if s.Hash != auditHash {
		return fmt.Errorf("%w: row content_hash %s != audit-approved hash %s", ErrHashMismatch, s.Hash, auditHash)
	}
	if bodyHash != auditHash {
		return fmt.Errorf("%w: body hash %s != audit-approved hash %s", ErrHashMismatch, bodyHash, auditHash)
	}
	return nil
}

// LocalPath resolves the F8 symlink-safe target path for name under
// repoRoot/.claude/skills/. It validates name (ValidName — blocks "/" and
// ".."), Cleans the join, then checks EACH path component from repoRoot
// down (".claude", ".claude/skills", ".claude/skills/<name>") that already
// exists on disk: if it is a symlink, its resolved target must still land
// inside the real (symlink-resolved) repoRoot — refusing otherwise. The
// target file itself need not exist yet (a fresh pull).
//
// repoRoot's OWN ancestors are deliberately NOT symlink-checked — resolving
// repoRoot first (filepath.EvalSymlinks) before deriving the boundary
// absorbs benign ancestor symlinks (e.g. macOS temp dirs: /var is itself a
// symlink to /private/var) without treating them as an escape, while still
// catching the actual threat: a symlink INSIDE the repo tree
// (.claude, .claude/skills, or the per-skill directory) that redirects
// writes outside the repo.
func LocalPath(repoRoot, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("invalid skill name %q — must match ^[a-z0-9][a-z0-9-]*$", name)
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	realRoot, err := realOrSelf(root)
	if err != nil {
		return "", err
	}

	boundary := filepath.Clean(filepath.Join(realRoot, ".claude", "skills"))
	skillDir := filepath.Clean(filepath.Join(boundary, name))
	target := filepath.Join(skillDir, "SKILL.md")

	// Lexical containment check first — catches a name that, despite
	// passing the regex, would still Clean outside boundary under some
	// future looser regex (defense in depth).
	if !isWithin(boundary, skillDir) {
		return "", fmt.Errorf("refusing to write outside %s", boundary)
	}

	// F8: check every component from realRoot down to skillDir — any
	// EXISTING component that is itself a symlink must resolve to somewhere
	// still inside boundary.
	for _, comp := range []string{
		filepath.Join(realRoot, ".claude"),
		boundary,
		skillDir,
	} {
		fi, statErr := os.Lstat(comp)
		if statErr != nil {
			continue // does not exist yet — nothing to check, and neither do deeper components
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
	// writing through it if IT is a symlink.
	if fi, err := os.Lstat(target); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to write through symlink: %s", target)
		}
	}
	return target, nil
}

// realOrSelf resolves symlinks in path, returning path itself (Cleaned) if it
// does not exist yet — repoRoot always exists (it is the cwd's repo), but
// this stays defensive for any future caller passing a not-yet-existing
// root.
func realOrSelf(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return filepath.Clean(path), nil
		}
		return "", err
	}
	return real, nil
}

// isWithin reports whether target is boundary itself or a descendant of it,
// using a Clean'd, separator-terminated prefix check (avoids "/a/bfoo"
// falsely matching boundary "/a/b").
func isWithin(boundary, target string) bool {
	b := filepath.Clean(boundary)
	t := filepath.Clean(target)
	if b == t {
		return true
	}
	return len(t) > len(b) && t[:len(b)] == b && t[len(b)] == filepath.Separator
}

// LocalHash returns sha256(file contents) hex, or "" with no error if the
// file does not exist (used by `skill list` to report install status:
// missing vs. present).
func LocalHash(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Write creates path's parent directory and writes body — callers MUST have
// already validated path via LocalPath (F8) immediately before calling this;
// Write itself performs no further symlink re-check (that window is short
// and the CLI calls LocalPath then Write back-to-back with no intervening
// I/O that could race a symlink swap in from an untrusted source on the same
// machine — accepted as out of scope for a single-operator local tool).
func Write(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}
