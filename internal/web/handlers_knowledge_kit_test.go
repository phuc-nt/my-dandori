package web

import (
	"strconv"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/observer"
)

// P4 F1/H1: /reviews for a kind=kit approval must show the manifest hash
// being approved (not the live unit row) and every per-file body, recomputed
// from knowledge_kit_files — the same rows the applier re-verifies against,
// so a reviewer approving KnowledgeContentHash can also read what it covers.
func TestReviewsRendersKitManifestAndFiles(t *testing.T) {
	s := testServer(t)
	files := []learn.KitFileInput{
		{Path: "agents/reviewer.md", Body: "review agent instructions body text"},
		{Path: "rules/dev-rules.md", Body: "dev rule content text here"},
	}
	id, err := learn.NominateUnitTx(s.Store, learn.KitNominateParams{
		Name: "review-kit", Title: "Review Kit", Files: files, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(s.Store, id, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := learn.RequestPublish(s.Store, observer.RequestAction, id, "tester"); err != nil {
		t.Fatal(err)
	}
	u, err := learn.GetUnit(s.Store, id)
	if err != nil || u == nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/reviews")
	body := rec.Body.String()
	if !strings.Contains(body, u.ContentHash) {
		t.Error("/reviews did not render the manifest content_hash being approved (H1)")
	}
	for _, f := range files {
		if !strings.Contains(body, f.Path) {
			t.Errorf("/reviews did not list kit file path %q", f.Path)
		}
		if !strings.Contains(body, f.Body) {
			t.Errorf("/reviews did not render full body for kit file %q", f.Path)
		}
	}
}

// Same guarantee on /knowledge/unit/:id — the detail page must also show the
// manifest file list with full expandable bodies, independent of /reviews.
func TestKnowledgeUnitDetailRendersKitFiles(t *testing.T) {
	s := testServer(t)
	files := []learn.KitFileInput{
		{Path: "skills/mining/SKILL.md", Body: "skill body content for mining"},
	}
	id, err := learn.NominateUnitTx(s.Store, learn.KitNominateParams{
		Name: "detail-kit", Title: "Detail Kit", Files: files, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/knowledge/unit/"+strconv.FormatInt(id, 10))
	body := rec.Body.String()
	if !strings.Contains(body, "skills/mining/SKILL.md") {
		t.Error("/knowledge/unit did not list the kit's file path")
	}
	if !strings.Contains(body, "skill body content for mining") {
		t.Error("/knowledge/unit did not render the kit file's full body")
	}
}

// Version-bump: a v2 kit that supersedes v1 must render a per-file diff, not
// just a fresh manifest dump — added/removed/changed files each need a
// distinct visible marker so a reviewer sees exactly what moved.
func TestKnowledgeUnitDetailRendersKitVersionDiff(t *testing.T) {
	s := testServer(t)
	v1Files := []learn.KitFileInput{
		{Path: "agents/reviewer.md", Body: "original reviewer body"},
		{Path: "rules/old-only.md", Body: "will be removed in v2"},
	}
	id1, err := learn.NominateUnitTx(s.Store, learn.KitNominateParams{
		Name: "diff-kit", Title: "Diff Kit", Files: v1Files, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := learn.SubmitForReview(s.Store, id1, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := learn.RequestPublish(s.Store, observer.RequestAction, id1, "tester"); err != nil {
		t.Fatal(err)
	}
	var apprID int64
	s.Store.DB.QueryRow(`SELECT id FROM approvals WHERE action LIKE 'observer:knowledge-publish:%' ORDER BY id DESC LIMIT 1`).Scan(&apprID)
	if _, err := govern.Decide(s.Store, apprID, true, "tester@console", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.RunObserverApplier(s.Store); err != nil {
		t.Fatal(err)
	}

	v2Files := []learn.KitFileInput{
		{Path: "agents/reviewer.md", Body: "CHANGED reviewer body for v2"},
		{Path: "agents/new-file.md", Body: "brand new file added in v2"},
	}
	id2, err := learn.NominateUnitTx(s.Store, learn.KitNominateParams{
		Name: "diff-kit", Title: "Diff Kit v2", Files: v2Files, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/knowledge/unit/"+strconv.FormatInt(id2, 10))
	if rec.Code != 200 {
		t.Fatalf("/knowledge/unit/%d status=%d body=%s", id2, rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "agents/new-file.md") {
		t.Error("kit version diff did not show the added file")
	}
	if !strings.Contains(body, "rules/old-only.md") {
		t.Error("kit version diff did not show the removed file")
	}
	if !strings.Contains(body, "agents/reviewer.md") {
		t.Error("kit version diff did not show the changed file")
	}
}
