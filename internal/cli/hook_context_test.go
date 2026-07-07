package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/skillreg"
	"github.com/phuc-nt/dandori/internal/store"
)

// setupRepoGit creates a temp dir with a .git marker so repoRootFrom finds it
// — mirrors withFakeRepoRoot's throwaway-dir discipline for skill_cmd_test.go
// (never touch this repo's own real .claude/skills/ during `go test`).
func setupRepoGit(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestComplianceNoticeAppendsWhenMissing: a mandated (required=1) published
// skill unit with no locally-pulled file must produce exactly one notice
// line mentioning the skill name.
func TestComplianceNoticeAppendsWhenMissing(t *testing.T) {
	db := tempDB(t)
	repo := setupRepoGit(t)
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	publishTestSkill(t, st, "mandated-missing", "body", true)

	notice := complianceNoticeLine(st, repo)
	if notice == "" {
		t.Fatal("expected a non-empty compliance notice for a missing mandated skill")
	}
	if !strings.Contains(notice, "mandated-missing") {
		t.Errorf("notice must name the missing skill: %s", notice)
	}
	if !strings.Contains(notice, "dandori skill pull") {
		t.Errorf("notice must tell the operator how to fix it: %s", notice)
	}
}

// TestComplianceNoticeAppendsWhenStale: file present locally but with content
// not matching the approve-time hash (simulates an older pull) must be
// reported as stale, distinct from missing.
func TestComplianceNoticeAppendsWhenStale(t *testing.T) {
	db := tempDB(t)
	repo := setupRepoGit(t)
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	publishTestSkill(t, st, "mandated-stale", "current body", true)

	path, err := skillreg.LocalPath(repo, "mandated-stale")
	if err != nil {
		t.Fatal(err)
	}
	if err := skillreg.Write(path, "an older, since-superseded body"); err != nil {
		t.Fatal(err)
	}

	notice := complianceNoticeLine(st, repo)
	if notice == "" {
		t.Fatal("expected a non-empty compliance notice for a stale mandated skill")
	}
	if !strings.Contains(notice, "mandated-stale") {
		t.Errorf("notice must name the stale skill: %s", notice)
	}
	if !strings.Contains(notice, "lệch phiên bản") {
		t.Errorf("notice must distinguish stale from missing: %s", notice)
	}
}

// TestComplianceNoticeSilentWhenUpToDate: once the exact published body is
// pulled, no notice — compliance is quiet by default (F3: only speak up when
// something needs attention).
func TestComplianceNoticeSilentWhenUpToDate(t *testing.T) {
	db := tempDB(t)
	repo := setupRepoGit(t)
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	publishTestSkill(t, st, "mandated-ok", "the exact published body", true)

	path, err := skillreg.LocalPath(repo, "mandated-ok")
	if err != nil {
		t.Fatal(err)
	}
	if err := skillreg.Write(path, "the exact published body"); err != nil {
		t.Fatal(err)
	}

	if notice := complianceNoticeLine(st, repo); notice != "" {
		t.Errorf("expected no notice once local matches published, got: %s", notice)
	}
}

// TestComplianceNoticeSilentWhenNotMandated: a published skill unit that is
// NOT required must never appear in the notice, even if never pulled —
// mandate is opt-in visibility, not a default check on every skill.
func TestComplianceNoticeSilentWhenNotMandated(t *testing.T) {
	db := tempDB(t)
	repo := setupRepoGit(t)
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	publishTestSkill(t, st, "optional-skill", "body", false)

	if notice := complianceNoticeLine(st, repo); notice != "" {
		t.Errorf("expected no notice for a non-mandated skill, got: %s", notice)
	}
}

// TestComplianceNoticeFailOpenNoRepoRoot: cwd outside any git repo (or a DB
// error) must never panic and must silently return "" — F3's absolute
// fail-open guarantee. The session must never be blocked by this check.
func TestComplianceNoticeFailOpenNoRepoRoot(t *testing.T) {
	db := tempDB(t)
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	publishTestSkill(t, st, "mandated-x", "body", true)

	notRepo := t.TempDir() // no .git anywhere above this in the temp hierarchy
	if notice := complianceNoticeLine(st, notRepo); notice != "" {
		t.Errorf("expected fail-open empty notice with no repo root, got: %s", notice)
	}
}

// TestComplianceNoticeFailOpenClosedStore: a closed store must not panic —
// every read errors, and the function must degrade to "" rather than crash
// the SessionStart hook.
func TestComplianceNoticeFailOpenClosedStore(t *testing.T) {
	db := tempDB(t)
	repo := setupRepoGit(t)
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	publishTestSkill(t, st, "mandated-y", "body", true)
	st.Close() // now every query against st must fail cleanly

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("complianceNoticeLine panicked on closed store: %v", r)
		}
	}()
	if notice := complianceNoticeLine(st, repo); notice != "" {
		t.Errorf("expected fail-open empty notice on closed store, got: %s", notice)
	}
}

// TestAppendComplianceNoticeJoinsWithBlankLine verifies the plumbing that
// attaches the notice to the SessionStart context text without mangling
// either an empty or a non-empty base text.
func TestAppendComplianceNoticeJoinsWithBlankLine(t *testing.T) {
	db := tempDB(t)
	repo := setupRepoGit(t)
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	publishTestSkill(t, st, "mandated-join", "body", true)

	got := appendComplianceNotice(st, repo, "")
	if !strings.Contains(got, "mandated-join") {
		t.Errorf("appendComplianceNotice with empty base must still surface the notice: %q", got)
	}

	got2 := appendComplianceNotice(st, repo, "existing context")
	if !strings.HasPrefix(got2, "existing context\n\n") {
		t.Errorf("appendComplianceNotice must join with a blank line: %q", got2)
	}
}
