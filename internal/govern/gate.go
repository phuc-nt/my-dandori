package govern

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// checkGate handles permission-gate rules (G4), modulated by the agent's
// autonomy band: supervised gates every edit tool call, trusted skips gate
// rules unless critical. Matching calls create a pending approval, then wait
// (polling) up to GateWaitSeconds for a human decision from the web console
// or Slack. Approved → allow (consumed once); rejected/timeout → deny.
func (e *Engine) checkGate(ctx context.Context, tc ToolCall, rules []Rule) (Decision, bool) {
	band := BandFor(e.St, tc.AgentID)
	var gated *Rule
	for i := range rules {
		r := &rules[i]
		if r.Kind != "gate" || !r.appliesTo(tc) || !r.matches(tc) {
			continue
		}
		if band == BandTrusted && !r.Critical {
			continue // trusted agents skip non-critical gates
		}
		gated = r
		break
	}
	reason := ""
	switch {
	case gated != nil:
		reason = fmt.Sprintf("%s (rule #%d)", gated.Description, gated.ID)
	case band == BandSupervised && (isEditTool(tc.ToolName) || tc.ToolName == "Bash"):
		// Supervised means supervised: file edits AND shell commands (a shell
		// can edit anything) both need a human.
		reason = "supervised band: edits and shell commands require approval"
	default:
		return Decision{}, false
	}

	action := tc.Command
	if action == "" && len(tc.Paths) > 0 {
		action = tc.ToolName + " " + tc.Paths[0]
	}
	if action == "" {
		action = tc.ToolName
	}
	e.expireStale()
	id, err := e.findOrCreateApproval(tc.RunID, action, reason)
	if err != nil {
		return Decision{Deny, "[dandori G4] internal error creating approval: " + err.Error()}, true
	}

	status, decidedBy := e.waitDecision(ctx, id, time.Duration(e.Cfg.GateWaitSeconds)*time.Second)
	switch status {
	case "approved":
		// Single-use: with pending-reuse, N concurrent waiters can share one
		// approval — only the one that consumes it proceeds.
		if !e.consume(id) {
			return Decision{Deny, fmt.Sprintf("[dandori G4] approval #%d was already consumed by another call — request approval again", id)}, true
		}
		return Decision{Allow, fmt.Sprintf("approval #%d granted by %s", id, decidedBy)}, true
	case "rejected":
		return Decision{Deny, fmt.Sprintf("[dandori G4] approval #%d REJECTED by %s — do not retry this action", id, decidedBy)}, true
	default:
		return Decision{Deny, fmt.Sprintf("[dandori G4] approval #%d still pending after %ds — ask an operator to approve at the Dandori console, then retry",
			id, e.Cfg.GateWaitSeconds)}, true
	}
}

// isEditTool identifies file-mutating tools (supervised-band gating).
func isEditTool(name string) bool {
	return name == "Write" || name == "Edit" || name == "NotebookEdit"
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

// findOrCreateApproval reuses an existing pending approval for the same
// run+action (agent retries after a timeout deny must not spam the queue).
func (e *Engine) findOrCreateApproval(runID, action, reason string) (int64, error) {
	var id int64
	err := e.St.DB.QueryRow(`SELECT id FROM approvals
		WHERE run_id = ? AND action = ? AND status = 'pending'
		ORDER BY id DESC LIMIT 1`, runID, action).Scan(&id)
	switch {
	case err == nil:
		return id, nil
	case err == sql.ErrNoRows:
		return e.CreateApproval(runID, action, reason)
	default:
		return 0, err // transient DB error must not spawn duplicates
	}
}

// expireStale flips pending approvals older than the TTL to expired.
// Called lazily at gate time and periodically by the serve worker.
func (e *Engine) expireStale() {
	ttl := e.Cfg.ApprovalTTLMinutes
	if ttl <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-time.Duration(ttl) * time.Minute).Format(time.RFC3339)
	// Closed-loop band proposals are review-queue items humans act on in
	// their own time — they must NOT die with the tool-call gate TTL
	// (an expired proposal would never be re-proposed while the flag stays open).
	res, err := e.St.DB.Exec(`UPDATE approvals SET status = 'expired', decided_at = ?
		WHERE status = 'pending' AND requested_at < ? AND action NOT LIKE 'band-demote:%'`, store.Now(), cutoff)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		_, _ = e.Audit.Append("approvals_expired", "gate", fmt.Sprintf("%d pending approval(s) past %dm TTL", n, ttl))
	}
}

// ExpireStale is the exported entrypoint for the serve worker.
func (e *Engine) ExpireStale() { e.expireStale() }

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

// consume marks an approved approval as used; exactly one caller wins.
func (e *Engine) consume(id int64) bool {
	res, err := e.St.DB.Exec(`UPDATE approvals SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`,
		store.Now(), id)
	if err != nil {
		return false // fail-closed: cannot prove single use → deny
	}
	n, _ := res.RowsAffected()
	return n == 1
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
