package web

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/store"
)

// savedViewNameMaxLen caps the user-supplied view name (L2): long enough for
// a descriptive label, short enough to keep the dropdown and table sane.
const savedViewNameMaxLen = 80

// SavedViewRow is one named, filtered view as shown in a page's dropdown.
type SavedViewRow struct {
	ID          int64
	Name        string
	Page        string
	FiltersJSON string
	CreatedAt   string
}

// querySavedViews lists saved views for one page (e.g. "runs"), newest first.
func (s *Server) querySavedViews(page string) ([]SavedViewRow, error) {
	rows, err := s.Store.DB.Query(`SELECT id, name, page, filters_json, created_at
		FROM saved_views WHERE page = ? ORDER BY id DESC`, page)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedViewRow
	for rows.Next() {
		var v SavedViewRow
		if err := rows.Scan(&v.ID, &v.Name, &v.Page, &v.FiltersJSON, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// handleSavedViewSave persists the current filter querystring under a name
// (UG3). The querystring is stored raw; apply re-parses it with std net/url
// and only ever redirects to /runs, so this is not an open redirect.
func (s *Server) handleSavedViewSave(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	page := r.FormValue("page")
	filters := r.FormValue("querystring")
	if name == "" {
		http.Error(w, "name required", 400)
		return
	}
	if len(name) > savedViewNameMaxLen {
		http.Error(w, "name too long", 400)
		return
	}
	if page == "" {
		page = "runs"
	}
	// Re-parse to confirm the stored value is a well-formed querystring, not
	// something that would break the eventual redirect target.
	if _, err := url.ParseQuery(filters); err != nil {
		http.Error(w, "invalid filters", 400)
		return
	}
	if _, err := s.Store.DB.Exec(
		`INSERT INTO saved_views(name, page, filters_json, created_at) VALUES(?, ?, ?, ?)`,
		name, page, filters, store.Now()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	redirectBack(w, r, "/"+page)
}

// handleSavedViewApply loads the stored querystring and redirects to the
// owning page with it applied. The target is always "/"+page (fixed set of
// known pages) — never a user-supplied URL — so this cannot become an open
// redirect.
func (s *Server) handleSavedViewApply(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var page, filters string
	err = s.Store.DB.QueryRow(`SELECT page, filters_json FROM saved_views WHERE id = ?`, id).
		Scan(&page, &filters)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if page != "runs" {
		// Only /runs consumes saved filters today; guard against a future page
		// value smuggling something unexpected into the redirect path.
		http.Error(w, "unsupported view page", 400)
		return
	}
	q, err := url.ParseQuery(filters)
	if err != nil {
		http.Error(w, "corrupt filters", 500)
		return
	}
	target := "/runs"
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleSavedViewDelete removes a saved view and refreshes the dropdown.
func (s *Server) handleSavedViewDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if _, err := s.Store.DB.Exec(`DELETE FROM saved_views WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	redirectBack(w, r, "/runs")
}
