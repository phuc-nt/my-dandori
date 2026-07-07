package web

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

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
	// ImportContent carries the FULL pinned Drive body for context-import
	// approvals only (C1 — this IS the operator inbox where the human
	// approves; the body must be visible here, not truncated, so a payload
	// cannot hide past a preview boundary). Empty for every other action type.
	ImportContent string
	ImportRunes   int
	// Impact is an advisory estimate (avg cost/files of similar past approvals
	// by this agent), nil when there is not enough history. Populated only for
	// pending items on the reviews/exec inbox.
	Impact *learn.Impact
	// Knowledge* carries the FULL pinned evidence for observer:knowledge-*
	// approvals (F1 CRITICAL) — the decide surface (/reviews) must render the
	// exact body+hash the reviewer is approving, never a truncated summary,
	// regardless of what the live knowledge_units row looks like by the time
	// this renders. Empty for every other action type.
	KnowledgeKind, KnowledgeName, KnowledgeBody, KnowledgeContentHash string
	KnowledgeLayer, KnowledgeLayerTarget                              string
	// KnowledgeRuleIntent (H1) is the pinned rule lifecycle effect
	// ("enable"/"retire"/"scope-up") — must render PROMINENTLY distinct from
	// a plain publish so an admin approving "gỡ rule" can see that is exactly
	// what they are about to execute, not a toggle-on.
	KnowledgeRuleIntent string
	// KnowledgeUnitID + KnowledgeCI (M4): link to the full /knowledge/unit/{id}
	// detail page and a compact CI/stats line, read from the unit row (the
	// evidence blob doesn't carry stats — the unit row is the single stats
	// source of truth, same one /knowledge/unit/{id} itself reads).
	KnowledgeUnitID int64
	KnowledgeCI     string
	// KnowledgeOrigin/KnowledgeOriginModel (v13 P2 anti-Goodhart badge) are the
	// PINNED origin fields (same pin rationale as KnowledgeBody above) so a
	// reviewer approving at /reviews sees who/what authored the content —
	// human, imported, ai-draft (+model), or detector — before approving.
	KnowledgeOrigin      string
	KnowledgeOriginModel string
	// KnowledgeKitFiles (P4) carries the per-file rows for a kind=kit approval
	// — path/size for the manifest list, full body for expand — recomputed
	// from knowledge_kit_files at render time so the list a reviewer sees
	// always matches the SAME rows the applier will re-verify against the
	// pinned manifest hash (H1). Empty for every non-kit action.
	KnowledgeKitFiles []learn.KitFileRow
}

// BudgetRow shows a budget scope with its live spend and end-of-month
// projection (linear burn rate — stated on the UI).
type BudgetRow struct {
	ScopeType, ScopeID string
	LimitUSD, SpendUSD float64
	Pct, ProjectedUSD  float64
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
	ScopeType, ScopeID         string
	Enabled, Critical          bool
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
	var out []ApprovalRow
	for rows.Next() {
		var a ApprovalRow
		if err := rows.Scan(&a.ID, &a.RunID, &a.Action, &a.Reason, &a.Status, &a.RequestedAt,
			&a.DecidedAt, &a.DecidedBy, &a.Note, &a.Channel); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// Second pass, after the approvals cursor is closed: Store.DB is a
	// single-connection pool (SetMaxOpenConns(1)), so a nested query while
	// `rows` is still open would deadlock waiting for the same connection.
	for i := range out {
		if strings.HasPrefix(out[i].Action, "observer:context-import:") {
			s.loadImportContent(&out[i])
		}
		if strings.HasPrefix(out[i].Action, "observer:knowledge-") {
			s.loadKnowledgeEvidence(&out[i])
		}
		// Advisory impact estimate for pending items (the ones a human is about
		// to decide). History-based, cached; skipped for decided rows.
		if out[i].Status == "pending" && out[i].RunID != "" {
			if agent := s.runAgent(out[i].RunID); agent != "" {
				if im, ok := learn.EstimateImpact(s.Store, agent, out[i].Action); ok {
					out[i].Impact = im
				}
			}
		}
	}
	return out, nil
}

// runAgent returns the agent_id for a run id, or "" if unknown.
func (s *Server) runAgent(runID string) string {
	var agent string
	_ = s.Store.DB.QueryRow(`SELECT COALESCE(agent_id,'') FROM runs WHERE id = ?`, runID).Scan(&agent)
	return agent
}

// loadImportContent fills ImportContent/ImportRunes for a context-import
// approval by reading the linked insight's evidence (C1 — the operator inbox
// at /reviews is where the human actually approves, so the FULL pinned body
// must render here, not just on the pre-submission /contexts preview).
// Best-effort: a lookup failure leaves ImportContent empty rather than
// failing the whole review-queue render.
func (s *Server) loadImportContent(a *ApprovalRow) {
	var evidence string
	if err := s.Store.DB.QueryRow(
		`SELECT evidence FROM insights WHERE approval_id = ?`, a.ID,
	).Scan(&evidence); err != nil {
		return
	}
	var ev struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		return
	}
	a.ImportContent = ev.Content
	a.ImportRunes = len([]rune(ev.Content))
}

// loadKnowledgeEvidence fills Knowledge* for an observer:knowledge-* approval
// (F1 CRITICAL) by reading the linked insight's evidence — the exact pinned
// params RequestPublish/Mandate/Retire wrote (learn.unitActionParams JSON
// shape). This is the ONLY render source for the decide surface: /reviews
// must show the FULL body+hash a reviewer is approving, not a summary, and
// not the live knowledge_units row (which may have since changed lineage,
// though content itself is immutable post-approve). Best-effort: a lookup
// failure leaves the Knowledge* fields empty rather than failing the whole
// queue render.
func (s *Server) loadKnowledgeEvidence(a *ApprovalRow) {
	var evidence string
	if err := s.Store.DB.QueryRow(
		`SELECT evidence FROM insights WHERE approval_id = ?`, a.ID,
	).Scan(&evidence); err != nil {
		return
	}
	var ev struct {
		UnitID      int64  `json:"unit_id"`
		Kind        string `json:"kind"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		ContentHash string `json:"content_hash"`
		Layer       string `json:"layer"`
		LayerTarget string `json:"layer_target"`
		RuleIntent  string `json:"rule_intent"`
		Origin      string `json:"origin"`
		OriginModel string `json:"origin_model"`
	}
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		return
	}
	a.KnowledgeUnitID = ev.UnitID
	a.KnowledgeKind = ev.Kind
	a.KnowledgeName = ev.Name
	a.KnowledgeBody = ev.Body
	a.KnowledgeContentHash = ev.ContentHash
	a.KnowledgeLayer = ev.Layer
	a.KnowledgeLayerTarget = ev.LayerTarget
	a.KnowledgeRuleIntent = ev.RuleIntent
	a.KnowledgeOrigin = ev.Origin
	a.KnowledgeOriginModel = ev.OriginModel
	// P4 H1: kit's real per-file bodies live outside the pinned evidence blob
	// (the blob only carries the manifest as Body) — load the SAME
	// knowledge_kit_files rows the applier will re-verify against, so a
	// reviewer approving KnowledgeContentHash can also expand and read every
	// file it covers.
	if ev.Kind == learn.KindKit && ev.UnitID != 0 {
		if files, err := learn.KitFiles(s.Store, ev.UnitID); err == nil {
			a.KnowledgeKitFiles = files
		}
	}
	// M4: CI/stats line, read from the live unit row (single source of truth
	// for stats — the evidence blob only pins body/hash, not the numbers).
	if ev.UnitID != 0 {
		if u, err := learn.GetUnit(s.Store, ev.UnitID); err == nil && u != nil && u.NPresent != nil {
			present := *u.NPresent
			donePresent := 0.0
			if u.DonePresent != nil {
				donePresent = *u.DonePresent
			}
			doneCount := int(donePresent*float64(present) + 0.5)
			a.KnowledgeCI = fmt.Sprintf("n=%d, %s", present, learn.FormatWilson(doneCount, present))
		}
	}
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
	now := time.Now().UTC()
	dayOfMonth := float64(now.Day())
	daysInMonth := float64(time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day())
	for i := range out {
		out[i].SpendUSD, _ = eng.SpendMonth(out[i].ScopeType, out[i].ScopeID)
		if out[i].LimitUSD > 0 {
			out[i].Pct = out[i].SpendUSD / out[i].LimitUSD * 100
		}
		if dayOfMonth > 0 {
			out[i].ProjectedUSD = out[i].SpendUSD / dayOfMonth * daysInMonth
		}
	}
	return out, nil
}

func (s *Server) queryRules() ([]RuleRow, error) {
	rows, err := s.Store.DB.Query(`SELECT id, kind, pattern, COALESCE(description,''), enabled, critical, scope_type, scope_id
		FROM guardrail_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuleRow
	for rows.Next() {
		var r RuleRow
		var en, crit int
		if err := rows.Scan(&r.ID, &r.Kind, &r.Pattern, &r.Description, &en, &crit, &r.ScopeType, &r.ScopeID); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		r.Critical = crit == 1
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

// queryBands maps agent id → autonomy band (missing rows = gated default).
func (s *Server) queryBands() map[string]string {
	out := map[string]string{}
	rows, err := s.Store.DB.Query(`SELECT agent_id, band FROM agent_bands`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, band string
		if rows.Scan(&id, &band) == nil {
			out[id] = band
		}
	}
	return out
}

// leaderboard is a thin wrapper so handlers don't import learn directly.
func (s *Server) leaderboard() ([]learn.LeaderboardRow, error) {
	return learn.LeaderboardCalibrated(s.Store, s.Cfg.LearnWindowDays, s.Cfg.CalibrateWithHumans)
}
