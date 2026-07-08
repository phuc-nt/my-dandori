package govern

import (
	"context"
	"fmt"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// checkRisk is the G5 risk-score guardrail: a run accrues points from
// tool_use volume plus real rule-hit denials over a SLIDING WINDOW of the
// last WindowN events — not the whole run — so a long, otherwise-clean run
// never climbs into the threshold purely from length. Default mode is
// log-only: crossing the threshold only emits an observational event, it
// never blocks. Gating is opt-in (RiskScore.Mode == "gate") and must only be
// turned on after an operator calibrates Threshold against real fleet data
// (see the CALIBRATION query in docs/04-implementation-notes.md).
//
// Self-exclusion (anti-ratchet): RiskScore's denial count deliberately
// excludes engine_error, budget_block, and risk_gate audit actions, and
// every Allow decision. Without this, a G5 escalation (or a transient DB
// error) would feed its own score, monotonically ratcheting every run toward
// the threshold regardless of actual behavior — the exact bug the original
// design had and this rewrite fixes. See RiskScore for the query mechanics.
func (e *Engine) checkRisk(ctx context.Context, tc ToolCall) (Decision, bool) {
	// A non-guarded tool can never gate, so skip the scoring queries entirely —
	// this runs on every pre-tool call, including read-only tools (Read/Grep),
	// and the two SQLite lookups would otherwise be computed and discarded.
	guarded := false
	for _, name := range e.Cfg.RiskScore.GuardedToolsValue() {
		if name == tc.ToolName {
			guarded = true
			break
		}
	}
	if !guarded {
		return Decision{}, false
	}

	score, err := e.RiskScore(tc.RunID)
	if err != nil {
		if e.Cfg.RiskScore.GateMode() {
			// risk, FailClosed (contract.go, GATE mode only): cannot prove the
			// run is under threshold → deny.
			return Decision{Deny, "[dandori G5] internal error computing risk score: " + err.Error()}, true
		}
		// LOG mode: nothing to gate yet, only an observation to skip — allow.
		return Decision{}, false
	}

	if score < e.Cfg.RiskScore.ThresholdValue() {
		return Decision{}, false
	}

	if !e.Cfg.RiskScore.GateMode() {
		e.emitRiskWouldGate(tc, score)
		return Decision{}, false // LOG mode never blocks
	}

	return e.escalateRisk(ctx, tc, score)
}

// emitRiskWouldGate records the log-mode observation: this call would have
// been escalated had gate mode been on. Cheap, best-effort (capture-style —
// a write failure here must not affect the tool call's own outcome).
func (e *Engine) emitRiskWouldGate(tc ToolCall, score int) {
	detail := fmt.Sprintf("tool=%s score=%d threshold=%d (log-only, not blocked)",
		tc.ToolName, score, e.Cfg.RiskScore.ThresholdValue())
	_, _ = e.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, ?, 'risk_would_gate', ?, 1, ?)`, tc.RunID, store.Now(), tc.ToolName, detail)
}

// escalateRisk routes an over-threshold guarded call through the SAME
// approval machinery checkGate uses (findOrCreateApproval + waitDecision)
// directly — NOT checkGate's band-skip path, because a trusted agent must
// not be exempt from G5 the way it is exempt from non-critical gate rules
// (unlike G4, risk accrual is about recent behavior, which a trust band
// earned on cost/edit history does not vouch for).
func (e *Engine) escalateRisk(ctx context.Context, tc ToolCall, score int) (Decision, bool) {
	reason := fmt.Sprintf("[dandori G5] risk score %d ≥ threshold %d over the last %d events — cần người duyệt trước khi tiếp tục",
		score, e.Cfg.RiskScore.ThresholdValue(), e.Cfg.RiskScore.WindowNValue())
	action := summarizeAction(tc)

	e.expireStale()
	id, err := e.findOrCreateApproval(tc.RunID, action, reason)
	if err != nil {
		return Decision{Deny, "[dandori G5] internal error creating approval: " + err.Error()}, true
	}

	status, decidedBy := e.waitDecision(ctx, id, time.Duration(e.Cfg.GateWaitSeconds)*time.Second)
	switch status {
	case "approved":
		if !e.consume(id) {
			return Decision{Deny, fmt.Sprintf("[dandori G5] approval #%d was already consumed by another call — request approval again", id)}, true
		}
		return Decision{Allow, fmt.Sprintf("risk approval #%d granted by %s", id, decidedBy)}, true
	case "rejected":
		return Decision{Deny, fmt.Sprintf("[dandori G5] approval #%d REJECTED by %s — do not retry this action", id, decidedBy)}, true
	default:
		return Decision{Deny, fmt.Sprintf("[dandori G5] approval #%d still pending after %ds — ask an operator to approve at the Dandori console, then retry",
			id, e.Cfg.GateWaitSeconds)}, true
	}
}
