package govern

import (
	"context"
	"fmt"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// checkGate handles permission-gate rules (G4): matching tool calls create a
// pending approval, then wait (polling) up to GateWaitSeconds for a human
// decision from the web console or Slack. Approved → allow (consumed once);
// rejected or timeout → deny with retry instructions.
func (e *Engine) checkGate(ctx context.Context, tc ToolCall, rules []Rule) (Decision, bool) {
	var gated *Rule
	for i := range rules {
		if rules[i].Kind == "gate" && rules[i].matches(tc) {
			gated = &rules[i]
			break
		}
	}
	if gated == nil {
		return Decision{}, false
	}

	action := tc.Command
	if action == "" && len(tc.Paths) > 0 {
		action = tc.ToolName + " " + tc.Paths[0]
	}
	reason := fmt.Sprintf("%s (rule #%d)", gated.Description, gated.ID)
	id, err := e.CreateApproval(tc.RunID, action, reason)
	if err != nil {
		return Decision{Deny, "[dandori G4] internal error creating approval: " + err.Error()}, true
	}

	status, decidedBy := e.waitDecision(ctx, id, time.Duration(e.Cfg.GateWaitSeconds)*time.Second)
	switch status {
	case "approved":
		e.consume(id)
		return Decision{Allow, fmt.Sprintf("approval #%d granted by %s", id, decidedBy)}, true
	case "rejected":
		return Decision{Deny, fmt.Sprintf("[dandori G4] approval #%d REJECTED by %s — do not retry this action", id, decidedBy)}, true
	default:
		return Decision{Deny, fmt.Sprintf("[dandori G4] approval #%d still pending after %ds — ask an operator to approve at the Dandori console, then retry",
			id, e.Cfg.GateWaitSeconds)}, true
	}
}

// CreateApproval inserts a pending approval and returns its id.
func (e *Engine) CreateApproval(runID, action, reason string) (int64, error) {
	res, err := e.St.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at)
		VALUES(?, ?, ?, ?)`, runID, action, reason, store.Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// waitDecision polls the approval row every 2s until decided, ctx done, or timeout.
func (e *Engine) waitDecision(ctx context.Context, id int64, timeout time.Duration) (status, decidedBy string) {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		_ = e.St.DB.QueryRow(`SELECT status, COALESCE(decided_by,'') FROM approvals WHERE id = ?`, id).
			Scan(&status, &decidedBy)
		if status == "approved" || status == "rejected" {
			return status, decidedBy
		}
		if time.Now().After(deadline) {
			return "pending", ""
		}
		select {
		case <-ctx.Done():
			return "pending", ""
		case <-tick.C:
		}
	}
}

// consume marks an approved approval as used so it cannot authorize twice.
func (e *Engine) consume(id int64) {
	_, _ = e.St.DB.Exec(`UPDATE approvals SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`,
		store.Now(), id)
}

// Decide resolves a pending approval (used by web console and Slack poller).
// First writer wins: the UPDATE is guarded by status='pending'.
func Decide(st *store.Store, id int64, approve bool, actor, note string) (bool, error) {
	status := "rejected"
	if approve {
		status = "approved"
	}
	res, err := st.DB.Exec(`UPDATE approvals SET status = ?, decided_at = ?, decided_by = ?, decision_note = ?
		WHERE id = ? AND status = 'pending'`, status, store.Now(), actor, note, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil // already decided elsewhere
	}
	a := &Audit{St: st, Actor: actor}
	_, err = a.Append("approval_"+status, fmt.Sprintf("approval:%d", id), note)
	return true, err
}
