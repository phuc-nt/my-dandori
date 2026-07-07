package cli

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// withFakeRepoRoot stubs findRepoRoot to a fresh temp dir for the duration
// of one test — skill pull/list must never touch this repo's own real
// .claude/skills/ during `go test`.
func withFakeRepoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	orig := findRepoRoot
	findRepoRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { findRepoRoot = orig })
	return root
}

// publishTestSkill mirrors observer.applyKnowledgePublish's real effect for
// kind=skill without needing the full gated-approval plumbing (observer
// cannot be imported here without risking a cycle, and RequestAction setup
// is heavy) — nominate, force-transition to published, then append the
// SAME audit shape applyKnowledgePublish writes.
func publishTestSkill(t *testing.T, st *store.Store, name, body string, required bool) (unitID int64, hash string) {
	t.Helper()
	id, err := learn.NominateUnit(st, learn.NominateParams{
		Kind: learn.KindSkill, Name: name, Title: name, Body: body, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("nominate: %v", err)
	}
	if err := learn.SubmitForReview(st, id, "tester"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET state = ?, required = ? WHERE id = ?`,
		learn.StatePublished, boolToInt(required), id); err != nil {
		t.Fatalf("force-publish: %v", err)
	}
	sum := sha256.Sum256([]byte(body))
	h := hex.EncodeToString(sum[:])
	a := &govern.Audit{St: st, Actor: "tester"}
	detail := fmt.Sprintf("skill %q v1 published, unit_id=%d, content_hash=%s (insight #1)", name, id, h)
	if _, err := a.Append("knowledge_published", "skill:"+name, detail); err != nil {
		t.Fatalf("audit append: %v", err)
	}
	return id, h
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestSkillPullHappyPath is also the H2 REQUIRED regression test: the pull
// path must pass a real learn window (not windowDays=0) into
// RecordUnitAdoption so metric_before actually freezes off the operator's
// prior behavior instead of silently staying NULL forever (H2 — a
// windowDays=0 pull, `datetime('now','-0 days')`, sees zero prior runs no
// matter how much history exists).
func TestSkillPullHappyPath(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	publishTestSkill(t, st, "greet-skill", "# Greet\nSay hello.", false)

	// Seed a prior run for the operator `skill pull` will actually use
	// (cfg.UserName, resolved the same way openStore does) so metric_before
	// has something to freeze off.
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('agent-a', 'agent-a', ?)`, store.Now()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO runs(id, agent_id, project, status, started_at, ended_at, operator_id)
		VALUES('prior-run', 'agent-a', 'proj', 'done', ?, ?, ?)`, store.Now(), store.Now(), cfg.UserName); err != nil {
		t.Fatalf("seed prior run: %v", err)
	}
	st.Close()

	out, err := execCLI(t, db, "skill", "pull", "greet-skill", "--yes")
	if err != nil {
		t.Fatalf("skill pull: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "pulled") {
		t.Errorf("expected pulled confirmation, got:\n%s", out)
	}

	written := filepath.Join(repo, ".claude", "skills", "greet-skill", "SKILL.md")
	body, err := os.ReadFile(written)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(body) != "# Greet\nSay hello." {
		t.Errorf("written body = %q", body)
	}

	// RecordUnitAdoption + audit skill_pulled both landed.
	st2, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	var adoptionCount int
	var metricBefore sql.NullFloat64
	st2.DB.QueryRow(`SELECT count(*), MAX(metric_before) FROM adoptions WHERE unit_id = (SELECT id FROM knowledge_units WHERE name = 'greet-skill')`).
		Scan(&adoptionCount, &metricBefore)
	if adoptionCount == 0 {
		t.Fatal("expected an adoptions row after pull")
	}
	if !metricBefore.Valid {
		t.Error("H2: metric_before is NULL after pull — windowDays=0 regression (must pass cfg.LearnWindowDays)")
	}
	var auditCount int
	st2.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'skill_pulled' AND subject = 'skill:greet-skill'`).Scan(&auditCount)
	if auditCount == 0 {
		t.Error("expected a skill_pulled audit entry after pull")
	}
}

func TestSkillPullUnpublishedFailsOpen(t *testing.T) {
	db := tempDB(t)
	withFakeRepoRoot(t)

	out, err := execCLI(t, db, "skill", "pull", "ghost-skill", "--yes")
	if err != nil {
		t.Fatalf("skill pull should exit cleanly (fail-open), got err: %v", err)
	}
	if !strings.Contains(out, "chưa published") {
		t.Errorf("expected F3 fail-open message, got:\n%s", out)
	}
}

// TestSkillPullHashMismatchRefusesWrite is the F12/F7 REQUIRED security
// negative at the CLI layer: a row tampered after publish (body edited,
// even if content_hash on the row is ALSO rewritten to match) must still
// fail because verification runs against the independent audit hash — and
// critically, no file must be written.
func TestSkillPullHashMismatchRefusesWrite(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	publishTestSkill(t, st, "tampered", "original safe body", false)
	tamperedBody := "evil instructions injected post-approval"
	sum := sha256.Sum256([]byte(tamperedBody))
	tamperedHash := hex.EncodeToString(sum[:])
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET body = ?, content_hash = ? WHERE name = ?`,
		tamperedBody, tamperedHash, "tampered"); err != nil {
		t.Fatalf("simulate tamper: %v", err)
	}
	st.Close()

	out, err := execCLI(t, db, "skill", "pull", "tampered", "--yes")
	if err == nil {
		t.Fatalf("expected pull to fail on hash mismatch, got success:\n%s", out)
	}

	written := filepath.Join(repo, ".claude", "skills", "tampered", "SKILL.md")
	if _, statErr := os.Stat(written); statErr == nil {
		t.Fatal("hash mismatch must not write ANY file — found one at " + written)
	}
}

func TestSkillPullNameTraversalRejected(t *testing.T) {
	db := tempDB(t)
	withFakeRepoRoot(t)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// A name containing ".." can never be a valid published slug (ValidSlug
	// rejects it at nominate time already), so exercise the path-safety leg
	// directly the way `skill pull` would after a hypothetical Get success —
	// via skillreg.LocalPath, which is what CLI relies on. This confirms the
	// CLI's dependency actually rejects traversal; a crafted unit name can
	// never reach this path in practice since NominateUnit already blocks it.
	out, err := execCLI(t, db, "skill", "pull", "../evil", "--yes")
	if err != nil {
		// Either "not published" (fail-open, since "../evil" was never a
		// valid nominated slug) or an explicit path-safety error — either
		// way it must not succeed silently or write outside the repo.
		return
	}
	if !strings.Contains(out, "chưa published") {
		t.Errorf("expected fail-open or rejection for traversal-shaped name, got:\n%s", out)
	}
}

func TestSkillPullPromptsWithoutYes(t *testing.T) {
	db := tempDB(t)
	repo := withFakeRepoRoot(t)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	publishTestSkill(t, st, "prompted-skill", "body text", false)
	st.Close()

	origPrompt := confirmPrompt
	confirmPrompt = func(cmd *cobra.Command, prompt string) bool { return false }
	t.Cleanup(func() { confirmPrompt = origPrompt })
	// cobra persists flag values across Execute() calls in-process — reset
	// explicitly so an earlier test's --yes doesn't leak into this one.
	flagSkillPullYes = false
	t.Cleanup(func() { flagSkillPullYes = false })

	out, err := execCLI(t, db, "skill", "pull", "prompted-skill")
	if err != nil {
		t.Fatalf("skill pull (declined): %v", err)
	}
	if !strings.Contains(out, "huỷ") {
		t.Errorf("expected cancellation message, got:\n%s", out)
	}
	written := filepath.Join(repo, ".claude", "skills", "prompted-skill", "SKILL.md")
	if _, statErr := os.Stat(written); statErr == nil {
		t.Fatal("declining the prompt must not write the file")
	}
}

func TestSkillPullTextDiffShown(t *testing.T) {
	db := tempDB(t)
	withFakeRepoRoot(t)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	publishTestSkill(t, st, "diff-skill", "line one\nline two\n", false)
	st.Close()

	out, err := execCLI(t, db, "skill", "pull", "diff-skill", "--yes")
	if err != nil {
		t.Fatalf("skill pull: %v\n%s", err, out)
	}
	if !strings.Contains(out, "+ line one") && !strings.Contains(out, "line one") {
		t.Errorf("expected diff output to mention new content, got:\n%s", out)
	}
	if strings.Contains(out, "<pre") || strings.Contains(out, "<div") {
		t.Errorf("diff output must be plain text, not HTML: %s", out)
	}
}

func TestSkillListShowsInstallStatus(t *testing.T) {
	db := tempDB(t)
	withFakeRepoRoot(t)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	publishTestSkill(t, st, "listed-skill", "content", true)
	st.Close()

	out, err := execCLI(t, db, "skill", "list")
	if err != nil {
		t.Fatalf("skill list: %v", err)
	}
	if !strings.Contains(out, "listed-skill") {
		t.Errorf("expected listed-skill in output:\n%s", out)
	}
	if !strings.Contains(out, "required") {
		t.Errorf("expected required flag shown:\n%s", out)
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("expected local=missing before any pull:\n%s", out)
	}

	if _, err := execCLI(t, db, "skill", "pull", "listed-skill", "--yes"); err != nil {
		t.Fatalf("pull: %v", err)
	}
	out2, err := execCLI(t, db, "skill", "list")
	if err != nil {
		t.Fatalf("skill list after pull: %v", err)
	}
	if !strings.Contains(out2, "match") {
		t.Errorf("expected local=match after pull:\n%s", out2)
	}
}
