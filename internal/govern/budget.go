package govern

import (
	"fmt"
	"strings"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// SpendMonth sums run cost for the current calendar month within a scope.
// scopeType: global | agent | project (scopeID empty for global).
func (e *Engine) SpendMonth(scopeType, scopeID string) (float64, error) {
	monthStart := time.Now().UTC().Format("2006-01") + "-01T00:00:00Z"
	q := `SELECT COALESCE(SUM(cost_usd), 0) FROM runs WHERE started_at >= ?`
	args := []any{monthStart}
	switch scopeType {
	case "agent":
		q += ` AND agent_id = ?`
		args = append(args, scopeID)
	case "project":
		q += ` AND project = ?`
		args = append(args, scopeID)
	}
	var spend float64
	err := e.St.DB.QueryRow(q, args...).Scan(&spend)
	return spend, err
}

// budgetLimit returns the configured limit for a scope (0 = no budget set).
// The global scope falls back to config when no row exists.
func (e *Engine) budgetLimit(scopeType, scopeID string) float64 {
	var limit float64
	err := e.St.DB.QueryRow(`SELECT limit_usd FROM budgets WHERE scope_type = ? AND scope_id = ?`,
		scopeType, scopeID).Scan(&limit)
	if err == nil {
		return limit
	}
	if scopeType == "global" {
		return e.Cfg.Budget.GlobalMonthlyUSD
	}
	return 0
}

// checkBudget enforces the circuit breaker (G3): each applicable scope is
// checked; ≥100% triggers the configured mode (hard-stop or downgrade-gate),
// warn thresholds emit one budget_warn event per (scope, pct, month).
func (e *Engine) checkBudget(tc ToolCall) (Decision, bool) {
	scopes := [][2]string{{"global", ""}, {"agent", tc.AgentID}, {"project", tc.Project}}
	for _, s := range scopes {
		limit := e.budgetLimit(s[0], s[1])
		if limit <= 0 {
			continue
		}
		spend, err := e.SpendMonth(s[0], s[1])
		if err != nil {
			// budget, FailClosed (contract.go): cannot prove spend is within limit → deny
			return Decision{Deny, "[dandori G3] internal error checking budget: " + err.Error()}, true
		}
		pct := spend / limit * 100
		if pct >= 100 {
			return e.overBudgetDecision(tc, s[0], s[1], spend, limit)
		}
		e.emitBudgetWarn(s[0], s[1], pct, spend, limit)
	}
	return Decision{}, false
}

// overBudgetDecision applies the configured Budget.Mode once a scope has
// crossed 100%. "hard" preserves the pre-v14 behavior: deny every tool call
// regardless of model. The default "downgrade" mode only denies when the
// run's own model is expensive — a cheap-model run is allowed to continue so
// an agent isn't fully blocked just because a different scope overspent.
func (e *Engine) overBudgetDecision(tc ToolCall, scopeType, scopeID string, spend, limit float64) (Decision, bool) {
	if e.Cfg.Budget.Mode == "hard" {
		return Decision{Deny, fmt.Sprintf("[dandori G3] budget exceeded for %s %s: $%.2f / $%.2f this month — hard stop",
			scopeType, scopeID, spend, limit)}, true
	}
	model, err := e.runModel(tc.RunID)
	if err != nil {
		// budget, FailClosed (contract.go): cannot prove the run's model is
		// cheap enough to allow through the downgrade-gate → deny.
		return Decision{Deny, "[dandori G3] internal error checking run model: " + err.Error()}, true
	}
	if model == "" {
		return e.nullAllowGate(tc, scopeType, scopeID, spend, limit)
	}
	if e.matchExpensive(model) {
		return Decision{Deny, fmt.Sprintf(
			"[dandori G3] budget exceeded for %s %s ($%.2f / $%.2f this month) and this run's model (%s) is expensive — đổi sang model rẻ hơn qua /model rồi thử lại",
			scopeType, scopeID, spend, limit, model)}, true
	}
	e.emitBudgetDowngradeAllow(tc.RunID, scopeType, scopeID, model, spend, limit)
	return Decision{}, false
}

// runModel point-queries the run's currently-known model. Empty string means
// unreconciled/unknown (NULL in the DB) — see ingest.go's ReconcileUsage,
// which only writes runs.model once a transcript is parsed (Claude runtime)
// or at FinalizeRun (other runtimes); a run can spend an entire session with
// model == "" if it never reconciles mid-run.
func (e *Engine) runModel(runID string) (string, error) {
	var model string
	err := e.St.DB.QueryRow(`SELECT COALESCE(model, '') FROM runs WHERE id = ?`, runID).Scan(&model)
	if err != nil {
		return "", err
	}
	return model, nil
}

// matchExpensive reports whether model matches the configured expensive
// allowlist (case-insensitive substring). An empty list disables the hard
// limit entirely — see Budget.ExpensiveModels doc comment.
func (e *Engine) matchExpensive(model string) bool {
	list := e.Cfg.Budget.ExpensiveModelList()
	if len(list) == 0 {
		return false
	}
	lower := strings.ToLower(model)
	for _, needle := range list {
		if needle == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

// emitBudgetDowngradeAllow records the honest-data event for "over budget,
// but this run's model was cheap enough (or NULL within the allow cap) to
// keep going" — at most once per run per month. Dedup is done via an events
// query (has this run already emitted this kind this month?) rather than a
// settings key, so the settings table doesn't grow one row per run
// (unbounded cardinality) — see Budget downgrade-gate requirements.
func (e *Engine) emitBudgetDowngradeAllow(runID, scopeType, scopeID, model string, spend, limit float64) {
	monthStart := time.Now().UTC().Format("2006-01") + "-01T00:00:00Z"
	var exists int
	_ = e.St.DB.QueryRow(`SELECT COUNT(*) FROM events
		WHERE run_id = ? AND kind = 'budget_downgrade_allow' AND ts >= ?`, runID, monthStart).Scan(&exists)
	if exists > 0 {
		return
	}
	detail := fmt.Sprintf("budget %s %s over limit ($%.2f/$%.2f) but model %q allowed to continue (downgrade-gate)",
		scopeType, scopeID, spend, limit, model)
	_, _ = e.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, ?, 'budget_downgrade_allow', '', 1, ?)`, runID, store.Now(), detail)
}

// emitBudgetWarn records threshold crossings once per scope+pct+month.
func (e *Engine) emitBudgetWarn(scopeType, scopeID string, pct, spend, limit float64) {
	for _, warn := range e.Cfg.Budget.WarnPcts {
		if pct < float64(warn) {
			continue
		}
		key := fmt.Sprintf("budget_warn:%s:%s:%d:%s", scopeType, scopeID, warn, time.Now().UTC().Format("2006-01"))
		if e.St.Setting(key) != "" {
			continue
		}
		_ = e.St.SetSetting(key, store.Now())
		detail := fmt.Sprintf("budget %s %s at %.0f%% ($%.2f/$%.2f, threshold %d%%)",
			scopeType, scopeID, pct, spend, limit, warn)
		_, _ = e.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
			VALUES(NULL, ?, 'budget_warn', '', 0, ?)`, store.Now(), detail)
	}
}
