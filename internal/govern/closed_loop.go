package govern

import (
	"fmt"
	"strings"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// Closed loop (G8): the GOVERN reflex the vision promises — low grades act
// by themselves instead of waiting for a human to notice a dashboard.
//
//	F → flag + Jira + auto-demote to supervised (asymmetric safety)
//	D → flag + Jira + a demote PROPOSAL in the review queue (human decides)
//	recovered (A/B/C) → open low-grade flags auto-resolve
//
// Every step lands in the audit chain. Deduped: one open low-grade flag per
// agent at a time.
const loopActor = "dandori-closed-loop"

// minRunsForLoop matches the LowConfidence threshold (5 runs): the loop must
// never act on a grade the UI itself greys out as tentative.
const minRunsForLoop = 5

type LoopResult struct {
	Flagged, Demoted, Proposed, Resolved, Applied int
	Details                                       []string
}

// RunClosedLoop executes one governance cycle. flagSink (nil-able) receives
// new flag ids for the external Jira leg (DRY_RUN-guarded by the caller).
func RunClosedLoop(st *store.Store, cfg *config.Config, flagSink func(int64)) (*LoopResult, error) {
	res := &LoopResult{}
	if n, err := applyApprovedDemotes(st, res); err != nil {
		return res, err
	} else {
		res.Applied = n
	}
	board, err := learn.LeaderboardCalibrated(st, cfg.LearnWindowDays, cfg.CalibrateWithHumans)
	if err != nil {
		return res, err
	}
	for _, row := range board {
		switch row.Grade.Letter {
		case "D", "F":
			if row.Runs < minRunsForLoop {
				continue
			}
			if err := actOnLowGrade(st, cfg, row, flagSink, res); err != nil {
				return res, err
			}
		default:
			if err := resolveRecovered(st, row.AgentID, res); err != nil {
				return res, err
			}
		}
	}
	// Surface flags that have sat open too long, so they reach a human via the
	// Slack Alerter (which picks up the flag_stale events). Best-effort: a
	// failure here must not abort the cycle above.
	scanStaleFlags(st, staleFlagDays(cfg))
	return res, nil
}

// staleFlagDays is the age (in days) past which an open flag is considered
// neglected. Configurable; defaults to 3.
func staleFlagDays(cfg *config.Config) int {
	if cfg.NotifyFlagStaleDays > 0 {
		return cfg.NotifyFlagStaleDays
	}
	return 3
}

// scanStaleFlags emits one flag_stale event per open flag older than days,
// deduped so a flag is announced at most once (no repeat every tick).
func scanStaleFlags(st *store.Store, days int) {
	rows, err := st.DB.Query(`SELECT f.id, COALESCE(r.agent_id,''), f.created_at
		FROM flags f LEFT JOIN runs r ON r.id = f.run_id
		WHERE f.status = 'open'
		  AND f.created_at <= datetime('now', ?)
		  AND NOT EXISTS (
		    SELECT 1 FROM events e WHERE e.kind = 'flag_stale' AND e.payload LIKE '%#' || f.id || ' %'
		  )`, fmt.Sprintf("-%d days", days))
	if err != nil {
		return
	}
	defer rows.Close()
	type stale struct {
		id      int64
		agent   string
		created string
	}
	var flags []stale
	for rows.Next() {
		var s stale
		if err := rows.Scan(&s.id, &s.agent, &s.created); err != nil {
			return
		}
		flags = append(flags, s)
	}
	for _, s := range flags {
		payload := fmt.Sprintf("cảnh báo #%d %s mở quá %d ngày chưa xử lý", s.id, s.agent, days)
		_, _ = st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
			VALUES(NULL, ?, 'flag_stale', ?, 0, ?)`, store.Now(), s.agent, payload)
	}
}

func openLowGradeFlag(st *store.Store, agentID string) (int64, bool) {
	var id int64
	err := st.DB.QueryRow(`SELECT f.id FROM flags f JOIN runs r ON r.id = f.run_id
		WHERE r.agent_id = ? AND f.status = 'open' AND f.reason LIKE 'low grade%'
		ORDER BY f.id DESC LIMIT 1`, agentID).Scan(&id)
	return id, err == nil
}

func actOnLowGrade(st *store.Store, cfg *config.Config, row learn.LeaderboardRow, flagSink func(int64), res *LoopResult) error {
	if _, exists := openLowGradeFlag(st, row.AgentID); exists {
		return nil // already flagged, awaiting review — no spam
	}
	lastRun := latestRunID(st, row.AgentID)
	reason := fmt.Sprintf("low grade %s: composite %.0f over %d runs (acceptance %.0f · success %.0f · autonomy %.0f · reliability %.0f)",
		row.Grade.Letter, row.Composite, row.Runs,
		row.Metrics.Acceptance.Value, row.Metrics.Success.Value,
		row.Metrics.Autonomy.Value, row.Metrics.Reliability.Value)
	fr, err := st.DB.Exec(`INSERT INTO flags(run_id, reason, created_at) VALUES(?, ?, ?)`,
		lastRun, reason, store.Now())
	if err != nil {
		return err
	}
	flagID, _ := fr.LastInsertId()
	a := &Audit{St: st, Actor: loopActor}
	if _, err := a.Append("closed_loop_flag", row.AgentID, reason); err != nil {
		return err
	}
	if flagSink != nil {
		flagSink(flagID)
	}
	_, _ = st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(NULL, ?, 'closed_loop', ?, 0, ?)`, store.Now(), row.AgentID, reason)
	res.Flagged++
	res.Details = append(res.Details, row.AgentID+": "+reason)

	switch row.Grade.Letter {
	case "F":
		if BandFor(st, row.AgentID) != BandSupervised {
			if err := SetBand(st, row.AgentID, BandSupervised, loopActor, "closed-loop: grade F"); err != nil {
				return err
			}
			res.Demoted++
		}
	case "D":
		if BandFor(st, row.AgentID) != BandSupervised {
			if err := proposeDemote(st, row.AgentID, lastRun, reason); err != nil {
				return err
			}
			res.Proposed++
		}
	}
	return nil
}

// proposeDemote opens a review-queue approval; nobody blocks on it — it is
// applied by applyApprovedDemotes on a later cycle once a human approves.
func proposeDemote(st *store.Store, agentID, runID, why string) error {
	action := "band-demote:" + agentID + ":" + BandSupervised
	var exists int
	st.DB.QueryRow(`SELECT count(*) FROM approvals WHERE action = ? AND status = 'pending'`, action).Scan(&exists)
	if exists > 0 {
		return nil
	}
	_, err := st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at)
		VALUES(?, ?, ?, ?)`, runID, action, "closed-loop proposal — "+why, store.Now())
	return err
}

// applyApprovedDemotes applies human-approved band proposals (consume-once).
func applyApprovedDemotes(st *store.Store, res *LoopResult) (int, error) {
	rows, err := st.DB.Query(`SELECT id, action, COALESCE(decided_by,'') FROM approvals
		WHERE action LIKE 'band-demote:%' AND status = 'approved' AND consumed_at IS NULL`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type prop struct {
		id            int64
		action, actor string
	}
	var props []prop
	for rows.Next() {
		var p prop
		if err := rows.Scan(&p.id, &p.action, &p.actor); err != nil {
			return 0, err
		}
		props = append(props, p)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	applied := 0
	for _, p := range props {
		parts := strings.Split(p.action, ":") // band-demote:<agent>:<band>
		if len(parts) != 3 || !ValidBand(parts[2]) {
			// Malformed action: consume so it stops reappearing, audit why.
			st.DB.Exec(`UPDATE approvals SET consumed_at = ? WHERE id = ?`, store.Now(), p.id)
			a := &Audit{St: st, Actor: loopActor}
			a.Append("proposal_malformed", fmt.Sprintf("approval:%d", p.id), p.action)
			continue
		}
		cr, err := st.DB.Exec(`UPDATE approvals SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`, store.Now(), p.id)
		if err != nil {
			return applied, err
		}
		if n, _ := cr.RowsAffected(); n != 1 {
			continue // another worker won the consume race
		}
		if err := SetBand(st, parts[1], parts[2], p.actor, fmt.Sprintf("approved proposal #%d", p.id)); err != nil {
			// Compensate: un-consume so the approval is retried next cycle
			// instead of being silently lost.
			st.DB.Exec(`UPDATE approvals SET consumed_at = NULL WHERE id = ?`, p.id)
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// resolveRecovered closes open low-grade flags for agents back at C or above.
func resolveRecovered(st *store.Store, agentID string, res *LoopResult) error {
	id, ok := openLowGradeFlag(st, agentID)
	if !ok {
		return nil
	}
	if _, err := st.DB.Exec(`UPDATE flags SET status = 'resolved' WHERE id = ?`, id); err != nil {
		return err
	}
	a := &Audit{St: st, Actor: loopActor}
	if _, err := a.Append("flag_resolved", agentID, fmt.Sprintf("flag #%d: grade recovered", id)); err != nil {
		return err
	}
	res.Resolved++
	return nil
}

func latestRunID(st *store.Store, agentID string) string {
	var id string
	_ = st.DB.QueryRow(`SELECT id FROM runs WHERE agent_id = ? ORDER BY started_at DESC LIMIT 1`, agentID).Scan(&id)
	return id
}
