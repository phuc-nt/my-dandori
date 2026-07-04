package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestSavedViewSaveListApplyRoundtrip(t *testing.T) {
	s := testServer(t)
	s.registerPhase06Routes()

	rec := postForm(t, s, "/views/save", url.Values{
		"name": {"chỉ agent-1"}, "page": {"runs"}, "querystring": {"agent=agent-1&status=running"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /views/save → %d body=%s", rec.Code, rec.Body)
	}

	views, err := s.querySavedViews("runs")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Name != "chỉ agent-1" {
		t.Fatalf("querySavedViews = %+v, want 1 view named 'chỉ agent-1'", views)
	}

	rec = get(t, s, "/views/"+strconv.FormatInt(views[0].ID, 10)+"/apply")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /views/{id}/apply → %d body=%s", rec.Code, rec.Body)
	}
	loc := rec.Header().Get("Location")
	if loc != "/runs?agent=agent-1&status=running" {
		t.Errorf("apply redirect Location = %q", loc)
	}
}

func TestSavedViewDeleteRemoves(t *testing.T) {
	s := testServer(t)
	s.registerPhase06Routes()
	postForm(t, s, "/views/save", url.Values{"name": {"tmp"}, "page": {"runs"}, "querystring": {"status=done"}})
	views, _ := s.querySavedViews("runs")
	if len(views) != 1 {
		t.Fatalf("setup: want 1 saved view, got %d", len(views))
	}

	rec := postForm(t, s, "/views/"+strconv.FormatInt(views[0].ID, 10)+"/delete", url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /views/{id}/delete → %d", rec.Code)
	}
	views, _ = s.querySavedViews("runs")
	if len(views) != 0 {
		t.Errorf("after delete, saved views = %d, want 0", len(views))
	}
}

// TestSavedViewNameEscapedInDropdown is the L2 regression test: a saved-view
// name containing HTML metacharacters must render as inert text in the
// runs-page dropdown, never as executable markup.
func TestSavedViewNameEscapedInDropdown(t *testing.T) {
	s := testServer(t)
	s.registerPhase06Routes()
	postForm(t, s, "/views/save", url.Values{
		"name": {"<script>alert(1)</script>"}, "page": {"runs"}, "querystring": {""},
	})

	rec := get(t, s, "/runs")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /runs → %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("saved-view name rendered unescaped — XSS risk")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped name in dropdown, body=%s", body)
	}
}

func TestSavedViewSaveRejectsOverLongName(t *testing.T) {
	s := testServer(t)
	s.registerPhase06Routes()
	longName := strings.Repeat("a", savedViewNameMaxLen+1)
	rec := postForm(t, s, "/views/save", url.Values{"name": {longName}, "page": {"runs"}, "querystring": {""}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("over-length name → %d, want 400", rec.Code)
	}
	views, _ := s.querySavedViews("runs")
	if len(views) != 0 {
		t.Error("over-length name must not be persisted")
	}
}

func TestSavedViewSaveRejectsEmptyName(t *testing.T) {
	s := testServer(t)
	s.registerPhase06Routes()
	rec := postForm(t, s, "/views/save", url.Values{"name": {""}, "page": {"runs"}, "querystring": {""}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty name → %d, want 400", rec.Code)
	}
}

func TestSavedViewApplyUnknownIDNotFound(t *testing.T) {
	s := testServer(t)
	s.registerPhase06Routes()
	rec := get(t, s, "/views/999/apply")
	if rec.Code != http.StatusNotFound {
		t.Errorf("apply unknown id → %d, want 404", rec.Code)
	}
}
