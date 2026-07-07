package web

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/phuc-nt/dandori/internal/learn"
)

// seedPublishedSkill nominates a skill unit and force-transitions it straight
// to state=published (bypassing submit/review — this file only exercises the
// adopt-skill handler's write, not the review workflow itself).
func seedPublishedSkill(t *testing.T, s *Server, name, title string) int64 {
	t.Helper()
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindSkill, Name: name, Title: title,
		Body:        "# " + title + "\nsteps...",
		NominatedBy: "dandori-observer",
	})
	if err != nil {
		t.Fatalf("nominate: %v", err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE knowledge_units SET state='published' WHERE id=?`, id); err != nil {
		t.Fatalf("force-publish: %v", err)
	}
	return id
}

// TestHandleKnowledgeAdoptSkillRecordsInstalledFalse: the suggest-card
// adopt-intent click (F4) must call learn.RecordUnitAdoption with
// installed=false — a click only marks intent, never "pulled to disk."
// installed only flips true from the actual `dandori skill pull` CLI path.
func TestHandleKnowledgeAdoptSkillRecordsInstalledFalse(t *testing.T) {
	s := testServer(t)
	id := seedPublishedSkill(t, s, "checkout-flow-fix", "Checkout flow fix skill")

	rec := postForm(t, s, "/knowledge/unit/"+strconv.FormatInt(id, 10)+"/adopt-skill", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("adopt-skill status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var installed int
	var unitID int64
	if err := s.Store.DB.QueryRow(`SELECT unit_id, installed FROM adoptions WHERE unit_id = ?`, id).
		Scan(&unitID, &installed); err != nil {
		t.Fatalf("adoptions row: %v", err)
	}
	if unitID != id {
		t.Errorf("unit_id = %d, want %d", unitID, id)
	}
	if installed != 0 {
		t.Errorf("installed = %d, want 0 (false) — suggest-card click is intent-only", installed)
	}
}

// TestHandleKnowledgeAdoptSkillRejectsNonSkillKind: adopt-skill is
// skill-kind only — a context/playbook unit id must 404, not silently
// record an adoption for the wrong kind.
func TestHandleKnowledgeAdoptSkillRejectsNonSkillKind(t *testing.T) {
	s := testServer(t)
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindContext, Name: "checkout-notes", Title: "Checkout notes",
		RefKind: "context_version", RefID: 1, NominatedBy: "dandori-observer",
	})
	if err != nil {
		t.Fatalf("nominate: %v", err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE knowledge_units SET state='published' WHERE id=?`, id); err != nil {
		t.Fatalf("force-publish: %v", err)
	}

	rec := postForm(t, s, "/knowledge/unit/"+strconv.FormatInt(id, 10)+"/adopt-skill", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("adopt-skill on context unit → %d, want 404", rec.Code)
	}
}
