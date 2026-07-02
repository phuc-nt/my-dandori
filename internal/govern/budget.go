package govern

import (
	"fmt"
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
// checked; ≥100% denies, warn thresholds emit one budget_warn event per
// (scope, pct, month).
func (e *Engine) checkBudget(tc ToolCall) (Decision, bool) {
	scopes := [][2]string{{"global", ""}, {"agent", tc.AgentID}, {"project", tc.Project}}
	for _, s := range scopes {
		limit := e.budgetLimit(s[0], s[1])
		if limit <= 0 {
			continue
		}
		spend, err := e.SpendMonth(s[0], s[1])
		if err != nil {
			return Decision{Deny, "[dandori G3] internal error checking budget: " + err.Error()}, true
		}
		pct := spend / limit * 100
		if pct >= 100 {
			return Decision{Deny, fmt.Sprintf("[dandori G3] budget exceeded for %s %s: $%.2f / $%.2f this month — hard stop",
				s[0], s[1], spend, limit)}, true
		}
		e.emitBudgetWarn(s[0], s[1], pct, spend, limit)
	}
	return Decision{}, false
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
