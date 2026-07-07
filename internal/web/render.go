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

	"github.com/phuc-nt/dandori/internal/learn"
)

//go:embed templates static
var uiFS embed.FS

// pages lists every page template; each is parsed with the layout and all
// partials so fragments can be rendered directly by name.
var pageNames = []string{
	"standup", "dash_org", "dash_project", "dash_agent",
	"runs", "run_detail", "run_compare", "reviews", "budgets", "provenance", "rules", "spikes", "playbooks",
	"chat", "exec_home", "contexts", "launch", "gate_thresholds", "wallboard",
	"settings_integrations", "welcome", "risk", "insights", "login", "knowledge",
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
	// dict builds a map[string]any from alternating key/value args, so a
	// defined-template invoked with a nested value (e.g. {{template "x" .Run}})
	// can still carry root-level fields like IsAdmin alongside it.
	"dict": func(pairs ...any) (map[string]any, error) {
		if len(pairs)%2 != 0 {
			return nil, fmt.Errorf("dict: odd number of arguments")
		}
		m := make(map[string]any, len(pairs)/2)
		for i := 0; i < len(pairs); i += 2 {
			key, ok := pairs[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict: key at index %d is not a string", i)
			}
			m[key] = pairs[i+1]
		}
		return m, nil
	},
	// intstatus finds one integration's health by name for the settings page.
	"intstatus": func(h Health, name string) IntegrationHealth {
		for _, i := range h.Integrations {
			if i.Name == name {
				return i
			}
		}
		return IntegrationHealth{Name: name}
	},
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
	// derefInt is deref's *int twin — KnowledgeUnit.NPresent/NAbsent are
	// *int (not *int64, unlike SupersedesID/RefID), so deref's signature
	// does not fit; a second tiny helper is simpler than changing either
	// struct's field type just for template access.
	"derefInt": func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	},
	"usd":    func(v float64) string { return fmt.Sprintf("$%.2f", v) },
	"pct":    func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
	"mul100": func(v float64) float64 { return v * 100 },
	"score": func(v float64) string { return fmt.Sprintf("%.0f", v) },
	"short": func(s string) string {
		if len(s) > 12 {
			return s[:12]
		}
		return s
	},
	// FormatWilson renders a proportion with its 95% Wilson CI + sample size,
	// e.g. "75% (CI 30–95%, n=4)" — used everywhere a done-rate/acceptance
	// ratio is shown (insights sections + F9 leaderboard/metrics surfaces).
	"FormatWilson": learn.FormatWilson,
	// wilsonFromMetric reconstructs the (successes, n) pair a Metric only
	// exposes as a pre-computed percent (learn.Metric.Value) + its RunIDs
	// slice (one id per trial in the same denominator the percent was
	// computed over — success()/autonomy() in metrics_calc.go both append
	// exactly one id per counted run). n = len(runIDs); successes = the exact
	// inverse of `value = 100*successes/n`, so this is arithmetic reconstruction
	// of an existing computation, not a new metric — no learn struct changes.
	// Used for Success/Autonomy only: Acceptance's EventIDs mixes edits and
	// rejected-subset ids (not a clean trial count) so it is deliberately not
	// wired here.
	"wilsonFromMetric": func(value float64, runIDs []string) string {
		n := len(runIDs)
		if n == 0 {
			return learn.FormatWilson(0, 0)
		}
		successes := int(value/100*float64(n) + 0.5)
		return learn.FormatWilson(successes, n)
	},
	// ruleIntentRetire/ruleIntentScopeUp (H1) drive the prominent warning
	// banner on the knowledge unit detail page for kind=rule, so an admin
	// about to click "Duyệt publish" sees the actual pinned effect (retire
	// vs scope-up vs plain enable) before deciding — same intent the applier
	// (applyKnowledgeRuleWrite) will execute.
	"ruleIntentRetire":  func(name string) bool { return learn.RuleIntentFromName(name) == learn.RuleIntentRetire },
	"ruleIntentScopeUp": func(name string) bool { return learn.RuleIntentFromName(name) == learn.RuleIntentScopeUp },
	// knowledgeDone renders a live-recomputed done-rate as a whole percent
	// (the CI is shown alongside it separately on the suggest card, since
	// UnitSuggestion already carries pre-rounded whole-percent CI bounds —
	// this only needs the point estimate, not another FormatWilson call).
	"knowledgeDone": func(rate float64, n int) string {
		if n == 0 {
			return "— (chưa có mẫu)"
		}
		return fmt.Sprintf("%.0f%%", rate*100)
	},
	// knowledgeEngineerInsufficient reports whether a unit was engineer-
	// nominated (NominatedBy != the fixed detector actor string) AND its
	// present-side sample is below the knowledge-nominate floor — the F9
	// "engineer-nominated · chưa đủ dữ liệu" badge (spec: show, never hide).
	"knowledgeEngineerInsufficient": func(nominatedBy string, nPresent int) bool {
		return nominatedBy != "" && nominatedBy != "dandori-observer" && nPresent < learn.MinSampleForKnowledge
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
		// Every page needs to know whether to render mutation buttons/forms
		// server-side (viewer sees none — 403 backstop in role_middleware.go
		// is not a UI substitute). Centralized here like Mode/KillOn so no
		// handler can forget to set it.
		if _, set := m["IsAdmin"]; !set {
			m["IsAdmin"] = IsAdmin(req)
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
// Several fragments (runs table rows, reviews queue, budgets table) carry
// their own mutation buttons/forms and are swapped in directly without going
// through render()'s layout path, so IsAdmin must be injected here too —
// otherwise a viewer polling/refreshing one of these fragments would see
// admin-only buttons render regardless of role.
func (s *Server) renderFragment(w http.ResponseWriter, req *http.Request, page, fragment string, data any) {
	t, ok := s.tmpl.pages[page]
	if !ok {
		http.Error(w, "unknown page "+page, 500)
		return
	}
	if m, ok := data.(map[string]any); ok {
		if _, set := m["IsAdmin"]; !set {
			m["IsAdmin"] = IsAdmin(req)
		}
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
