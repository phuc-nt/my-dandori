package web

import (
	"net/http"

	"github.com/phuc-nt/dandori/internal/govern"
)

// wallboardRunCap bounds how many running runs the TV board lists — a fleet
// wallboard is glanced at, not scrolled.
const wallboardRunCap = 20

// WallboardData is everything the full-screen TV board renders: currently
// running runs, spend vs the global monthly budget, and the pending-work
// queue depth (approvals + open flags).
type WallboardData struct {
	Running    []RunRow
	SpendUSD   float64
	BudgetUSD  float64
	SpendPct   float64
	QueueDepth int
	HasBudget  bool
}

// handleWallboard renders the full-screen TV shell; the numbers themselves
// come from the polled fragment.
func (s *Server) handleWallboard(w http.ResponseWriter, r *http.Request) {
	data := s.buildWallboardData()
	s.render(w, r, "wallboard", map[string]any{"Page": "wallboard", "Wallboard": data})
}

// handleWallboardFragment is polled every 5s (hx-trigger) to refresh the
// board's numbers without a full page reload. Read-only, no auth-sensitive
// action — safe for an unattended shared screen.
func (s *Server) handleWallboardFragment(w http.ResponseWriter, r *http.Request) {
	data := s.buildWallboardData()
	s.renderFragment(w, r, "wallboard", "wallboard_fragment", map[string]any{"Wallboard": data})
}

// buildWallboardData assembles UG5's numbers. Degrades gracefully: no
// running runs / no budget configured still returns a valid, zero-valued
// struct rather than erroring the page.
func (s *Server) buildWallboardData() WallboardData {
	running, err := s.queryRuns(`WHERE status = 'running'`)
	if err != nil {
		running = nil
	}
	if len(running) > wallboardRunCap {
		running = running[:wallboardRunCap]
	}

	eng := govern.NewEngine(s.Cfg, s.Store)
	spend, _ := eng.SpendMonth("global", "")

	var budget float64
	hasBudget := false
	if err := s.Store.DB.QueryRow(
		`SELECT limit_usd FROM budgets WHERE scope_type = 'global' AND scope_id = ''`,
	).Scan(&budget); err == nil {
		hasBudget = true
	} else if s.Cfg.Budget.GlobalMonthlyUSD > 0 {
		budget = s.Cfg.Budget.GlobalMonthlyUSD
		hasBudget = true
	}

	var pct float64
	if budget > 0 {
		pct = spend / budget * 100
	}

	var pendingApprovals, openFlags int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM approvals WHERE status = 'pending'`).Scan(&pendingApprovals)
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM flags WHERE status = 'open'`).Scan(&openFlags)

	return WallboardData{
		Running:    running,
		SpendUSD:   spend,
		BudgetUSD:  budget,
		SpendPct:   pct,
		QueueDepth: pendingApprovals + openFlags,
		HasBudget:  hasBudget,
	}
}
