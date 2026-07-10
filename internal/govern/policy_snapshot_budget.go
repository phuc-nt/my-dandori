package govern

import (
	"database/sql"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

// populateBudgetExceeded fills p.BudgetExceeded (global) and
// p.BudgetExceededAgents/BudgetExceededProjects (per-scope) using the same
// SpendMonth/budgetLimit math checkBudget uses locally (budget.go), but
// against the read pool via spendMonthOn/budgetLimitOn so this never contends
// with the single ingest writer (same posture as populateRiskScores).
//
// Scoping choice (KISS): rather than a map[runID]bool, the agent/project
// scopes actually over budget are collected as plain string sets — a tool
// call already carries its own AgentID/Project (ToolCall), so Evaluate can
// check membership directly without a run-id indirection. Only the active
// runs owned by operatorID (or all runs, in local/no-operator mode) contribute
// scopes — mirrors populateRiskScores' own operator-scoping and 24h active
// bound, so this can't leak another operator's agent/project identifiers.
func populateBudgetExceeded(st *store.Store, p *PolicySnapshot, operatorID string, budgetCfg config.Budget) error {
	limit := budgetLimitOn(st.Read(), "global", "", budgetCfg.GlobalMonthlyUSD)
	if limit > 0 {
		spend, err := spendMonthOn(st.Read(), "global", "")
		if err != nil {
			return err
		}
		p.BudgetExceeded = spend >= limit
	}

	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	var rows *sql.Rows
	var err error
	if operatorID != "" {
		rows, err = st.Read().Query(`SELECT DISTINCT agent_id, project FROM runs
			WHERE status = 'running' AND started_at > ? AND operator_id = ?`, cutoff, operatorID)
	} else {
		rows, err = st.Read().Query(`SELECT DISTINCT agent_id, project FROM runs
			WHERE status = 'running' AND started_at > ?`, cutoff)
	}
	if err != nil {
		return err
	}
	type scope struct{ agentID, project string }
	var scopes []scope
	for rows.Next() {
		var s scope
		if err := rows.Scan(&s.agentID, &s.project); err != nil {
			rows.Close()
			return err
		}
		scopes = append(scopes, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	seenAgent := map[string]bool{}
	seenProject := map[string]bool{}
	for _, s := range scopes {
		if s.agentID != "" && !seenAgent[s.agentID] {
			seenAgent[s.agentID] = true
			exceeded, err := scopeExceeded(st, "agent", s.agentID)
			if err != nil {
				return err
			}
			if exceeded {
				p.BudgetExceededAgents = append(p.BudgetExceededAgents, s.agentID)
			}
		}
		if s.project != "" && !seenProject[s.project] {
			seenProject[s.project] = true
			exceeded, err := scopeExceeded(st, "project", s.project)
			if err != nil {
				return err
			}
			if exceeded {
				p.BudgetExceededProjects = append(p.BudgetExceededProjects, s.project)
			}
		}
	}
	return nil
}

// scopeExceeded reports whether the given agent/project scope has a budget
// row configured AND is at or over it this month. Scopes with no budgets row
// (limit 0) are never exceeded — matching checkBudget's own skip-if-unset.
func scopeExceeded(st *store.Store, scopeType, scopeID string) (bool, error) {
	limit := budgetLimitOn(st.Read(), scopeType, scopeID, 0)
	if limit <= 0 {
		return false, nil
	}
	spend, err := spendMonthOn(st.Read(), scopeType, scopeID)
	if err != nil {
		return false, err
	}
	return spend >= limit, nil
}

// budgetExceededScope reports the first budget scope (global, then agent,
// then project) this tool call falls under that is over its monthly limit —
// checked in that order only to pick a stable, informative scope for the
// Deny/Ask message; a call over budget on more than one scope still gets
// exactly one gate decision either way.
func (p *PolicySnapshot) budgetExceededScope(tc ToolCall) (scopeType, scopeID string, exceeded bool) {
	if p.BudgetExceeded {
		return "global", "", true
	}
	for _, a := range p.BudgetExceededAgents {
		if a == tc.AgentID {
			return "agent", a, true
		}
	}
	for _, proj := range p.BudgetExceededProjects {
		if proj == tc.Project {
			return "project", proj, true
		}
	}
	return "", "", false
}
