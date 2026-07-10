// Package cli v13 (Kit & Mining) end-to-end coverage — the CLI-surface half
// of phase-06's Loop 1 (kit full: nominate → review → publish → pull) and
// the CLI-surface security negatives that only make sense driven through
// `dandori kit`/`dandori knowledge import`, not HTTP. Loop 2 (mining→draft→
// nominate) and most of Loop 1's WEB half (publish-request/approve over
// `/reviews`) live in internal/web/knowledge_v13_e2e_test.go instead, since
// this package cannot drive HTTP handlers directly.
//
// Reference, not duplicate: Loop 3 (import) is ALREADY fully proven at the
// exact contract this phase asks for by import_cmd_test.go's
// TestImportMemoryFixtureSetsOrigin (origin=import-memory persisted) and
// TestImportMemoryIdenticalReimportSkipped ("không đổi", no new row) — not
// re-driven here; this file's own new coverage is entirely kit-focused
// (Loop 1 full round-trip + the kit-side security negatives below). M5
// (deep intermediate symlink) is already proven by
// internal/skillreg/kit_path_test.go's
// TestKitLocalPathSymlinkedDeepIntermediateRejected and sibling tests
// (TestKitLocalPathRejectsBackslashTraversal, TestKitLocalPathSymlinkedRootRejected,
// TestKitLocalPathEmptyOrDotSegmentsRejected) — not re-driven here. Deny-list
// at nominate/pull is already proven by kit_cmd_test.go's
// TestKitNominateDenyListAbortsNamingFile, TestKitNominateSettingsJSONAbortsNamingFile,
// and TestKitPullHooksPathInManifestRefusesWholePull.
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/observer"
	"github.com/phuc-nt/dandori/internal/skillreg"
	"github.com/phuc-nt/dandori/internal/store"
)

// withRepoRootValue re-points findRepoRoot to root for the remainder of the
// test (or until the next call) — unlike withFakeRepoRoot (skill_cmd_test.go),
// which fixes ONE root for a whole test via t.Cleanup, Loop 1 below needs TWO
// distinct repos in the SAME test (nominate scanned out of repo A, pull
// written into repo B), so the stub must be re-pointed mid-test rather than
// fixed once. The very first call also registers a single t.Cleanup that
// restores the pre-test original, regardless of how many times the var is
// re-pointed afterward.
func withRepoRootValue(t *testing.T, root string) {
	t.Helper()
	orig := findRepoRoot
	findRepoRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { findRepoRoot = orig })
}

// repointRepoRoot changes findRepoRoot's target again without registering a
// second cleanup (the first withRepoRootValue call already owns restoration).
func repointRepoRoot(root string) {
	findRepoRoot = func() (string, error) { return root, nil }
}

// runGit shells out to the real `git` binary — Loop 1 below is the one test
// in this package that deliberately does NOT stub gitLsFiles, so it proves
// the real `git ls-files` scan (untracked files excluded, exactly as
// production `kit nominate` behaves against a real worktree) rather than the
// stubbed-scan pattern every other kit_cmd_test.go test uses for speed.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=dandori-test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=dandori-test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// writeFixtureClaudeTree lays down a realistic .claude/{agents,rules,skills}
// fixture under repoRoot/.claude, per phase-06's Loop 1 spec, and returns the
// repo-relative (forward-slash) paths written — used both to `git add` them
// and to assert against kit pull's landed files afterward.
func writeFixtureClaudeTree(t *testing.T, repoRoot string) []string {
	t.Helper()
	files := map[string]string{
		"agents/reviewer.md":   "# Reviewer agent\nReviews code.",
		"rules/dev-rules.md":   "# Dev rules\nFollow YAGNI.",
		"skills/pack/SKILL.md": "# Pack skill\nDoes a thing.",
	}
	rel := make([]string, 0, len(files))
	for r, body := range files {
		writeRepoFile(t, repoRoot, r, body)
		rel = append(rel, r)
	}
	return rel
}

// runKitThroughGatedReviewToPublish drives the REAL gated-review pipeline
// (learn.RequestPublish -> observer.RequestAction -> govern.Decide(approve)
// -> observer.RunObserverApplier) for the kit unit named `name` — the
// applier's KindKit branch (internal/observer/knowledge_apply.go) is the same
// code the web /reviews decide handler triggers synchronously in production;
// this is its CLI-side equivalent, more authentic than force-publishing via
// publishTestKit (kit_cmd_test.go) since it actually exercises the applier's
// per-file verifyKitFilesTx path on the way to state=published.
func runKitThroughGatedReviewToPublish(t *testing.T, st *store.Store, unitID int64, actor string) {
	t.Helper()
	reqID, err := learn.RequestPublish(st, observer.RequestAction, unitID, actor)
	if err != nil {
		t.Fatalf("RequestPublish: %v", err)
	}
	won, err := govern.Decide(st, reqID, true, actor, "")
	if err != nil {
		t.Fatalf("govern.Decide approve: %v", err)
	}
	if !won {
		t.Fatalf("govern.Decide: expected to win the decision race")
	}
	if _, err := observer.RunObserverApplier(st); err != nil {
		t.Fatalf("RunObserverApplier: %v", err)
	}
	u, err := learn.GetUnit(st, unitID)
	if err != nil {
		t.Fatalf("GetUnit after applier: %v", err)
	}
	if u.State != learn.StatePublished {
		t.Fatalf("unit state after applier = %q, want published", u.State)
	}
}

// TestE2EKitFullLoopNominatePullAcrossTwoRepos is Loop 1: a real git-init'd
// repo A with a fixture .claude/{agents,rules,skills} tree -> `kit nominate`
// (real `git ls-files` scan, gitLsFiles NOT stubbed) -> the real gated
// review/publish pipeline -> `kit pull` into a SEPARATE repo B -> files land
// ONLY under whitelisted prefixes, adoption recorded, kit_pulled audit
// present.
func TestE2EKitFullLoopNominatePullAcrossTwoRepos(t *testing.T) {
	db := tempDB(t)
	t.Cleanup(resetKitFlags)
	t.Cleanup(resetKitPullFlags)
	// This test builds its own audit chain in a fresh temp-DB store, so it
	// must not read the git-tracked default docs/audit-checkpoints/ (which
	// belongs to a completely different chain) when govern.Verify runs below.
	t.Setenv("DANDORI_AUDIT_CHECKPOINT_DIR", t.TempDir())

	repoA := t.TempDir()
	runGit(t, repoA, "init", "-q")
	rel := writeFixtureClaudeTree(t, repoA)
	// A file that exists on disk but is NEVER `git add`-ed — must never
	// become a kit candidate (real-git equivalent of
	// TestKitNominateUntrackedFileExcluded, which uses the stubbed scan).
	writeRepoFile(t, repoA, "agents/untracked.md", "must never be scanned")
	runGit(t, repoA, "add", ".claude/agents/reviewer.md", ".claude/rules/dev-rules.md", ".claude/skills/pack/SKILL.md")
	runGit(t, repoA, "commit", "-q", "-m", "seed fixture kit tree")

	withRepoRootValue(t, repoA)

	out, err := execCLI(t, db, "kit", "nominate", "e2e-kit", "--yes")
	if err != nil {
		t.Fatalf("kit nominate: %v\n%s", err, out)
	}
	if strings.Contains(out, "untracked.md") {
		t.Fatalf("untracked file must never appear in nominate output: %s", out)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	var unitID int64
	if err := st.DB.QueryRow(`SELECT id FROM knowledge_units WHERE kind = 'kit' AND name = 'e2e-kit'`).Scan(&unitID); err != nil {
		t.Fatalf("expected kit unit nominated: %v", err)
	}
	var fileCount int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_kit_files WHERE unit_id = ?`, unitID).Scan(&fileCount)
	if fileCount != len(rel) {
		t.Fatalf("kit_files count = %d, want %d (untracked file must be excluded)", fileCount, len(rel))
	}

	if err := learn.SubmitForReview(st, unitID, "tester"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	runKitThroughGatedReviewToPublish(t, st, unitID, "tester")
	st.Close()

	repoB := t.TempDir()
	repointRepoRoot(repoB)

	out2, err := execCLI(t, db, "kit", "pull", "e2e-kit", "--yes")
	if err != nil {
		t.Fatalf("kit pull: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "pulled kit") {
		t.Errorf("expected pull confirmation, got:\n%s", out2)
	}

	realRepoB, _ := filepath.EvalSymlinks(repoB)
	for _, r := range rel {
		got, err := os.ReadFile(filepath.Join(realRepoB, ".claude", r))
		if err != nil {
			t.Fatalf("expected %s to land in repo B: %v", r, err)
		}
		if len(got) == 0 {
			t.Errorf("%s landed empty", r)
		}
	}
	if _, statErr := os.Stat(filepath.Join(realRepoB, ".claude", "agents", "untracked.md")); statErr == nil {
		t.Fatal("untracked.md must never be pulled — it was never a kit candidate")
	}

	st2, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	var adoptionCount int
	st2.DB.QueryRow(`SELECT count(*) FROM adoptions WHERE unit_id = ?`, unitID).Scan(&adoptionCount)
	if adoptionCount != 1 {
		t.Errorf("expected 1 adoption row for the pulled kit, got %d", adoptionCount)
	}
	var auditCount int
	st2.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'kit_pulled' AND subject = 'kit:e2e-kit'`).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("expected 1 kit_pulled audit row, got %d", auditCount)
	}
	if n, reason, err := govern.Verify(st2); err != nil || reason != "" {
		t.Errorf("audit chain verify failed after full loop: %v (at #%d reason=%q)", err, n, reason)
	}
}

// TestE2EKitTraversalAndAbsolutePathRejectedAtNominate covers the nominate-
// side traversal negative named in phase-06: a manifest-shaped RAW segment
// containing ".." or an absolute path must abort the whole nominate, naming
// the offending entry — kitpolicy.ValidateKitPath's raw-segment check (before
// path.Clean can hide the shape) is what catches this; kit_path.go's
// filesystem-level symlink walk (M5, referenced above) is the OTHER half of
// this defense, exercised at pull time instead.
func TestE2EKitTraversalAndAbsolutePathRejectedAtNominate(t *testing.T) {
	db := tempDB(t)
	withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	cases := []struct {
		name string
		rel  string
	}{
		{"dotdot-traversal", "agents/../hooks/x.md"},
		{"absolute-path", "/etc/passwd.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(resetKitFlags)
			withFakeGitLsFiles(t, []string{tc.rel})
			out, err := execCLI(t, db, "kit", "nominate", "traversal-"+tc.name, "--yes")
			if err == nil {
				t.Fatalf("expected traversal/absolute-path rejection, got success:\n%s", out)
			}
			st, serr := store.Open(db)
			if serr != nil {
				t.Fatal(serr)
			}
			defer st.Close()
			var n int
			st.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE kind = 'kit' AND name = ?`, "traversal-"+tc.name).Scan(&n)
			if n != 0 {
				t.Errorf("expected nothing written for %s, found %d unit(s)", tc.rel, n)
			}
		})
	}
}

// TestE2EKitPerFileTamperAfterPublishHardFailsPull is H1 Part 2: distinct
// from kit_cmd_test.go's TestKitPullManifestTamperHardFailsNothingWritten
// (which tampers the WHOLE manifest body on knowledge_units.body), this
// tampers only ONE knowledge_kit_files.body row after a legitimate publish —
// the manifest JSON and its content_hash stay internally consistent (passes
// verifyKitManifest's 3-way check), so this specifically proves runKitPull's
// PER-FILE verify (sha256(f.Body) vs f.ContentHash vs manifest's recorded
// hash for that path, kit_cmd.go lines ~361-366) is what catches a
// file-level tamper the manifest-level check alone would miss.
func TestE2EKitPerFileTamperAfterPublishHardFailsPull(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitPullFlags)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	files := []learn.KitFileInput{
		{Path: "agents/reviewer.md", Body: "reviewer body"},
		{Path: "rules/dev.md", Body: "dev rules body"},
	}
	unitID, _ := publishTestKit(t, st, "per-file-tamper-kit", files)

	// Tamper ONLY the per-file row's body — leave content_hash, the manifest
	// JSON (knowledge_units.body), and knowledge_units.content_hash untouched,
	// so the 3-way manifest check still passes and only the per-file check
	// can catch this.
	if _, err := st.DB.Exec(`UPDATE knowledge_kit_files SET body = ? WHERE unit_id = ? AND path = ?`,
		"TAMPERED reviewer body", unitID, "agents/reviewer.md"); err != nil {
		t.Fatalf("simulate per-file tamper: %v", err)
	}
	st.Close()

	out, err := execCLI(t, db, "kit", "pull", "per-file-tamper-kit", "--yes")
	if err == nil {
		t.Fatalf("expected per-file tamper hard fail, got success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "lệch hash") {
		t.Errorf("expected per-file hash-mismatch error, got: %v", err)
	}

	realRepo, _ := filepath.EvalSymlinks(repo)
	for _, rel := range []string{"agents/reviewer.md", "rules/dev.md"} {
		if _, statErr := os.Stat(filepath.Join(realRepo, ".claude", rel)); statErr == nil {
			t.Fatalf("expected NOTHING written after per-file tamper hard fail, but %s exists", rel)
		}
	}
}

// TestE2EPublishedKitPassesApproveHashRegression is C1: a legitimately
// published KIT must pass skillreg.ApproveHash (subject derived from
// u.Kind+":"+u.Name) followed by kit_cmd.go's own verifyKitManifest 3-way
// check — regression-guards the old ApproveHash bug that hardcoded
// learn.KindSkill in its subject lookup, which made any kit's audit-hash
// lookup silently mismatch (or fail) even for a completely legitimate
// publish. kit pull's happy path (kit_cmd_test.go's
// TestKitPullHappyPathWritesAllFilesAndAudit) already exercises this
// indirectly; this test isolates the ApproveHash call itself as the
// regression guard so a future re-hardcode fails here first, with a clear
// name.
func TestE2EPublishedKitPassesApproveHashRegression(t *testing.T) {
	db := tempDB(t)
	withFakeRepoRoot(t)
	t.Cleanup(resetKitPullFlags)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	files := []learn.KitFileInput{{Path: "agents/reviewer.md", Body: "reviewer body"}}
	unitID, manifestHash := publishTestKit(t, st, "approve-hash-kit", files)

	// This is the exact call C1 regressed: ApproveHash must resolve the
	// audit entry keyed off "kit:approve-hash-kit" (u.Kind+":"+u.Name), not a
	// hardcoded "skill:approve-hash-kit" subject that would never match.
	auditHash, err := skillreg.ApproveHash(st, unitID)
	if err != nil {
		t.Fatalf("ApproveHash on a legitimately published kit must succeed: %v", err)
	}
	if auditHash != manifestHash {
		t.Errorf("ApproveHash = %q, want manifest content hash %q", auditHash, manifestHash)
	}

	// Full pull must also succeed end-to-end (drives verifyKitManifest's
	// 3-way check on top of the now-proven ApproveHash call).
	out, err := execCLI(t, db, "kit", "pull", "approve-hash-kit", "--yes")
	if err != nil {
		t.Fatalf("kit pull for a legitimately published kit must succeed: %v\n%s", err, out)
	}
}
