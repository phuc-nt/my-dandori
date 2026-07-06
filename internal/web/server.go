// Package web serves the Dandori operations console: server-rendered
// html/template pages progressively enhanced with HTMX fragments.
// Principle: every number has a verb next to it.
package web

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/runner"
	"github.com/phuc-nt/dandori/internal/store"
)

type Server struct {
	Cfg   *config.Config
	Store *store.Store
	// FlagSink, when set by the integrations wiring, receives new flag ids
	// (flag → Jira ticket). Nil = no external leg.
	FlagSink func(flagID int64)
	// ReportSink publishes the fleet report to Confluence when wired.
	// Returns page id, "dry-run", or "" (deduped today).
	ReportSink func() (string, error)
	// Launcher runs agent-runs launched from the console (v6). Set by serve
	// after construction; nil when launch isn't wired (tests).
	Launcher *runner.Launcher

	mux  *chi.Mux
	tmpl *renderer
}

// New builds the console server. templatesDir != "" reads templates from disk
// (dev mode); empty uses the embedded FS.
func New(cfg *config.Config, st *store.Store, templatesDir ...string) (*Server, error) {
	dir := ""
	if len(templatesDir) > 0 {
		dir = templatesDir[0]
	}
	tmpl, err := newRenderer(dir)
	if err != nil {
		return nil, err
	}
	s := &Server{Cfg: cfg, Store: st, mux: chi.NewRouter(), tmpl: tmpl}
	s.mux.Use(middleware.Recoverer)
	s.mux.Use(s.originGuard)
	s.routes()
	return s, nil
}

// originGuard rejects requests whose Host is not this console (DNS-rebinding
// protection) and cross-origin mutations (drive-by CSRF against the approval
// gate / kill switch). The console has no auth by design (localhost bind), so
// this boundary is what keeps a random browser tab from driving GOVERN.
func (s *Server) originGuard(next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(s.Cfg.Listen)
	allowed := map[string]bool{
		s.Cfg.Listen:        true,
		"localhost:" + port: true,
		"127.0.0.1:" + port: true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// A same-origin browser form always sends a real Origin. A
			// sandboxed iframe forces Origin: null, and some drive-by vectors
			// omit it — both must be rejected on mutations, else an off-origin
			// page can POST here (e.g. plant credentials on /settings). Absent
			// Sec-Fetch-Site is the escape hatch for non-browser clients
			// (curl/tests) which are trusted on localhost.
			o := r.Header.Get("Origin")
			switch {
			case o == "" || o == "null":
				if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
					http.Error(w, "cross-origin request rejected", http.StatusForbidden)
					return
				}
			default:
				if u, err := url.Parse(o); err != nil || !allowed[u.Host] {
					http.Error(w, "cross-origin request rejected", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Collect(s.Cfg, s.Store))
	})
	s.mux.Handle("/static/*", staticHandler())

	s.mux.Get("/", s.handleHome) // mode-aware: exec home or standup
	s.mux.Get("/standup", s.handleStandup)
	s.mux.Post("/mode", s.handleSetMode)
	s.mux.Get("/exec", s.handleExecHome)
	s.mux.Post("/exec/approve/{id}", s.handleExecApprove)
	s.mux.Post("/exec/insight/{id}/dismiss", s.handleExecDismiss)
	s.mux.Get("/insights", s.handleInsights)
	s.mux.Get("/dash/org", s.handleDashOrg)
	s.mux.Get("/dash/project/{project}", s.handleDashProject)
	s.mux.Get("/dash/agent/{agent}", s.handleDashAgent)
	s.mux.Post("/agents/{agent}/band", s.handleSetBand)
	s.mux.Get("/agents/{agent}/review", s.handleAgentAIReview)

	s.mux.Get("/launch", s.handleLaunchForm)
	s.mux.Post("/launch", s.handleLaunch)
	s.mux.Get("/runs/{id}/retry", s.handleRetryForm)
	s.mux.Post("/runs/{id}/retry", s.handleRetry)

	s.mux.Get("/runs", s.handleRuns)
	s.mux.Post("/runs/bulk-kill", s.handleBulkKill)
	s.mux.Post("/runs/bulk-budget", s.handleBulkBudget)
	s.mux.Get("/runs/compare", s.handleRunCompare)
	s.mux.Get("/spikes", s.handleSpikes)
	s.mux.Get("/runs/{id}", s.handleRunDetail)
	s.mux.Get("/runs/{id}/status-fragment", s.handleRunStatusFragment)
	s.mux.Get("/runs/{id}/log-tail", s.handleRunLogTail)
	s.mux.Post("/runs/{id}/kill", s.handleRunKill)
	s.mux.Post("/runs/{id}/task-key", s.handleRunTaskKey)
	s.mux.Post("/runs/{id}/flag", s.handleRunFlag)
	s.mux.Post("/runs/{id}/playbook", s.handlePlaybookCreate)
	s.mux.Get("/playbooks", s.handlePlaybooks)
	s.mux.Post("/playbooks/{id}/adopt", s.handlePlaybookAdopt)

	s.mux.Get("/contexts", s.handleContexts)
	s.mux.Post("/contexts/save", s.handleContextSave)
	s.mux.Get("/contexts/{layer}/{target}/history", s.handleContextHistory)
	s.mux.Get("/contexts/diff", s.handleContextDiff)
	s.mux.Post("/contexts/rollback", s.handleContextRollback)
	s.mux.Post("/contexts/promote", s.handleContextPromote)
	s.mux.Get("/contexts/effective", s.handleContextEffective)
	s.mux.Get("/contexts/drive-search", s.handleDriveSearch)
	s.mux.Get("/contexts/drive-review", s.handleDriveReview)
	s.mux.Post("/contexts/drive-import", s.handleDriveImport)

	s.mux.Get("/reviews", s.handleReviews)
	s.mux.Post("/reviews/{id}/decide", s.handleReviewDecide)

	s.mux.Get("/chat", s.handleChatPage)
	s.mux.Post("/chat/message", s.handleChatMessage)

	s.mux.Get("/budgets", s.handleBudgets)
	s.mux.Post("/budgets", s.handleBudgetSet)

	s.mux.Get("/provenance", s.handleProvenance)
	s.mux.Get("/rules", s.handleRules)
	s.mux.Post("/rules", s.handleRuleCreate)
	s.mux.Post("/rules/simulate", s.handleRuleSimulate)
	s.mux.Post("/rules/{id}/toggle", s.handleRuleToggle)
	s.mux.Post("/rules/{id}/delete", s.handleRuleDelete)

	s.mux.Post("/api/kill", s.handleGlobalKill)
	// POST: the export appends an audit entry (side effect) — GET would let a
	// drive-by <img src> spam the append-only chain past the origin guard.
	s.mux.Post("/export/compliance", s.handleComplianceExport)
	s.mux.Post("/reports/confluence", s.handleConfluenceReport)

	// v8 onboarding: credential settings + test connection (write-through .env,
	// restart-required for workers).
	s.mux.Get("/settings/integrations", s.handleSettings)
	s.mux.Post("/settings/integrations/{name}", s.handleSettingsSave)
	s.mux.Post("/settings/integrations/{name}/test", s.handleSettingsTest)

	// v8 onboarding wizard.
	s.mux.Get("/welcome", s.handleWelcome)
	s.mux.Get("/welcome/fragment", s.handleWelcomeFragment)

	// v8 risk overview (reuses reviews_pending + budgets_table partials).
	s.mux.Get("/risk", s.handleRisk)
	s.mux.Get("/risk/fragment", s.handleRiskFragment)

	s.registerPhase02Routes()
	s.registerPhase03Routes()
	s.registerPhase05Routes()
	s.registerPhase06Routes()
}

func (s *Server) Handler() http.Handler { return s.mux }

// ListenAndServe blocks serving the console on cfg.Listen.
func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.Cfg.Listen, s.mux)
}
