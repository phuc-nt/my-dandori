package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// withFakeGitLsFiles stubs gitLsFiles to return exactly `rel` (repo-relative,
// forward-slash paths) regardless of pathspec — this decouples kit nominate
// tests from a real git worktree/index while still exercising the exact same
// scan→kitpolicy→scan-result pipeline runKitNominate uses in production.
func withFakeGitLsFiles(t *testing.T, rel []string) {
	t.Helper()
	orig := gitLsFiles
	gitLsFiles = func(repoRoot, pathspec string) ([]string, error) { return rel, nil }
	t.Cleanup(func() { gitLsFiles = orig })
}

// writeRepoFile writes body at repoRoot/.claude/rel (creating parent dirs) —
// rel is .claude-relative (e.g. "agents/reviewer.md"), the same convention
// kitpolicy/knowledge_kit_files/skillreg.KitLocalPath all use; repoRoot is
// the REPO root (the .claude dir's parent), matching what real `git
// ls-files -- .claude` resolves against and what runKitNominate's
// os.ReadFile(repoRoot, ".claude", rel) reads from.
func writeRepoFile(t *testing.T, repoRoot, rel, body string) {
	t.Helper()
	full := filepath.Join(repoRoot, ".claude", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func resetKitFlags() {
	flagKitTitle = ""
	flagKitExclude = nil
	flagKitNominYes = false
}

func TestKitNominateHappyPathOneTxUnitAndFiles(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	rel := []string{"agents/reviewer.md", "rules/dev.md"}
	writeRepoFile(t, repo, rel[0], "review agent body")
	writeRepoFile(t, repo, rel[1], "dev rules body")
	withFakeGitLsFiles(t, rel)

	out, err := execCLI(t, db, "kit", "nominate", "agent-pack", "--yes")
	if err != nil {
		t.Fatalf("kit nominate: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "đã đề cử kit") {
		t.Errorf("expected nominate confirmation, got:\n%s", out)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var unitCount, fileCount int
	var unitID int64
	if err := st.DB.QueryRow(`SELECT id FROM knowledge_units WHERE kind = 'kit' AND name = 'agent-pack'`).Scan(&unitID); err != nil {
		t.Fatalf("expected one kind=kit unit: %v", err)
	}
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE kind = 'kit' AND name = 'agent-pack'`).Scan(&unitCount)
	if unitCount != 1 {
		t.Errorf("unit count = %d, want 1", unitCount)
	}
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_kit_files WHERE unit_id = ?`, unitID).Scan(&fileCount)
	if fileCount != 2 {
		t.Errorf("knowledge_kit_files count = %d, want 2", fileCount)
	}
}

func TestKitNominateDenyListAbortsNamingFile(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	rel := []string{"agents/reviewer.md", "hooks/pre-tool.md"}
	writeRepoFile(t, repo, rel[0], "review agent body")
	writeRepoFile(t, repo, rel[1], "hook body")
	withFakeGitLsFiles(t, rel)

	out, err := execCLI(t, db, "kit", "nominate", "bad-kit", "--yes")
	if err == nil {
		t.Fatalf("expected deny-list abort, got success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "hooks/pre-tool.md") {
		t.Errorf("expected error to name the offending file, got: %v", err)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE kind = 'kit'`).Scan(&n)
	if n != 0 {
		t.Errorf("expected nothing written on deny-list abort, found %d unit(s)", n)
	}
}

func TestKitNominateSettingsJSONAbortsNamingFile(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	rel := []string{"agents/reviewer.md", "settings.json"}
	writeRepoFile(t, repo, rel[0], "review agent body")
	writeRepoFile(t, repo, rel[1], `{"hooks":{}}`)
	withFakeGitLsFiles(t, rel)

	out, err := execCLI(t, db, "kit", "nominate", "bad-kit-2", "--yes")
	if err == nil {
		t.Fatalf("expected settings.json deny abort, got success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "settings.json") {
		t.Errorf("expected error to name settings.json, got: %v", err)
	}
}

func TestKitNominateNonMarkdownInSkillDirWarnsAndContinues(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	rel := []string{"skills/foo/SKILL.md", "skills/foo/run.py"}
	writeRepoFile(t, repo, rel[0], "# Foo skill")
	writeRepoFile(t, repo, rel[1], "print('hi')")
	withFakeGitLsFiles(t, rel)

	out, err := execCLI(t, db, "kit", "nominate", "skill-kit", "--yes")
	if err != nil {
		t.Fatalf("kit nominate: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "CẢNH BÁO") || !strings.Contains(out, "run.py") {
		t.Errorf("expected H4 warning naming run.py, got:\n%s", out)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var unitID int64
	if err := st.DB.QueryRow(`SELECT id FROM knowledge_units WHERE kind = 'kit' AND name = 'skill-kit'`).Scan(&unitID); err != nil {
		t.Fatalf("expected the kit to still be nominated (warn, not abort): %v", err)
	}
	var fileCount int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_kit_files WHERE unit_id = ?`, unitID).Scan(&fileCount)
	if fileCount != 1 {
		t.Errorf("expected only the .md file kept, got %d file rows", fileCount)
	}
	var path string
	st.DB.QueryRow(`SELECT path FROM knowledge_kit_files WHERE unit_id = ?`, unitID).Scan(&path)
	if path != "skills/foo/SKILL.md" {
		t.Errorf("kept file path = %q, want skills/foo/SKILL.md", path)
	}
}

// TestKitNominateUntrackedFileExcluded proves the scan only ever sees what
// gitLsFiles reports — a file present on disk but NOT returned by
// gitLsFiles (i.e. untracked) must never become a kit candidate.
func TestKitNominateUntrackedFileExcluded(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	writeRepoFile(t, repo, "agents/reviewer.md", "tracked body")
	writeRepoFile(t, repo, "agents/untracked.md", "untracked body — must be excluded")
	// gitLsFiles reports ONLY the tracked file.
	withFakeGitLsFiles(t, []string{"agents/reviewer.md"})

	out, err := execCLI(t, db, "kit", "nominate", "tracked-kit", "--yes")
	if err != nil {
		t.Fatalf("kit nominate: %v\noutput:\n%s", err, out)
	}
	if strings.Contains(out, "untracked.md") {
		t.Errorf("untracked file must never appear in nominate output: %s", out)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var unitID int64
	st.DB.QueryRow(`SELECT id FROM knowledge_units WHERE kind = 'kit' AND name = 'tracked-kit'`).Scan(&unitID)
	var fileCount int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_kit_files WHERE unit_id = ?`, unitID).Scan(&fileCount)
	if fileCount != 1 {
		t.Errorf("expected exactly 1 file row (tracked only), got %d", fileCount)
	}
}

func TestKitNominateSecretShapedContentAborts(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	rel := []string{"rules/leaky.md"}
	writeRepoFile(t, repo, rel[0], "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	withFakeGitLsFiles(t, rel)

	out, err := execCLI(t, db, "kit", "nominate", "leaky-kit", "--yes")
	if err == nil {
		t.Fatalf("expected secret-scan abort, got success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "leaky.md") {
		t.Errorf("expected error to name the offending file, got: %v", err)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE kind = 'kit'`).Scan(&n)
	if n != 0 {
		t.Errorf("expected nothing written on secret-scan abort, found %d unit(s)", n)
	}
}

func TestKitNominateOversizedFileAborts(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	big := strings.Repeat("x", 64*1024+1)
	rel := []string{"rules/huge.md"}
	writeRepoFile(t, repo, rel[0], big)
	withFakeGitLsFiles(t, rel)

	out, err := execCLI(t, db, "kit", "nominate", "huge-kit", "--yes")
	if err == nil {
		t.Fatalf("expected oversized-file abort, got success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "huge.md") {
		t.Errorf("expected error to name the offending file, got: %v", err)
	}
}

func TestKitNominateExcludeGlobFiltersFile(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	rel := []string{"rules/keep.md", "rules/legacy.md"}
	writeRepoFile(t, repo, rel[0], "keep body")
	writeRepoFile(t, repo, rel[1], "legacy body")
	withFakeGitLsFiles(t, rel)

	out, err := execCLI(t, db, "kit", "nominate", "exclude-kit", "--exclude", "rules/legacy.md", "--yes")
	if err != nil {
		t.Fatalf("kit nominate: %v\noutput:\n%s", err, out)
	}
	if strings.Contains(out, "legacy.md") {
		t.Errorf("excluded file must not appear in manifest preview: %s", out)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var unitID int64
	st.DB.QueryRow(`SELECT id FROM knowledge_units WHERE kind = 'kit' AND name = 'exclude-kit'`).Scan(&unitID)
	var fileCount int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_kit_files WHERE unit_id = ?`, unitID).Scan(&fileCount)
	if fileCount != 1 {
		t.Errorf("expected 1 file after --exclude, got %d", fileCount)
	}
}

func TestKitNominateDeclinePromptWritesNothing(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	origPrompt := confirmPrompt
	confirmPrompt = func(cmd *cobra.Command, prompt string) bool { return false }
	t.Cleanup(func() { confirmPrompt = origPrompt })

	rel := []string{"agents/one.md"}
	writeRepoFile(t, repo, rel[0], "body")
	withFakeGitLsFiles(t, rel)

	out, err := execCLI(t, db, "kit", "nominate", "declined-kit")
	if err != nil {
		t.Fatalf("kit nominate (declined): %v\n%s", err, out)
	}
	if !strings.Contains(out, "huỷ") {
		t.Errorf("expected cancellation message, got:\n%s", out)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE kind = 'kit' AND name = 'declined-kit'`).Scan(&n)
	if n != 0 {
		t.Errorf("declining the prompt must not nominate anything, found %d", n)
	}
}

func TestKitNominateCapsFileCountExceeded(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitFlags)

	var rel []string
	for i := 0; i < 201; i++ {
		p := filepath.ToSlash(filepath.Join("rules", strconv.Itoa(i)+".md"))
		writeRepoFile(t, repo, p, "x")
		rel = append(rel, p)
	}
	withFakeGitLsFiles(t, rel)

	out, err := execCLI(t, db, "kit", "nominate", "too-big-kit", "--yes")
	if err == nil {
		t.Fatalf("expected MaxKitFiles cap error, got success:\n%s", out)
	}
}

func resetKitPullFlags() { flagKitPullYes = false }

// publishTestKit nominates a real kind=kit unit (learn.NominateUnitTx, same
// path `kit nominate` uses) with the given files, force-publishes it, and
// appends the SAME "knowledge_published" audit shape observer.
// applyKnowledgePublish writes in production — the manifest's OWN
// ContentHash (not an arbitrary hash) is what gets recorded, since that is
// what a real publish flow pins.
func publishTestKit(t *testing.T, st *store.Store, name string, files []learn.KitFileInput) (unitID int64, manifestHash string) {
	t.Helper()
	id, err := learn.NominateUnitTx(st, learn.KitNominateParams{
		Name: name, Title: name, Files: files, NominatedBy: "tester", Origin: "human",
	})
	if err != nil {
		t.Fatalf("nominate kit: %v", err)
	}
	if err := learn.SubmitForReview(st, id, "tester"); err != nil {
		t.Fatalf("submit kit: %v", err)
	}
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET state = ? WHERE id = ?`, learn.StatePublished, id); err != nil {
		t.Fatalf("force-publish kit: %v", err)
	}
	manifest, _ := learn.BuildKitManifest(files)
	h, err := manifest.ContentHash()
	if err != nil {
		t.Fatalf("manifest content hash: %v", err)
	}
	a := &govern.Audit{St: st, Actor: "tester"}
	detail := fmt.Sprintf("kit %q v1 published, unit_id=%d, content_hash=%s (insight #1)", name, id, h)
	if _, err := a.Append("knowledge_published", "kit:"+name, detail); err != nil {
		t.Fatalf("audit append kit: %v", err)
	}
	return id, h
}

func TestKitPullHappyPathWritesAllFilesAndAudit(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitPullFlags)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	files := []learn.KitFileInput{
		{Path: "agents/reviewer.md", Body: "reviewer body"},
		{Path: "skills/z/references/a.md", Body: "reference body"},
	}
	_, _ = publishTestKit(t, st, "agent-pack", files)
	st.Close()

	out, err := execCLI(t, db, "kit", "pull", "agent-pack", "--yes")
	if err != nil {
		t.Fatalf("kit pull: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "pulled kit") {
		t.Errorf("expected pull confirmation, got:\n%s", out)
	}

	realRepo, _ := filepath.EvalSymlinks(repo)
	for rel, want := range map[string]string{
		"agents/reviewer.md":       "reviewer body",
		"skills/z/references/a.md": "reference body",
	} {
		got, err := os.ReadFile(filepath.Join(realRepo, ".claude", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}

	st2, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	var adoptionCount int
	st2.DB.QueryRow(`SELECT count(*) FROM adoptions`).Scan(&adoptionCount)
	if adoptionCount != 1 {
		t.Errorf("expected 1 adoption row, got %d", adoptionCount)
	}
	var auditCount int
	st2.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'kit_pulled' AND subject = 'kit:agent-pack'`).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("expected 1 kit_pulled audit row, got %d", auditCount)
	}
}

// TestKitPullManifestTamperHardFailsNothingWritten proves the 3-way manifest
// verify: a row-only tamper of the knowledge_units.body (manifest JSON) must
// hard-fail the WHOLE pull before any file write, even though the
// per-file knowledge_kit_files rows are untouched.
func TestKitPullManifestTamperHardFailsNothingWritten(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitPullFlags)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	files := []learn.KitFileInput{{Path: "agents/reviewer.md", Body: "reviewer body"}}
	unitID, _ := publishTestKit(t, st, "tampered-kit", files)
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET body = '{"files":[]}' WHERE id = ?`, unitID); err != nil {
		t.Fatalf("simulate manifest tamper: %v", err)
	}
	st.Close()

	out, err := execCLI(t, db, "kit", "pull", "tampered-kit", "--yes")
	if err == nil {
		t.Fatalf("expected manifest-tamper hard fail, got success:\n%s", out)
	}

	realRepo, _ := filepath.EvalSymlinks(repo)
	if _, statErr := os.Stat(filepath.Join(realRepo, ".claude", "agents", "reviewer.md")); statErr == nil {
		t.Fatal("expected NOTHING written after manifest-tamper hard fail")
	}
}

// TestKitPullHooksPathInManifestRefusesWholePull covers a manifest that
// (however it got there — e.g. a bypassed nominate, or a future kitpolicy
// regression) lists a hooks/ path: KitLocalPath's own kitpolicy lexical
// layer must refuse it, hard-failing the whole pull before any write.
func TestKitPullHooksPathInManifestRefusesWholePull(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitPullFlags)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	// Bypass kit nominate's own kitpolicy scan by inserting kit_files rows
	// directly alongside a manifest that (inconsistently with real nominate)
	// lists a denied path — this is exactly the "how did this get published"
	// defense-in-depth scenario KitLocalPath itself must still catch.
	id, err := learn.NominateUnitTx(st, learn.KitNominateParams{
		Name: "hook-kit", Title: "hook-kit",
		Files:       []learn.KitFileInput{{Path: "agents/ok.md", Body: "ok body"}},
		NominatedBy: "tester", Origin: "human",
	})
	if err != nil {
		t.Fatalf("nominate: %v", err)
	}
	if err := learn.SubmitForReview(st, id, "tester"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// Force the manifest body + kit_files row to a hooks/ path directly in
	// the DB (bypassing kitpolicy the way a compromised writer would).
	badManifest := `{"files":[{"path":"hooks/evil.md","content_hash":"","size":4}]}`
	sum := sha256Hex("evil")
	badManifest = strings.Replace(badManifest, `"content_hash":""`, `"content_hash":"`+sum+`"`, 1)
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET body = ?, content_hash = ?, state = ? WHERE id = ?`,
		badManifest, sha256Hex(badManifest), learn.StatePublished, id); err != nil {
		t.Fatalf("force manifest: %v", err)
	}
	if _, err := st.DB.Exec(`DELETE FROM knowledge_kit_files WHERE unit_id = ?`, id); err != nil {
		t.Fatalf("clear kit files: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO knowledge_kit_files(unit_id, path, body, content_hash, size) VALUES (?, ?, ?, ?, ?)`,
		id, "hooks/evil.md", "evil", sum, 4); err != nil {
		t.Fatalf("insert bad kit file: %v", err)
	}
	a := &govern.Audit{St: st, Actor: "tester"}
	detail := fmt.Sprintf("kit %q v1 published, unit_id=%d, content_hash=%s (insight #1)", "hook-kit", id, sha256Hex(badManifest))
	if _, err := a.Append("knowledge_published", "kit:hook-kit", detail); err != nil {
		t.Fatalf("audit append: %v", err)
	}
	st.Close()

	out, err := execCLI(t, db, "kit", "pull", "hook-kit", "--yes")
	if err == nil {
		t.Fatalf("expected hooks/ path refusal, got success:\n%s", out)
	}

	realRepo, _ := filepath.EvalSymlinks(repo)
	if _, statErr := os.Stat(filepath.Join(realRepo, ".claude", "hooks")); statErr == nil {
		t.Fatal("expected NOTHING written for a hooks/ manifest path")
	}
}

// TestKitPullOrphanFileWarnedNotDeleted proves a local file no longer in the
// pulled manifest is reported but left on disk (never auto-deleted).
func TestKitPullOrphanFileWarnedNotDeleted(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitPullFlags)

	orphanPath := filepath.Join(repo, ".claude", "agents", "old.md")
	if err := os.MkdirAll(filepath.Dir(orphanPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphanPath, []byte("stale agent"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	files := []learn.KitFileInput{{Path: "agents/new.md", Body: "new agent body"}}
	publishTestKit(t, st, "refresh-kit", files)
	st.Close()

	out, err := execCLI(t, db, "kit", "pull", "refresh-kit", "--yes")
	if err != nil {
		t.Fatalf("kit pull: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "old.md") || !strings.Contains(out, "giữ nguyên") {
		t.Errorf("expected orphan warning naming old.md, got:\n%s", out)
	}
	if _, statErr := os.Stat(orphanPath); statErr != nil {
		t.Fatal("orphan file must be left on disk, never deleted")
	}
}

func TestKitPullDeclinePromptWritesNothing(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)
	t.Cleanup(resetKitPullFlags)

	origPrompt := confirmPrompt
	confirmPrompt = func(cmd *cobra.Command, prompt string) bool { return false }
	t.Cleanup(func() { confirmPrompt = origPrompt })

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	files := []learn.KitFileInput{{Path: "agents/one.md", Body: "one body"}}
	publishTestKit(t, st, "declined-kit", files)
	st.Close()

	out, err := execCLI(t, db, "kit", "pull", "declined-kit")
	if err != nil {
		t.Fatalf("kit pull (declined): %v\n%s", err, out)
	}
	if !strings.Contains(out, "huỷ") {
		t.Errorf("expected cancellation message, got:\n%s", out)
	}
	realRepo, _ := filepath.EvalSymlinks(repo)
	if _, statErr := os.Stat(filepath.Join(realRepo, ".claude", "agents", "one.md")); statErr == nil {
		t.Fatal("declining the prompt must not write anything")
	}
}

func TestKitListShowsPublishedKitsAndInstallStatus(t *testing.T) {
	db := tempDB(t)
	withFakeRepoRoot(t)
	t.Cleanup(resetKitPullFlags)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	files := []learn.KitFileInput{{Path: "agents/reviewer.md", Body: "reviewer body"}}
	publishTestKit(t, st, "list-kit", files)
	st.Close()

	out, err := execCLI(t, db, "kit", "list")
	if err != nil {
		t.Fatalf("kit list: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "list-kit") {
		t.Errorf("expected list-kit in output, got:\n%s", out)
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("expected local=missing before pull, got:\n%s", out)
	}

	if _, err := execCLI(t, db, "kit", "pull", "list-kit", "--yes"); err != nil {
		t.Fatalf("kit pull: %v", err)
	}
	out2, err := execCLI(t, db, "kit", "list")
	if err != nil {
		t.Fatalf("kit list (after pull): %v", err)
	}
	if !strings.Contains(out2, "match") {
		t.Errorf("expected local=match after pull, got:\n%s", out2)
	}
}
