package web

import (
	"net/http"
	"strconv"

	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// insightsWindow reads the ?days filter, defaulting to the configured learn
// window. days=0 (all-time) is allowed explicitly via ?days=0.
func (s *Server) insightsWindow(r *http.Request) int {
	if v := r.URL.Query().Get("days"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d >= 0 {
			return d
		}
	}
	return s.Cfg.LearnWindowDays
}

// section is a {Data, Err} cell (F13): every v11 analysis query is wrapped in
// one of these instead of the page fail-fast on first error. A query error
// sets Err and the template renders a "không tải được section này" block for
// just that section — the rest of the page still renders. This replaces the
// v9 pattern (below, kept for the 3 pre-v11 analyses) where any query error
// killed the whole page with http.Error 500.
type section struct {
	Data any
	Err  string
}

func ok(data any) section { return section{Data: data} }

func failed(err error) section { return section{Err: err.Error()} }

// handleInsights renders the data-driven efficiency page: per-model cost &
// cache efficiency (v9), cost-per-outcome (v9), plus 8 v11 descriptive
// analyses (context-ROI, post-run activity, guardrail ledger, time-horizon,
// spend pareto, steering econ, approval funnel). Every ratio ships with its
// sample size; rows under MinSampleForInsight are flagged, never compared as
// if reliable. Per-section degrade (F13): a query error on any ONE v11
// section renders that section's "không tải được" block, it never takes
// down the whole page.
func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	days := s.insightsWindow(r)

	// The 3 pre-v11 analyses stay fail-fast: they are the page's oldest,
	// most exercised path and a failure here means the store itself is
	// broken, not a single new analysis — degrading them piecemeal would
	// add complexity without a real benefit (YAGNI).
	models, err := learn.ModelStats(s.Store, days)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	best, worst, err := learn.TopCacheRuns(s.Store, days, 5)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	group := r.URL.Query().Get("group")
	if group != "agent" {
		group = "project"
	}
	outcomes, err := learn.CostPerOutcome(s.Store, days, group)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	data := insightsData(s.Store, days)
	data["Page"] = "insights"
	data["Window"] = days
	data["MinSample"] = learn.MinSampleForInsight
	data["Models"] = models
	data["BestCache"] = best
	data["WorstCache"] = worst
	data["Outcomes"] = outcomes
	data["Group"] = group

	s.render(w, r, "insights", data)
}

// insightsData runs the 8 v11 analyses, each wrapped in a {Data,Err} section
// so one query error degrades only that section (F13). Split out of
// handleInsights to keep the handler itself under ~200 lines.
func insightsData(st *store.Store, days int) map[string]any {
	hasCtx, ctxErr := learn.HasContextData(st)
	ctxEmpty := ctxErr == nil && !hasCtx

	var ctxSection section
	switch {
	case ctxErr != nil:
		ctxSection = failed(ctxErr)
	case ctxEmpty:
		ctxSection = ok(nil) // empty-state rendered from CtxEmpty, not from Data
	default:
		ready, insufficient, err := learn.ContextROIPairs(st, days)
		if err != nil {
			ctxSection = failed(err)
		} else {
			ctxSection = ok(map[string]any{"Ready": ready, "Insufficient": insufficient})
		}
	}

	activity, err := learn.PostRunActivity(st, days)
	activitySection := ok(activity)
	if err != nil {
		activitySection = failed(err)
	}

	ledger, err := learn.GuardrailLedger(st, days)
	ledgerSection := ok(ledger)
	if err != nil {
		ledgerSection = failed(err)
	}

	horizon, err := learn.TimeHorizon(st, days)
	horizonSection := ok(horizon)
	if err != nil {
		horizonSection = failed(err)
	}
	horizonByModel, err := learn.TimeHorizonByModel(st, days)
	horizonByModelSection := ok(horizonByModel)
	if err != nil {
		horizonByModelSection = failed(err)
	}

	pareto, err := learn.SpendPareto(st, days)
	paretoSection := ok(pareto)
	if err != nil {
		paretoSection = failed(err)
	}

	steering, err := learn.SteeringEconomics(st, days)
	steeringSection := ok(steering)
	if err != nil {
		steeringSection = failed(err)
	}

	funnel, err := learn.ApprovalFunnel(st, days)
	funnelSection := ok(funnel)
	if err != nil {
		funnelSection = failed(err)
	}

	return map[string]any{
		"CtxEmpty":       ctxEmpty,
		"ContextROI":     ctxSection,
		"Activity":       activitySection,
		"Ledger":         ledgerSection,
		"Horizon":        horizonSection,
		"HorizonByModel": horizonByModelSection,
		"Pareto":         paretoSection,
		"Steering":       steeringSection,
		"Funnel":         funnelSection,
	}
}
