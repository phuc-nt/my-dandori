// Package observer is the Master Observer: a periodic engine that watches
// the fleet (grades, behavior, budgets, playbooks) and turns what it sees
// into typed insights. Safe conclusions surface to the inbox by themselves
// (INTERNAL-only — never an external write); sensitive ones become approval
// rows a human decides. Same no-bypass contract as the closed loop it
// generalizes: state changes happen ONLY in the applier, after approval,
// consume-once, audited.
package observer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/redact"
	"github.com/phuc-nt/dandori/internal/store"
)

const actor = "dandori-observer"

// Insight is one observed condition, ready for the inbox or approval queue.
type Insight struct {
	Type     string
	Subject  string
	Summary  string         // plain Vietnamese, redacted before persist
	Evidence map[string]any // metric values + structured action params
	Class    string         // auto | approval
	Surface  string         // ceo | operator
}

type ObserveResult struct {
	Surfaced, Proposed, Applied, Deduped int
	Details                              []string
}

// RunObserver executes one cycle: apply what humans approved, then detect.
func RunObserver(st *store.Store, cfg *config.Config) (*ObserveResult, error) {
	res := &ObserveResult{}
	applied, err := RunObserverApplier(st)
	if err != nil {
		return res, err
	}
	res.Applied = applied

	insights, err := detectAll(st, cfg)
	if err != nil {
		return res, err
	}
	for _, in := range insights {
		if openInsightExists(st, in.Type, in.Subject) {
			res.Deduped++
			continue
		}
		id, err := persistInsight(st, in)
		if err != nil {
			return res, err
		}
		a := &govern.Audit{St: st, Actor: actor}
		switch in.Class {
		case "auto":
			// INTERNAL-only: the insight is now visible in the inbox. No
			// governance state changes, no external write — ever.
			if _, err := st.DB.Exec(`UPDATE insights SET status = 'surfaced' WHERE id = ?`, id); err != nil {
				return res, err
			}
			_, _ = a.Append("observer_surfaced", in.Subject, in.Type)
			res.Surfaced++
		case "approval":
			action := fmt.Sprintf("observer:%s:%d", shortType(in.Type), id)
			ar, err := st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at)
				VALUES(NULL, ?, ?, ?)`, action, in.Summary, store.Now())
			if err != nil {
				return res, err
			}
			approvalID, _ := ar.LastInsertId()
			if _, err := st.DB.Exec(`UPDATE insights SET approval_id = ? WHERE id = ?`, approvalID, id); err != nil {
				return res, err
			}
			_, _ = a.Append("observer_proposed", in.Subject, action+" — "+in.Summary)
			res.Proposed++
		}
		res.Details = append(res.Details, in.Type+": "+in.Subject)
	}
	return res, nil
}

// shortType maps insight types to the compact action namespace token.
func shortType(t string) string {
	switch t {
	case "budget_overshoot_trend":
		return "budget"
	default:
		return t
	}
}

func openInsightExists(st *store.Store, typ, subject string) bool {
	var n int
	_ = st.Read().QueryRow(`SELECT count(*) FROM insights
		WHERE type = ? AND subject = ? AND status IN ('open','surfaced')`, typ, subject).Scan(&n)
	return n > 0
}

func persistInsight(st *store.Store, in Insight) (int64, error) {
	ev, err := json.Marshal(in.Evidence)
	if err != nil {
		return 0, err
	}
	res, err := st.DB.Exec(`INSERT INTO insights(type, subject, summary, evidence, class, surface, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		in.Type, in.Subject, redact.String(in.Summary), string(ev), in.Class, in.Surface, store.Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RunObserverApplier executes human-approved observer actions consume-once.
// Structured params come from insights.evidence — NEVER parsed out of the
// action string (an agent can influence text that ends up in summaries).
func RunObserverApplier(st *store.Store) (int, error) {
	rows, err := st.DB.Query(`SELECT id, action, COALESCE(decided_by,'') FROM approvals
		WHERE action LIKE 'observer:%' AND status = 'approved' AND consumed_at IS NULL`)
	if err != nil {
		return 0, err
	}
	type prop struct {
		id            int64
		action, actor string
	}
	var props []prop
	for rows.Next() {
		var p prop
		if err := rows.Scan(&p.id, &p.action, &p.actor); err != nil {
			rows.Close()
			return 0, err
		}
		props = append(props, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	applied := 0
	for _, p := range props {
		parts := strings.Split(p.action, ":") // observer:<type>:<insight_id>
		insightID, convErr := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		if len(parts) != 3 || convErr != nil {
			consumeApproval(st, p.id)
			a := &govern.Audit{St: st, Actor: actor}
			a.Append("observer_malformed", fmt.Sprintf("approval:%d", p.id), p.action)
			continue
		}
		cr, err := st.DB.Exec(`UPDATE approvals SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`,
			store.Now(), p.id)
		if err != nil {
			return applied, err
		}
		if n, _ := cr.RowsAffected(); n != 1 {
			continue // another worker won the consume race
		}
		if err := applyInsightAction(st, parts[1], insightID, p.actor); err != nil {
			if permanentApplyError(err) {
				// Bad params / gone insight will never succeed — stay
				// consumed, audit, move on (don't loop forever).
				a := &govern.Audit{St: st, Actor: actor}
				a.Append("observer_apply_failed", fmt.Sprintf("approval:%d", p.id), err.Error())
				continue
			}
			// Transient (e.g. DB hiccup): un-consume so it retries.
			st.DB.Exec(`UPDATE approvals SET consumed_at = NULL WHERE id = ?`, p.id)
			return applied, err
		}
		st.DB.Exec(`UPDATE insights SET status = 'resolved', resolved_at = ? WHERE id = ?`, store.Now(), insightID)
		applied++
	}
	return applied, nil
}

func consumeApproval(st *store.Store, id int64) {
	st.DB.Exec(`UPDATE approvals SET consumed_at = ? WHERE id = ?`, store.Now(), id)
}

// errPermanentApply marks a failure that retrying cannot fix (invalid
// params, unknown type, missing insight) — as opposed to a transient DB
// error worth re-running next cycle.
type errPermanentApply struct{ err error }

func (e errPermanentApply) Error() string { return e.err.Error() }

func permanentApplyError(err error) bool {
	_, ok := err.(errPermanentApply)
	return ok
}

// applyInsightAction executes one approved action from its evidence params.
func applyInsightAction(st *store.Store, typ string, insightID int64, decidedBy string) error {
	var evidence string
	if err := st.DB.QueryRow(`SELECT evidence FROM insights WHERE id = ?`, insightID).Scan(&evidence); err != nil {
		return errPermanentApply{fmt.Errorf("insight %d: %w", insightID, err)}
	}
	a := &govern.Audit{St: st, Actor: decidedBy}
	switch typ {
	case "budget":
		var ev struct {
			ScopeType      string  `json:"scope_type"`
			ScopeID        string  `json:"scope_id"`
			SuggestedLimit float64 `json:"suggested_limit"`
		}
		if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
			return errPermanentApply{err}
		}
		if ev.SuggestedLimit <= 0 || ev.ScopeType == "" {
			return errPermanentApply{fmt.Errorf("insight %d: invalid budget params", insightID)}
		}
		if _, err := st.DB.Exec(`INSERT INTO budgets(scope_type, scope_id, limit_usd)
			VALUES(?, ?, ?) ON CONFLICT(scope_type, scope_id) DO UPDATE SET limit_usd = excluded.limit_usd`,
			ev.ScopeType, ev.ScopeID, ev.SuggestedLimit); err != nil {
			return err
		}
		_, err := a.Append("observer_budget_applied", ev.ScopeType+":"+ev.ScopeID,
			fmt.Sprintf("limit → $%.2f (insight #%d)", ev.SuggestedLimit, insightID))
		return err
	case "kill":
		var ev struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
			return errPermanentApply{err}
		}
		if ev.RunID == "" {
			return errPermanentApply{fmt.Errorf("insight %d: missing run_id", insightID)}
		}
		if _, err := st.DB.Exec(`UPDATE runs SET status = 'killed', ended_at = COALESCE(ended_at, ?)
			WHERE id = ?`, store.Now(), ev.RunID); err != nil {
			return err
		}
		_, err := a.Append("observer_kill_applied", ev.RunID, fmt.Sprintf("run killed (insight #%d)", insightID))
		return err
	case "band":
		var ev struct {
			AgentID string `json:"agent_id"`
			Band    string `json:"band"`
		}
		if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
			return errPermanentApply{err}
		}
		if ev.AgentID == "" || !govern.ValidBand(ev.Band) {
			return errPermanentApply{fmt.Errorf("insight %d: invalid band params", insightID)}
		}
		return govern.SetBand(st, ev.AgentID, ev.Band, decidedBy, fmt.Sprintf("approved request (insight #%d)", insightID))
	default:
		return errPermanentApply{fmt.Errorf("unknown observer action type %q", typ)}
	}
}

// RequestAction is the shared "propose, never execute" entry point: it
// persists an insight holding the structured params and opens an approval
// request in the observer namespace. Used by the observer's own detectors
// and by the CEO chatbot's action tools — neither may mutate state directly.
func RequestAction(st *store.Store, typ, subject, summary string, params map[string]any, requestedBy string) (int64, error) {
	id, err := persistInsight(st, Insight{
		Type: "request_" + typ, Subject: subject, Summary: summary,
		Evidence: params, Class: "approval", Surface: "ceo",
	})
	if err != nil {
		return 0, err
	}
	action := fmt.Sprintf("observer:%s:%d", typ, id)
	ar, err := st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at)
		VALUES(NULL, ?, ?, ?)`, action, summary, store.Now())
	if err != nil {
		return 0, err
	}
	approvalID, _ := ar.LastInsertId()
	if _, err := st.DB.Exec(`UPDATE insights SET approval_id = ? WHERE id = ?`, approvalID, id); err != nil {
		return 0, err
	}
	a := &govern.Audit{St: st, Actor: requestedBy}
	_, _ = a.Append("action_requested", subject, action+" — "+summary)
	return approvalID, nil
}
