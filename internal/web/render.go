package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates static
var uiFS embed.FS

// pages lists every page template; each is parsed with the layout and all
// partials so fragments can be rendered directly by name.
var pageNames = []string{
	"standup", "dash_org", "dash_project", "dash_agent",
	"runs", "run_detail", "run_compare", "reviews", "budgets", "provenance", "rules", "spikes", "playbooks",
	"chat", "exec_home",
}

type renderer struct {
	pages map[string]*template.Template
}

// navItem is the data one sidebar link needs: which page is active, this
// item's page key, its href, icon and label (+ optional badge count).
type navItem struct {
	Active, Item, Href, Icon, Label string
	Badge                           int
}

var tmplFuncs = template.FuncMap{
	"list": func(items ...any) []any { return items },
	"navctx": func(active, item, href, icon, label string) navItem {
		return navItem{Active: active, Item: item, Href: href, Icon: icon, Label: label}
	},
	"navctx4": func(active, item, href, icon, label string, badge int) navItem {
		return navItem{Active: active, Item: item, Href: href, Icon: icon, Label: label, Badge: badge}
	},
	"deref": func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	},
	"usd":   func(v float64) string { return fmt.Sprintf("$%.2f", v) },
	"pct":   func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
	"score": func(v float64) string { return fmt.Sprintf("%.0f", v) },
	"short": func(s string) string {
		if len(s) > 12 {
			return s[:12]
		}
		return s
	},
	"statusColor": func(s string) string {
		switch s {
		case "done", "approved":
			return "text-green-600"
		case "failed", "killed", "rejected":
			return "text-red-600"
		case "running", "pending":
			return "text-amber-600"
		}
		return "text-gray-500"
	},
}

// newRenderer parses templates from the embedded FS, or from templatesDir on
// disk when set (dev mode: edit template → F5, no rebuild).
func newRenderer(templatesDir string) (*renderer, error) {
	var root fs.FS = uiFS
	if templatesDir != "" {
		root = os.DirFS(filepath.Dir(templatesDir))
	}
	r := &renderer{pages: map[string]*template.Template{}}
	for _, name := range pageNames {
		t, err := template.New("layout.html").Funcs(tmplFuncs).ParseFS(root,
			"templates/layout.html",
			"templates/partials/*.html",
			"templates/pages/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		r.pages[name] = t
	}
	return r, nil
}

// render writes a full page (layout) or, for HTMX requests asking a specific
// fragment, just that named template.
func (s *Server) render(w http.ResponseWriter, req *http.Request, page string, data any) {
	t, ok := s.tmpl.pages[page]
	if !ok {
		http.Error(w, "unknown page "+page, 500)
		return
	}
	// The sidebar needs Mode (which nav to draw), Page (which item is active)
	// and KillOn on EVERY page. Inject them centrally so handlers don't each
	// have to remember — a missing Mode is what made /chat show the wrong nav.
	if m, ok := data.(map[string]any); ok {
		mode := modeFrom(req)
		if _, set := m["Mode"]; !set {
			m["Mode"] = mode
		}
		if _, set := m["KillOn"]; !set {
			m["KillOn"] = s.Store.Setting("kill_switch_global") == "1"
		}
		// The exec sidebar shows a "cần duyệt" badge on every exec page.
		if m["Mode"] == "exec" {
			if _, set := m["InboxCount"]; !set {
				m["InboxCount"] = s.ceoInboxCount()
			}
		}
	}
	name := "layout.html"
	if frag := req.URL.Query().Get("fragment"); frag != "" && req.Header.Get("HX-Request") == "true" {
		name = frag
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		// Headers already sent; log into the body for visibility in dev.
		fmt.Fprintf(w, "<!-- render error: %s -->", template.HTMLEscapeString(err.Error()))
	}
}

// renderFragment always renders one named template (HTMX swap responses).
func (s *Server) renderFragment(w http.ResponseWriter, page, fragment string, data any) {
	t, ok := s.tmpl.pages[page]
	if !ok {
		http.Error(w, "unknown page "+page, 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, fragment, data); err != nil {
		fmt.Fprintf(w, "<!-- render error: %s -->", template.HTMLEscapeString(err.Error()))
	}
}

// staticHandler serves embedded static assets.
func staticHandler() http.Handler {
	sub, _ := fs.Sub(uiFS, "static")
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}

// isHTMX reports whether the request came from an HTMX swap.
func isHTMX(r *http.Request) bool { return strings.EqualFold(r.Header.Get("HX-Request"), "true") }
