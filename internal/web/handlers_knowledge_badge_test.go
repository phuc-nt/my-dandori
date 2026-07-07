package web

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/learn"
)

// TestKnowledgeEngineerInsufficientBadgeShownNeverHidden (F9): a unit an
// engineer nominated (NominatedBy != the fixed detector actor string) with a
// present-side sample below MinSampleForKnowledge must render the
// "engineer-nominated · chưa đủ dữ liệu" badge on /knowledge/unit/{id} — the
// spec is explicit this must be SHOWN, never hidden, even though the unit is
// otherwise fully viewable.
func TestKnowledgeEngineerInsufficientBadgeShownNeverHidden(t *testing.T) {
	s := testServer(t)
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindSkill, Name: "new-idea-skill", Title: "New idea skill",
		Body: "# steps", NominatedBy: "phuc@example.com", // a human operator, not the detector
	})
	if err != nil {
		t.Fatalf("nominate: %v", err)
	}
	// n_present stays NULL/0 by default (no runs seeded) — below the
	// MinSampleForKnowledge=10 floor.
	rec := get(t, s, "/knowledge/unit/"+strconv.FormatInt(id, 10))
	if rec.Code != http.StatusOK {
		t.Fatalf("unit page status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "engineer-nominated") {
		t.Errorf("badge must be shown for engineer-nominated + insufficient-data unit, got body:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "chưa đủ dữ liệu") {
		t.Error("badge text must include the insufficient-data label")
	}
}

// TestKnowledgeEngineerInsufficientBadgeAbsentForDetector: the detector's
// own nominations (NominatedBy == "dandori-observer") must NOT show the
// "engineer-nominated" badge — it only applies to human-proposed units.
func TestKnowledgeEngineerInsufficientBadgeAbsentForDetector(t *testing.T) {
	s := testServer(t)
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindSkill, Name: "detector-found-skill", Title: "Detector found skill",
		Body: "# steps", NominatedBy: "dandori-observer",
	})
	if err != nil {
		t.Fatalf("nominate: %v", err)
	}
	rec := get(t, s, "/knowledge/unit/"+strconv.FormatInt(id, 10))
	if rec.Code != http.StatusOK {
		t.Fatalf("unit page status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "engineer-nominated") {
		t.Error("detector-nominated unit must not show the engineer-nominated badge")
	}
}

// TestKnowledgeEngineerInsufficientBadgeAbsentOnceEnoughSample: once the
// unit's stored n_present reaches MinSampleForKnowledge, the badge must
// disappear even for an engineer-nominated unit.
func TestKnowledgeEngineerInsufficientBadgeAbsentOnceEnoughSample(t *testing.T) {
	s := testServer(t)
	id, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind: learn.KindSkill, Name: "matured-idea-skill", Title: "Matured idea skill",
		Body: "# steps", NominatedBy: "phuc@example.com",
		Stats: learn.StatsSnapshot{NPresent: 12, DonePresent: 0.8},
	})
	if err != nil {
		t.Fatalf("nominate: %v", err)
	}
	rec := get(t, s, "/knowledge/unit/"+strconv.FormatInt(id, 10))
	if rec.Code != http.StatusOK {
		t.Fatalf("unit page status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "engineer-nominated") {
		t.Error("badge must not show once n_present >= MinSampleForKnowledge")
	}
}
