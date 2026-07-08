package govern

// discriminatedDenialActions are the audit_log actions engine.record() writes
// for a REAL rule-hit denial — the check found something wrong and stopped
// the call. RiskScore counts only these. Deliberately excluded:
//   - "engine_error": the check itself failed, not the tool call — a
//     transient DB error must not read as misbehavior.
//   - "budget_block": a cost circuit breaker, not a risk signal about what
//     the agent is doing.
//   - "risk_gate": G5's own escalation — counting it would make every
//     escalation (including a human-APPROVED one) feed the next score,
//     a self-amplifying ratchet that bricks normal runs. This is the
//     self-exclusion the phase spec calls out as the critical anti-ratchet
//     fix over the original design.
//   - "gate_decision": G4's ordinary human-approval outcomes (including
//     Allow-after-approve) are governance-by-design, not misbehavior.
//   - "kill_block": an operator-initiated stop, not agent behavior.
var discriminatedDenialActions = map[string]bool{
	"guardrail_block": true,
	"sandbox_block":   true,
	"secrets_block":   true,
}

// RiskScore computes the G5 score for a run over a SLIDING WINDOW of its
// last WindowN events (default 40) — never the whole run.
//
// Window semantics: the base is the tool_use volume among the last WindowN
// `events` rows for this run (ordered by id, matching insertion/ts order —
// events(run_id,ts) index already covers this). Real-denial points are then
// counted from audit_log, scoped to the SAME time span: every audit_log row
// for this run_id (subject) whose ts is >= the timestamp of the oldest event
// in that window, with action in discriminatedDenialActions. audit_log has
// no per-subject index, but it is a small, append-only table (one row per
// guardrail decision across the whole fleet) — a per-run filtered scan is
// sub-ms in practice, so no migration/index is added for it (confirmed
// against 001_init.sql: audit_log has none, events(run_id,ts) already does).
//
// This keeps "window" as one honest concept (a time span) applied
// consistently to both the tool_use side (events table) and the denial side
// (audit_log table), rather than trying to windo the audit rows by their own
// count, which would not line up with the tool_use window at all.
func (e *Engine) RiskScore(runID string) (int, error) {
	events, err := e.lastNEvents(runID, e.Cfg.RiskScore.WindowNValue())
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}

	points := e.Cfg.RiskScore.ToolPointsValue()
	score := 0
	for _, ev := range events {
		if ev.kind == "tool_use" {
			score += points[ev.tool]
		}
	}

	windowStart := events[0].ts // oldest event in the window (ASC order)
	denials, err := e.countRealDenials(runID, windowStart)
	if err != nil {
		return 0, err
	}
	score += denials * e.Cfg.RiskScore.DenialPointsValue()
	return score, nil
}

type scoredEvent struct {
	kind, tool, ts string
}

// lastNEvents returns the last n events for a run in chronological (oldest
// first) order, using the existing events(run_id, ts) index.
func (e *Engine) lastNEvents(runID string, n int) ([]scoredEvent, error) {
	rows, err := e.St.DB.Query(`SELECT kind, COALESCE(tool_name,''), ts FROM events
		WHERE run_id = ? ORDER BY ts DESC, id DESC LIMIT ?`, runID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scoredEvent
	for rows.Next() {
		var ev scoredEvent
		if err := rows.Scan(&ev.kind, &ev.tool, &ev.ts); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse to chronological (oldest first) so callers can read events[0]
	// as the window's start boundary.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// countRealDenials counts audit_log rows for this run (subject = run_id)
// at or after windowStart, whose action is a real rule-hit denial — see
// discriminatedDenialActions for the self-exclusion list.
func (e *Engine) countRealDenials(runID, windowStart string) (int, error) {
	rows, err := e.St.DB.Query(`SELECT action FROM audit_log
		WHERE subject = ? AND ts >= ?`, runID, windowStart)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			return 0, err
		}
		if discriminatedDenialActions[action] {
			count++
		}
	}
	return count, rows.Err()
}
