package web

import (
	"strconv"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
)

// RunRow is a run as displayed in lists and timelines.
type RunRow struct {
	ID, SessionID, AgentID, Project, TaskKey, Status, Model string
	StartedAt, EndedAt, Source, Runtime                     string
	CostUSD                                                 float64
	InputTokens, OutputTokens                               int64
	LinesAdded, LinesDeleted                                int64
}

// EventRow is one timeline entry on the run detail page.
type EventRow struct {
	ID             int64
	TS, Kind, Tool string
	OK             *int64
	Payload        string
}

// ApprovalRow is one review-queue item.
type ApprovalRow struct {
	ID                                      int64
	RunID, Action, Reason, Status           string
	RequestedAt, DecidedAt, DecidedBy, Note string
	Channel                                 string
}

// BudgetRow shows a budget scope with its live spend.
type BudgetRow struct {
	ScopeType, ScopeID string
	LimitUSD, SpendUSD float64
	Pct                float64
}

// FlagRow is an open flag needing attention.
type FlagRow struct {
	ID              int64
	RunID, Reason   string
	Status, JiraKey string
	CreatedAt       string
}

// RuleRow mirrors guardrail_rules for the toggle table.
type RuleRow struct {
	ID                         int64
	Kind, Pattern, Description string
	Enabled                    bool
}

const listCap = 200 // hard cap for list queries (UI pages, no pagination yet)

func (s *Server) queryRuns(where string, args ...any) ([]RunRow, error) {
	q := `SELECT id, session_id, COALESCE(agent_id,''), COALESCE(project,''), COALESCE(task_key,''),
		status, COALESCE(model,''), COALESCE(started_at,''), COALESCE(ended_at,''), source, runtime,
		cost_usd, input_tokens, output_tokens, lines_added, lines_deleted
		FROM runs ` + where + ` ORDER BY started_at DESC LIMIT ?`
	rows, err := s.Store.DB.Query(q, append(args, listCap)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRow
	for rows.Next() {
		var r RunRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.AgentID, &r.Project, &r.TaskKey, &r.Status,
			&r.Model, &r.StartedAt, &r.EndedAt, &r.Source, &r.Runtime,
			&r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.LinesAdded, &r.LinesDeleted); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Server) queryEvents(runID string) ([]EventRow, error) {
	rows, err := s.Store.DB.Query(`SELECT id, ts, kind, COALESCE(tool_name,''), ok, COALESCE(payload,'')
		FROM events WHERE run_id = ? ORDER BY ts, id LIMIT ?`, runID, 500)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &e.Tool, &e.OK, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Server) queryApprovals(status string) ([]ApprovalRow, error) {
	q := `SELECT id, COALESCE(run_id,''), action, COALESCE(reason,''), status, requested_at,
		COALESCE(decided_at,''), COALESCE(decided_by,''), COALESCE(decision_note,''), channel
		FROM approvals`
	args := []any{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	rows, err := s.Store.DB.Query(q, append(args, listCap)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApprovalRow
	for rows.Next() {
		var a ApprovalRow
		if err := rows.Scan(&a.ID, &a.RunID, &a.Action, &a.Reason, &a.Status, &a.RequestedAt,
			&a.DecidedAt, &a.DecidedBy, &a.Note, &a.Channel); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Server) queryFlags(status string) ([]FlagRow, error) {
	rows, err := s.Store.DB.Query(`SELECT id, COALESCE(run_id,''), COALESCE(reason,''), status,
		COALESCE(jira_key,''), created_at FROM flags WHERE status = ? ORDER BY id DESC LIMIT ?`, status, listCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FlagRow
	for rows.Next() {
		var f FlagRow
		if err := rows.Scan(&f.ID, &f.RunID, &f.Reason, &f.Status, &f.JiraKey, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// queryBudgets lists configured budgets (plus the config global default) with
// current-month spend from the govern engine.
func (s *Server) queryBudgets() ([]BudgetRow, error) {
	eng := govern.NewEngine(s.Cfg, s.Store)
	rows, err := s.Store.DB.Query(`SELECT scope_type, scope_id, limit_usd FROM budgets ORDER BY scope_type, scope_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BudgetRow
	haveGlobal := false
	for rows.Next() {
		var b BudgetRow
		if err := rows.Scan(&b.ScopeType, &b.ScopeID, &b.LimitUSD); err != nil {
			return nil, err
		}
		if b.ScopeType == "global" {
			haveGlobal = true
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !haveGlobal {
		out = append([]BudgetRow{{ScopeType: "global", LimitUSD: s.Cfg.Budget.GlobalMonthlyUSD}}, out...)
	}
	for i := range out {
		out[i].SpendUSD, _ = eng.SpendMonth(out[i].ScopeType, out[i].ScopeID)
		if out[i].LimitUSD > 0 {
			out[i].Pct = out[i].SpendUSD / out[i].LimitUSD * 100
		}
	}
	return out, nil
}

func (s *Server) queryRules() ([]RuleRow, error) {
	rows, err := s.Store.DB.Query(`SELECT id, kind, pattern, COALESCE(description,''), enabled
		FROM guardrail_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuleRow
	for rows.Next() {
		var r RuleRow
		var en int
		if err := rows.Scan(&r.ID, &r.Kind, &r.Pattern, &r.Description, &en); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// costTrend returns daily fleet cost for the last n days (org chart data).
func (s *Server) costTrend(days int) (labels []string, values []float64, err error) {
	rows, err := s.Store.DB.Query(`SELECT substr(started_at, 1, 10) d, COALESCE(sum(cost_usd),0)
		FROM runs WHERE started_at >= datetime('now', ?)
		GROUP BY d ORDER BY d`, "-"+itoa(days)+" days")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var v float64
		if err := rows.Scan(&d, &v); err != nil {
			return nil, nil, err
		}
		labels = append(labels, d)
		values = append(values, v)
	}
	return labels, values, rows.Err()
}

func itoa(n int) string { return strconv.Itoa(n) }

// leaderboard is a thin wrapper so handlers don't import learn directly.
func (s *Server) leaderboard() ([]learn.LeaderboardRow, error) {
	return learn.Leaderboard(s.Store, s.Cfg.LearnWindowDays)
}
