package govern

import (
	"database/sql"
	"fmt"
	"time"
)

// nullAllowGate decides an over-budget tool call whose run has no known
// model yet (NULL/unreconciled). NULL must not be an unlimited free pass —
// an agent could otherwise keep restarting sessions to dodge the
// expensive-model deny while its model column stays NULL. Two guards:
//  1. Agent-scoped fallback: if this agent's most recent PRIOR run this month
//     (excluding the current run) settled on an expensive model, treat the
//     current NULL run as expensive too and deny.
//  2. A per-agent-per-month cap on NULL-allows (Budget.NullAllowCap, default
//     20): once exceeded, NULL denies regardless of history — bounds the
//     total exposure of the free pass even for an agent with no expensive
//     history yet.
func (e *Engine) nullAllowGate(tc ToolCall, scopeType, scopeID string, spend, limit float64) (Decision, bool) {
	month := time.Now().UTC().Format("2006-01")
	deny := fmt.Sprintf(
		"[dandori G3] budget exceeded for %s %s ($%.2f / $%.2f this month) and this run's model is not yet known — denying to avoid an expensive-model bypass; đổi sang model rẻ hơn qua /model rồi thử lại",
		scopeType, scopeID, spend, limit)

	prevModel, err := e.agentPriorModel(tc.AgentID, tc.RunID, month)
	if err != nil {
		// budget, FailClosed: cannot prove the agent's history is clean → deny.
		return Decision{Deny, "[dandori G3] internal error checking agent history: " + err.Error()}, true
	}
	if prevModel != "" && e.matchExpensive(prevModel) {
		return Decision{Deny, deny}, true
	}

	count, err := e.nullAllowCount(tc.AgentID, month)
	if err != nil {
		return Decision{Deny, "[dandori G3] internal error checking null-allow count: " + err.Error()}, true
	}
	if count >= e.Cfg.Budget.NullAllowCapValue() {
		return Decision{Deny, deny}, true
	}

	if err := e.incrNullAllowCount(tc.AgentID, month, count); err != nil {
		return Decision{Deny, "[dandori G3] internal error recording null-allow: " + err.Error()}, true
	}
	e.emitBudgetDowngradeAllow(tc.RunID, scopeType, scopeID, "(unknown)", spend, limit)
	return Decision{}, false
}

// agentPriorModel returns the most recent non-null model this agent settled
// on this month, from a run other than the current one. Empty means no
// prior run with a known model this month.
func (e *Engine) agentPriorModel(agentID, excludeRunID, month string) (string, error) {
	monthStart := month + "-01T00:00:00Z"
	var model string
	err := e.St.DB.QueryRow(`SELECT model FROM runs
		WHERE agent_id = ? AND id != ? AND started_at >= ? AND model IS NOT NULL AND model != ''
		ORDER BY started_at DESC LIMIT 1`, agentID, excludeRunID, monthStart).Scan(&model)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return model, nil
}

// nullAllowCount reads the per-agent-per-month NULL-allow counter. This uses
// a bounded settings key (one row per agent per month, not per run) rather
// than an events-query scan — the cardinality is intentionally small and the
// value must be read-then-incremented atomically-enough for a single-writer
// SQLite engine.
func (e *Engine) nullAllowCount(agentID, month string) (int, error) {
	key := nullAllowKey(agentID, month)
	v := e.St.Setting(key)
	if v == "" {
		return 0, nil
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return 0, err
	}
	return n, nil
}

func (e *Engine) incrNullAllowCount(agentID, month string, current int) error {
	return e.St.SetSetting(nullAllowKey(agentID, month), fmt.Sprintf("%d", current+1))
}

func nullAllowKey(agentID, month string) string {
	return fmt.Sprintf("null_allow:%s:%s", agentID, month)
}
