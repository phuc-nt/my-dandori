package skillreg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// publishSkill mirrors observer.applyKnowledgePublish's real effect for
// kind=skill (nominate -> in_review -> published transition + the
// "knowledge_published" audit entry recording content_hash) without
// depending on the observer package (which would import learn and could not
// be imported back here) or the full gated-approval RequestAction plumbing —
// this reproduces exactly the two facts skillreg reads: the published row
// and its independent audit hash.
func publishSkill(t *testing.T, st *store.Store, name, body string) (unitID int64, hash string) {
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
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET state = ? WHERE id = ?`, learn.StatePublished, id); err != nil {
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

func TestPublishedAndGet(t *testing.T) {
	st := openTestStore(t)
	id, _ := publishSkill(t, st, "my-skill", "# My Skill\nbody text")

	list, err := Published(st)
	if err != nil {
		t.Fatalf("Published: %v", err)
	}
	if len(list) != 1 || list[0].UnitID != id || list[0].Name != "my-skill" {
		t.Fatalf("Published: got %+v", list)
	}

	byName, err := Get(st, "my-skill")
	if err != nil {
		t.Fatalf("Get by name: %v", err)
	}
	if byName.UnitID != id {
		t.Errorf("Get by name: unitID = %d, want %d", byName.UnitID, id)
	}

	byID, err := Get(st, "1")
	if err != nil {
		t.Fatalf("Get by id: %v", err)
	}
	if byID.Name != "my-skill" {
		t.Errorf("Get by id: name = %q", byID.Name)
	}
}

// TestGetUnpublishedFailsOpen covers F3: no crash, ErrNotFound for a
// non-existent / not-yet-published unit.
func TestGetUnpublishedFailsOpen(t *testing.T) {
	st := openTestStore(t)
	if _, err := Get(st, "does-not-exist"); err != ErrNotFound {
		t.Errorf("Get unpublished: err = %v, want ErrNotFound", err)
	}

	// A nominated-but-not-published unit must ALSO fail-open, not leak body.
	id, err := learn.NominateUnit(st, learn.NominateParams{
		Kind: learn.KindSkill, Name: "draft-skill", Title: "draft", Body: "draft body", NominatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("nominate: %v", err)
	}
	if _, err := Get(st, "draft-skill"); err != ErrNotFound {
		t.Errorf("Get draft: err = %v, want ErrNotFound", err)
	}
	if _, err := Get(st, itoa(id)); err != ErrNotFound {
		t.Errorf("Get draft by id: err = %v, want ErrNotFound", err)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestApproveHashMatchesPublish(t *testing.T) {
	st := openTestStore(t)
	id, wantHash := publishSkill(t, st, "hash-skill", "content for hashing")

	got, err := ApproveHash(st, id)
	if err != nil {
		t.Fatalf("ApproveHash: %v", err)
	}
	if got != wantHash {
		t.Errorf("ApproveHash = %s, want %s", got, wantHash)
	}
}

// publishSkillSupersedingV1 mirrors publishSkill but sets supersedes_id and
// version_n=2, and appends a "knowledge_published" audit entry carrying THIS
// (v2) unit's own unit_id — reproducing observer.applyKnowledgePublish's real
// F5 versioning effect for the H1 downgrade test below.
func publishSkillSupersedingV1(t *testing.T, st *store.Store, name string, v1ID int64, body string) (unitID int64, hash string) {
	t.Helper()
	id, err := learn.NominateUnit(st, learn.NominateParams{
		Kind: learn.KindSkill, Name: name, Title: name, Body: body, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("nominate v2: %v", err)
	}
	if err := learn.SubmitForReview(st, id, "tester"); err != nil {
		t.Fatalf("submit v2: %v", err)
	}
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET state = 'superseded' WHERE id = ?`, v1ID); err != nil {
		t.Fatalf("supersede v1: %v", err)
	}
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET state = ?, version_n = 2, supersedes_id = ? WHERE id = ?`,
		learn.StatePublished, v1ID, id); err != nil {
		t.Fatalf("force-publish v2: %v", err)
	}
	sum := sha256.Sum256([]byte(body))
	h := hex.EncodeToString(sum[:])
	a := &govern.Audit{St: st, Actor: "tester"}
	detail := fmt.Sprintf("skill %q v2 published, unit_id=%d, content_hash=%s (insight #2)", name, id, h)
	if _, err := a.Append("knowledge_published", "skill:"+name, detail); err != nil {
		t.Fatalf("audit append v2: %v", err)
	}
	return id, h
}

// TestApproveHashRejectsVersionDowngrade is the H1 REQUIRED security
// negative: publish v1 → supersede with v2 (fixing a flaw in v1) → a
// DB-writer UPDATEs the live (v2) row back to v1's body+content_hash,
// simulating a version-downgrade tamper. Before the H1 fix, ApproveHash
// scanned audit history for ANY entry whose hash agreed with the row and
// would happily return v1's historical hash, making Verify pass on the
// downgraded content. After the fix, ApproveHash returns v2's own
// unit_id-matched hash regardless — the row's v1 content_hash no longer
// agrees with any leg, so Verify (all three legs) must fail closed.
func TestApproveHashRejectsVersionDowngrade(t *testing.T) {
	st := openTestStore(t)
	v1ID, v1Hash := publishSkill(t, st, "lineage-skill", "v1 body with a flaw")
	v2ID, v2Hash := publishSkillSupersedingV1(t, st, "lineage-skill", v1ID, "v2 body — flaw fixed")

	// Attacker rewrites the live (v2) row back to v1's body+hash.
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET body = ?, content_hash = ? WHERE id = ?`,
		"v1 body with a flaw", v1Hash, v2ID); err != nil {
		t.Fatalf("simulate downgrade tamper: %v", err)
	}

	got, err := ApproveHash(st, v2ID)
	if err != nil {
		t.Fatalf("ApproveHash: %v", err)
	}
	if got != v2Hash {
		t.Fatalf("ApproveHash = %s, want v2's own hash %s (downgrade must not be authoritative)", got, v2Hash)
	}
	if got == v1Hash {
		t.Fatal("ApproveHash returned v1's hash — downgrade defeats F7")
	}

	s, err := Get(st, "lineage-skill")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s.UnitID != v2ID {
		t.Fatalf("Get returned unit %d, want live v2 unit %d", s.UnitID, v2ID)
	}
	if err := Verify(*s, got); err == nil {
		t.Fatal("Verify: expected failure for version-downgraded row — all three legs can no longer agree")
	}
}

func TestVerifyHappyPath(t *testing.T) {
	st := openTestStore(t)
	id, hash := publishSkill(t, st, "verify-ok", "verified body")
	s, err := Get(st, "verify-ok")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := Verify(*s, hash); err != nil {
		t.Fatalf("Verify happy path: %v", err)
	}
	_ = id
}

// TestVerifyHashMismatchBodyVsAudit is the F12/F7 REQUIRED security negative:
// a DB-writer who tampers the body (and, in the worst case, the row's
// content_hash to match the tampered body) after approval must still fail
// verification against the audit hash, which they cannot also rewrite
// without breaking govern.Verify's chain.
func TestVerifyHashMismatchBodyVsAudit(t *testing.T) {
	st := openTestStore(t)
	_, auditHash := publishSkill(t, st, "tampered-skill", "original body")

	// Simulate a DB-writer tampering BOTH body and content_hash on the same
	// row to look self-consistent — this must STILL fail because Verify
	// checks against auditHash, independent of the row.
	tamperedBody := "evil injected instructions"
	sum := sha256.Sum256([]byte(tamperedBody))
	tamperedRowHash := hex.EncodeToString(sum[:])
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET body = ?, content_hash = ? WHERE name = ?`,
		tamperedBody, tamperedRowHash, "tampered-skill"); err != nil {
		t.Fatalf("simulate tamper: %v", err)
	}

	s, err := Get(st, "tampered-skill")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := Verify(*s, auditHash); err == nil {
		t.Fatal("Verify: expected failure for tampered body+row-hash vs audit hash, got nil")
	}
}

// TestVerifyBodyRowHashMismatch covers the simpler leg: body edited without
// touching content_hash (an even less sophisticated tamper attempt).
func TestVerifyBodyRowHashMismatch(t *testing.T) {
	st := openTestStore(t)
	_, auditHash := publishSkill(t, st, "row-mismatch", "original")
	if _, err := st.DB.Exec(`UPDATE knowledge_units SET body = ? WHERE name = ?`, "edited-body-only", "row-mismatch"); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	s, err := Get(st, "row-mismatch")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := Verify(*s, auditHash); err == nil {
		t.Fatal("Verify: expected failure for body/row-hash mismatch")
	}
}

func TestLocalPathTraversalRejected(t *testing.T) {
	repo := t.TempDir()
	for _, bad := range []string{"../x", "..", "a/b", "/etc/passwd", "", "UPPER", "-leading"} {
		if _, err := LocalPath(repo, bad); err == nil {
			t.Errorf("LocalPath(%q): expected rejection, got nil error", bad)
		}
	}
}

func TestLocalPathSymlinkEscapeRejected(t *testing.T) {
	repo := t.TempDir()
	skillsDir := filepath.Join(repo, ".claude", "skills")
	if err := os.MkdirAll(filepath.Dir(skillsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	// .claude/skills itself is a symlink pointing OUTSIDE the repo.
	if err := os.Symlink(outside, skillsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := LocalPath(repo, "some-skill"); err == nil {
		t.Fatal("LocalPath: expected refusal for .claude/skills symlinked outside repo")
	}
}

func TestLocalPathSymlinkedSkillDirEscapeRejected(t *testing.T) {
	repo := t.TempDir()
	skillsDir := filepath.Join(repo, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	// .claude/skills/evil-skill is a symlink pointing outside the repo.
	if err := os.Symlink(outside, filepath.Join(skillsDir, "evil-skill")); err != nil {
		t.Fatal(err)
	}
	if _, err := LocalPath(repo, "evil-skill"); err == nil {
		t.Fatal("LocalPath: expected refusal for symlinked skill dir escaping repo")
	}
}

func TestLocalPathHappyPathWritesInsideRepo(t *testing.T) {
	repo := t.TempDir()
	path, err := LocalPath(repo, "good-skill")
	if err != nil {
		t.Fatalf("LocalPath: %v", err)
	}
	// repo itself may be a symlink target (e.g. macOS /var -> /private/var
	// on temp dirs) — LocalPath resolves repoRoot first, so compare against
	// the same resolved basis rather than the lexical t.TempDir() string.
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("EvalSymlinks(repo): %v", err)
	}
	want := filepath.Join(realRepo, ".claude", "skills", "good-skill", "SKILL.md")
	if path != want {
		t.Errorf("LocalPath = %s, want %s", path, want)
	}
	if err := Write(path, "hello"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("read back = %q", got)
	}
}

func TestLocalHashMissingFileReturnsEmpty(t *testing.T) {
	h, err := LocalHash(filepath.Join(t.TempDir(), "nope", "SKILL.md"))
	if err != nil {
		t.Fatalf("LocalHash missing: %v", err)
	}
	if h != "" {
		t.Errorf("LocalHash missing = %q, want empty", h)
	}
}

func TestLocalHashMatchesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("abc"))
	want := hex.EncodeToString(sum[:])
	got, err := LocalHash(path)
	if err != nil {
		t.Fatalf("LocalHash: %v", err)
	}
	if got != want {
		t.Errorf("LocalHash = %s, want %s", got, want)
	}
}
