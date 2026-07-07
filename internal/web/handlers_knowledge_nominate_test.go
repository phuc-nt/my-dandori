package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store/testseed"
)

// TestHandleKnowledgeNominatePersistsOrigin is the C2 proof: origin/
// origin_model/provenance_run_ids posted on the nominate form must land on
// the DB row — before this fix the handler silently dropped all three and
// every nomination landed origin='human' regardless of what was posted.
func TestHandleKnowledgeNominatePersistsOrigin(t *testing.T) {
	s := testServer(t)
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "run-1", "a1", "done", 0, 0.5)
	testseed.Run(t, s.Store, "run-2", "a1", "done", 0, 0.3)

	form := url.Values{
		"kind":               {"context"},
		"name":               {"ai-drafted-note"},
		"title":              {"AI drafted note"},
		"body":               {"some body content"},
		"origin":             {"ai-draft"},
		"origin_model":       {"anthropic/claude-3.5"},
		"provenance_run_ids": {"run-1, run-2"},
	}
	rec := postForm(t, s, "/knowledge/nominate", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("nominate status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var origin, originModel, provJSON string
	if err := s.Store.DB.QueryRow(
		`SELECT origin, COALESCE(origin_model,''), provenance_run_ids FROM knowledge_units WHERE name = 'ai-drafted-note'`,
	).Scan(&origin, &originModel, &provJSON); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if origin != "ai-draft" {
		t.Errorf("origin = %q, want ai-draft", origin)
	}
	if originModel != "anthropic/claude-3.5" {
		t.Errorf("origin_model = %q, want anthropic/claude-3.5", originModel)
	}
	if provJSON != `["run-1","run-2"]` {
		t.Errorf("provenance_run_ids = %q, want [\"run-1\",\"run-2\"]", provJSON)
	}
}

// TestHandleKnowledgeNominateRejectsForgedProvenance is the C2 security
// proof: a provenance_run_ids value that does not match a real `runs` row
// must reject the WHOLE nominate (no row created), never silently drop the
// bad id and keep the nomination — otherwise a low-trust nominator (viewer,
// F9) could forge a "đã đúc" badge pointing at evidence that never produced
// this content.
func TestHandleKnowledgeNominateRejectsForgedProvenance(t *testing.T) {
	s := testServer(t)

	form := url.Values{
		"kind":               {"context"},
		"name":               {"forged-note"},
		"title":              {"Forged note"},
		"body":               {"some body content"},
		"origin":             {"ai-draft"},
		"provenance_run_ids": {"does-not-exist"},
	}
	rec := postForm(t, s, "/knowledge/nominate", form)
	// Rejection renders as a banner fragment (contextBanner, same convention
	// as the secret-fragment/slug/dup-draft rejects a few lines above this
	// handler) — HTTP 200 by design (HTMX swaps it into place), so the real
	// assertion is "no row created," not the status code.
	if rec.Code != http.StatusOK {
		t.Fatalf("banner render status = %d, want 200: body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "không tồn tại") {
		t.Errorf("expected rejection banner mentioning the unknown run id, got: %s", rec.Body.String())
	}

	var n int
	_ = s.Store.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE name = 'forged-note'`).Scan(&n)
	if n != 0 {
		t.Errorf("forged-provenance nominate created %d row(s), want 0", n)
	}
}

// TestHandleKnowledgeNominateEmptyOriginDefaultsHuman: a plain nominate with
// no origin form field still lands origin='human' (the pre-v13 behavior),
// proving the C2 edit is additive and does not change the default path.
func TestHandleKnowledgeNominateEmptyOriginDefaultsHuman(t *testing.T) {
	s := testServer(t)

	form := url.Values{
		"kind":  {"context"},
		"name":  {"plain-human-note"},
		"title": {"Plain human note"},
		"body":  {"some body content"},
	}
	rec := postForm(t, s, "/knowledge/nominate", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("nominate status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var origin string
	if err := s.Store.DB.QueryRow(
		`SELECT origin FROM knowledge_units WHERE name = 'plain-human-note'`,
	).Scan(&origin); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if origin != "human" {
		t.Errorf("origin = %q, want human", origin)
	}
}

// TestParseProvenanceRunIDsDistinguishesDBErrorFromForged: a transient DB
// failure (connection closed, disk error, etc.) during the runs-row lookup
// must NOT be mislabeled "forged" — that message accuses the nominator of
// trying to fake evidence, which is wrong and misleading when the real cause
// is a DB-layer problem on our side. Closing the store's connection before
// calling parseProvenanceRunIDs directly forces the lookup's Scan to return a
// non-ErrNoRows error, exercising the "default" branch of the switch.
func TestParseProvenanceRunIDsDistinguishesDBErrorFromForged(t *testing.T) {
	s := testServer(t)
	s.Store.DB.Close()

	_, err := s.parseProvenanceRunIDs("some-run-id")
	if err == nil {
		t.Fatal("want error after DB close, got nil")
	}
	if strings.Contains(err.Error(), "làm giả bằng chứng") {
		t.Errorf("error=%q wrongly labeled a DB-layer failure as forged provenance", err.Error())
	}
	if !strings.Contains(err.Error(), "lỗi kiểm tra") {
		t.Errorf("error=%q, want a distinct DB-error message (lỗi kiểm tra)", err.Error())
	}
}
