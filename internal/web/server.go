// Package web serves the Dandori operations console: server-rendered
// html/template pages progressively enhanced with HTMX fragments.
// Principle: every number has a verb next to it.
package web

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/phuc-nt/dandori/internal/auth"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/runner"
	"github.com/phuc-nt/dandori/internal/store"
)

// gcInterval controls how often RunGC sweeps expired sessions and stale
// rate-limiter entries. Hourly is frequent enough to bound growth on a
// long-running server without adding meaningful DB/CPU load.
const gcInterval = time.Hour

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

	mux       *chi.Mux
	tmpl      *renderer
	sessions  *auth.SessionStore
	rateLimit *auth.RateLimiter
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
	s := &Server{
		Cfg: cfg, Store: st, mux: chi.NewRouter(), tmpl: tmpl,
		sessions:  auth.NewSessionStore(st),
		rateLimit: auth.NewRateLimiter(),
	}
	s.mux.Use(middleware.Recoverer)
	s.mux.Use(s.originGuard)
	s.mux.Use(s.sessionMiddleware) // after originGuard: CSRF/host checks run first
	s.routes()
	s.registerAuthRoutes()
	return s, nil
}

// RunGC sweeps expired sessions (SQLite) and stale rate-limiter entries
// (in-memory maps) on gcInterval until ctx is cancelled. Both stores grow
// unbounded without this: sessions past absolute expiry are never deleted
// otherwise, and rate-limiter buckets/failures accumulate per unique
// ip/username seen. Call as a goroutine from serve bootstrap.
func (s *Server) RunGC(ctx context.Context) {
	t := time.NewTicker(gcInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.sessions.GC(); err != nil {
				log.Println("session gc:", err)
			}
			s.rateLimit.GC()
		}
	}
}

// originGuard rejects requests whose Host is not this console (DNS-rebinding
// protection) and cross-origin mutations (drive-by CSRF against the approval
// gate / kill switch). The console has no auth by design (localhost bind), so
// this boundary is what keeps a random browser tab from driving GOVERN.
func (s *Server) originGuard(next http.Handler) http.Handler {
	host, port, _ := net.SplitHostPort(s.Cfg.Listen)
	allowed := map[string]bool{
		s.Cfg.Listen:        true,
		"localhost:" + port: true,
		"127.0.0.1:" + port: true,
	}
	// Deliberately non-loopback bind (e.g. Listen="0.0.0.0:PORT" to reach the
	// C2 bootstrap page over LAN): a browser's Host header carries the actual
	// interface IP, which never equals the literal wildcard address above.
	// Allow the machine's own non-loopback interface IPs at the configured
	// port — this is the legitimate bind target, not an open allowlist; a
	// spoofed/foreign Host still fails since it won't be one of THIS host's
	// addresses. Loopback binds are untouched (host stays 127.0.0.1/localhost
	// only), so DNS-rebinding protection for the default mode is unchanged.
	if host == "0.0.0.0" || host == "" || host == "::" {
		for _, ip := range localNonLoopbackIPs() {
			allowed[net.JoinHostPort(ip, port)] = true
		}
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
			// [C5] Defense-in-depth: even with a same-origin-looking Origin,
			// a browser-sent Sec-Fetch-Site of "cross-site" means some other
			// origin initiated the request (fetch metadata can't be spoofed by
			// script). Absent header = non-browser client (curl/tests), trusted
			// on localhost; only an explicit cross-site value is rejected here.
			if sfs := r.Header.Get("Sec-Fetch-Site"); sfs == "cross-site" {
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// localNonLoopbackIPs returns this machine's own non-loopback interface IPs
// (e.g. LAN address like 192.168.1.5), best-effort. Used only to extend the
// originGuard Host allowlist when the console is deliberately bound to a
// wildcard address — never used to accept an arbitrary caller-supplied Host.
func localNonLoopbackIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var ips []string
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.To4() == nil {
			continue
		}
		ips = append(ips, ipNet.IP.String())
	}
	return ips
}

func (s *Server) routes() {
	s.mux.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Collect(s.Cfg, s.Store))
	})
	s.mux.Handle("/static/*", staticHandler())

	s.mux.Get("/", s.handleHome) // mode-aware: exec home or standup
	s.mux.Get("/standup", s.handleStandup)
	s.mux.Post("/mode", s.handleSetMode) // viewer-ok: cookie UI, no DB mutation
	s.mux.Get("/exec", s.handleExecHome)
	s.mux.With(s.requireAdmin).Post("/exec/approve/{id}", s.handleExecApprove)
	s.mux.With(s.requireAdmin).Post("/exec/insight/{id}/dismiss", s.handleExecDismiss)
	s.mux.Get("/insights", s.handleInsights)
	s.mux.Get("/dash/org", s.handleDashOrg)
	s.mux.Get("/dash/project/{project}", s.handleDashProject)
	s.mux.Get("/dash/agent/{agent}", s.handleDashAgent)
	s.mux.With(s.requireAdmin).Post("/agents/{agent}/band", s.handleSetBand)
	s.mux.Get("/agents/{agent}/review", s.handleAgentAIReview)

	s.mux.Get("/launch", s.handleLaunchForm)
	s.mux.With(s.requireAdmin).Post("/launch", s.handleLaunch) // spend + execute code (C4)
	s.mux.Get("/runs/{id}/retry", s.handleRetryForm)
	s.mux.With(s.requireAdmin).Post("/runs/{id}/retry", s.handleRetry) // spend + execute (C4)

	s.mux.Get("/runs", s.handleRuns)
	s.mux.With(s.requireAdmin).Post("/runs/bulk-kill", s.handleBulkKill)
	s.mux.With(s.requireAdmin).Post("/runs/bulk-budget", s.handleBulkBudget)
	s.mux.Get("/runs/compare", s.handleRunCompare)
	s.mux.Get("/spikes", s.handleSpikes)
	s.mux.Get("/runs/{id}", s.handleRunDetail)
	s.mux.Get("/runs/{id}/status-fragment", s.handleRunStatusFragment)
	s.mux.Get("/runs/{id}/log-tail", s.handleRunLogTail)
	s.mux.With(s.requireAdmin).Post("/runs/{id}/kill", s.handleRunKill)
	s.mux.Post("/runs/{id}/task-key", s.handleRunTaskKey) // viewer-ok: metadata annotate
	s.mux.Post("/runs/{id}/flag", s.handleRunFlag)         // viewer-ok: signal (like reaction)
	s.mux.Post("/runs/{id}/playbook", s.handlePlaybookCreate) // viewer-ok: knowledge capture
	s.mux.Get("/playbooks", s.handlePlaybooks)
	s.mux.Post("/playbooks/{id}/adopt", s.handlePlaybookAdopt) // viewer-ok: metric

	s.mux.Get("/contexts", s.handleContexts)
	s.mux.With(s.requireAdmin).Post("/contexts/save", s.handleContextSave)
	s.mux.Get("/contexts/{layer}/{target}/history", s.handleContextHistory)
	s.mux.Get("/contexts/diff", s.handleContextDiff)
	s.mux.With(s.requireAdmin).Post("/contexts/rollback", s.handleContextRollback)
	s.mux.With(s.requireAdmin).Post("/contexts/promote", s.handleContextPromote)
	s.mux.Get("/contexts/effective", s.handleContextEffective)
	s.mux.Get("/contexts/drive-search", s.handleDriveSearch)
	s.mux.Get("/contexts/drive-review", s.handleDriveReview)
	s.mux.With(s.requireAdmin).Post("/contexts/drive-import", s.handleDriveImport)

	s.mux.Get("/reviews", s.handleReviews)
	s.mux.With(s.requireAdmin).Post("/reviews/{id}/decide", s.handleReviewDecide)

	s.mux.Get("/chat", s.handleChatPage)
	s.mux.With(s.requireAdmin).Post("/chat/message", s.handleChatMessage)

	s.mux.Get("/budgets", s.handleBudgets)
	s.mux.With(s.requireAdmin).Post("/budgets", s.handleBudgetSet)

	s.mux.Get("/provenance", s.handleProvenance)
	s.mux.Get("/rules", s.handleRules)
	s.mux.With(s.requireAdmin).Post("/rules", s.handleRuleCreate)
	s.mux.Post("/rules/simulate", s.handleRuleSimulate) // viewer-ok: read-only sim
	s.mux.With(s.requireAdmin).Post("/rules/{id}/toggle", s.handleRuleToggle)
	s.mux.With(s.requireAdmin).Post("/rules/{id}/delete", s.handleRuleDelete)

	s.mux.With(s.requireAdmin).Post("/api/kill", s.handleGlobalKill)
	// POST: the export appends an audit entry (side effect) — GET would let a
	// drive-by <img src> spam the append-only chain past the origin guard.
	s.mux.With(s.requireAdmin).Post("/export/compliance", s.handleComplianceExport)
	s.mux.With(s.requireAdmin).Post("/reports/confluence", s.handleConfluenceReport)

	// v8 onboarding: credential settings + test connection (write-through .env,
	// restart-required for workers).
	s.mux.Get("/settings/integrations", s.handleSettings)
	s.mux.With(s.requireAdmin).Post("/settings/integrations/{name}", s.handleSettingsSave)
	s.mux.Post("/settings/integrations/{name}/test", s.handleSettingsTest) // viewer-ok: read-only test

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
	s.registerKnowledgeRoutes()
}

func (s *Server) Handler() http.Handler { return s.mux }

// ListenAndServe blocks serving the console on cfg.Listen.
func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.Cfg.Listen, s.mux)
}
