package observer

import (
	"fmt"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// MVP detectors. Each is small and independently testable; summaries are
// plain Vietnamese because the CEO inbox renders them verbatim.
// rule_candidate_from_repeated_blocks is deliberately NOT here ([Sau]):
// auto-proposing guardrail regexes needs an operator-reviewed design.

// Thresholds — named so the UI/docs can cite them, tuned coarse on purpose.
const (
	overshootMarginPct   = 20  // projected EOM ≥ limit × 1.2 → propose raise
	underusedMinFleetRun = 20  // fleet needs some volume before "underused" means anything
	underusedSharePct    = 5   // agent share below this → underused
	overSteerPerRun      = 5.0 // avg mid-run msgs per run — HIGH on purpose
	overSteerMinRuns     = 10  // sustained, not a bad afternoon
	// defaultGateMinGrade is the fallback playbook-candidate grade floor when
	// the operator has not set the UE3 "gate_min_grade" setting yet.
	defaultGateMinGrade = "C"
)

// gradeRank orders letters worst→best so minGrade comparisons ("is this run's
// grade at least the configured floor?") are a simple rank comparison.
var gradeRank = map[string]int{"F": 0, "D": 1, "C": 2, "B": 3, "A": 4}

// minGrade is UE3's global quality-gate floor: the minimum grade a run needs
// to qualify as a playbook candidate. Read from the settings table (set via
// the /gate-thresholds form); falls back to defaultGateMinGrade when unset so
// existing behavior is preserved until an operator opts into a stricter (or
// looser) floor.
func minGrade(st *store.Store) string {
	if v := st.Setting("gate_min_grade"); v != "" {
		if _, ok := gradeRank[v]; ok {
			return v
		}
	}
	return defaultGateMinGrade
}

// meetsMinGrade reports whether letter is at or above floor in the A>B>C>D>F
// ranking (unknown letters never meet the floor).
func meetsMinGrade(letter, floor string) bool {
	lr, ok1 := gradeRank[letter]
	fr, ok2 := gradeRank[floor]
	return ok1 && ok2 && lr >= fr
}

func detectAll(st *store.Store, cfg *config.Config) ([]Insight, error) {
	var out []Insight
	board, err := learn.LeaderboardCalibrated(st, cfg.LearnWindowDays, cfg.CalibrateWithHumans)
	if err != nil {
		return nil, err
	}
	out = append(out, detectBudgetOvershoot(st, cfg)...)
	out = append(out, detectAgentUnderused(board)...)
	ops, err := detectOperatorOverSteering(st, cfg)
	if err != nil {
		return nil, err
	}
	out = append(out, ops...)
	pbs, err := detectPlaybookCandidates(st, board)
	if err != nil {
		return nil, err
	}
	return append(out, pbs...), nil
}

// detectBudgetOvershoot projects end-of-month spend from the month-to-date
// burn rate; a sustained overshoot proposes a limit raise (CEO decides).
func detectBudgetOvershoot(st *store.Store, cfg *config.Config) []Insight {
	limit := cfg.Budget.GlobalMonthlyUSD
	if limit <= 0 {
		return nil
	}
	var spent float64
	var dayOfMonth, daysInMonth int
	err := st.Read().QueryRow(`SELECT COALESCE(sum(cost_usd),0),
		CAST(strftime('%d','now') AS INTEGER),
		CAST(strftime('%d', date('now','start of month','+1 month','-1 day')) AS INTEGER)
		FROM runs WHERE started_at >= strftime('%Y-%m-01T00:00:00Z','now')`).
		Scan(&spent, &dayOfMonth, &daysInMonth)
	if err != nil {
		return nil
	}
	projected := spent / float64(dayOfMonth) * float64(daysInMonth)
	alreadyOver := spent >= limit
	// Early-month projections are noise — trust them only from day 5; an
	// outright breach counts on any day.
	projectedOver := dayOfMonth >= 5 && projected >= limit*(1+overshootMarginPct/100.0)
	if !alreadyOver && !projectedOver {
		return nil
	}
	if projected < spent {
		projected = spent
	}
	suggested := roundUp10(projected * 1.1)
	return []Insight{{
		Type:    "budget_overshoot_trend",
		Subject: "global",
		Summary: fmt.Sprintf("Chi tiêu tháng này dự kiến $%.0f, vượt ngân sách $%.0f. Đề xuất nâng ngân sách lên $%.0f — cần bạn duyệt.",
			projected, limit, suggested),
		Evidence: map[string]any{
			"scope_type": "global", "scope_id": "",
			"spent_to_date": spent, "projected_eom": projected,
			"current_limit": limit, "suggested_limit": suggested,
		},
		Class:   "approval",
		Surface: "ceo",
	}}
}

func roundUp10(v float64) float64 {
	n := int(v/10) * 10
	if float64(n) < v {
		n += 10
	}
	return float64(n)
}

// detectAgentUnderused flags capable agents doing a tiny share of the work —
// an assignment suggestion, never a state change.
func detectAgentUnderused(board []learn.LeaderboardRow) []Insight {
	total := 0
	for _, r := range board {
		total += r.Runs
	}
	if total < underusedMinFleetRun {
		return nil
	}
	var out []Insight
	for _, r := range board {
		share := 100 * float64(r.Runs) / float64(total)
		goodGrade := r.Grade.Letter == "A" || r.Grade.Letter == "B"
		if goodGrade && !r.Grade.LowConfidence && share < underusedSharePct {
			out = append(out, Insight{
				Type:    "agent_underused",
				Subject: r.AgentID,
				Summary: fmt.Sprintf("Agent %s đạt hạng %s nhưng chỉ nhận %.0f%% khối lượng việc — cân nhắc giao thêm task cho agent này.",
					r.AgentName, r.Grade.Letter, share),
				Evidence: map[string]any{"grade": r.Grade.Letter, "runs": r.Runs, "share_pct": share},
				Class:    "auto",
				Surface:  "ceo",
			})
		}
	}
	return out
}

// detectOperatorOverSteering is a PRIVATE coaching signal for a tech lead:
// high threshold, sustained volume, and the caveat travels in the summary.
// Never published, never a ranking (see learn.OperatorCaveat).
func detectOperatorOverSteering(st *store.Store, cfg *config.Config) ([]Insight, error) {
	rows, err := st.Read().Query(`SELECT r.operator_id, count(*),
		COALESCE(avg(COALESCE((SELECT CAST(e.payload AS REAL) FROM events e
		              WHERE e.run_id = r.id AND e.kind = 'user_msg' LIMIT 1), 0)), 0)
		FROM runs r WHERE r.operator_id IS NOT NULL
		AND r.started_at >= datetime('now', ?)
		GROUP BY r.operator_id`, fmt.Sprintf("-%d days", cfg.LearnWindowDays))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Insight
	for rows.Next() {
		var op string
		var runs int
		var steer float64
		if err := rows.Scan(&op, &runs, &steer); err != nil {
			return nil, err
		}
		if runs >= overSteerMinRuns && steer >= overSteerPerRun {
			out = append(out, Insight{
				Type:    "operator_over_steering",
				Subject: op,
				Summary: fmt.Sprintf("Operator %s can thiệp trung bình %.1f lần/run trên %d run — có thể task giao chưa đủ rõ, hoặc agent chưa hợp việc. LƯU Ý: can thiệp nhiều khi là điều ĐÚNG; đây là gợi ý coaching riêng tư, không phải xếp hạng.",
					op, steer, runs),
				Evidence: map[string]any{"steering_avg": steer, "runs": runs, "private": true},
				Class:    "auto",
				Surface:  "operator",
			})
		}
	}
	return out, rows.Err()
}

// detectPlaybookCandidates surfaces high-grade agents' clean recent runs
// that are not captured as playbooks yet — the flywheel's intake (P4).
func detectPlaybookCandidates(st *store.Store, board []learn.LeaderboardRow) ([]Insight, error) {
	floor := minGrade(st)
	var out []Insight
	for _, r := range board {
		if r.Grade.LowConfidence || !meetsMinGrade(r.Grade.Letter, floor) {
			continue
		}
		var runID string
		err := st.Read().QueryRow(`SELECT r.id FROM runs r
			WHERE r.agent_id = ? AND r.status = 'done'
			AND NOT EXISTS (SELECT 1 FROM playbooks p WHERE p.run_id = r.id)
			AND NOT EXISTS (SELECT 1 FROM events e WHERE e.run_id = r.id AND e.kind = 'tool_result' AND e.ok = 0)
			ORDER BY r.started_at DESC LIMIT 1`, r.AgentID).Scan(&runID)
		if err != nil {
			continue // no clean un-captured run — fine
		}
		out = append(out, Insight{
			Type:    "playbook_candidate",
			Subject: r.AgentID,
			Summary: fmt.Sprintf("Run %s của agent %s (hạng %s) chạy sạch không lỗi — nên lưu thành playbook để nhân bản cách làm tốt.",
				shortID(runID), r.AgentName, r.Grade.Letter),
			Evidence: map[string]any{"run_id": runID, "grade": r.Grade.Letter},
			Class:    "auto",
			Surface:  "ceo",
		})
	}
	return out, nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
