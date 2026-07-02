package learn

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/redact"
	"github.com/phuc-nt/dandori/internal/store"
)

// Flywheel: detect what top performers do differently, package it as a
// playbook PATTERN, and measure whether adopters improve. Publication
// content is patterns and playbooks only — never a human ranking (Goodhart:
// publish steering counts and people stop steering, even when they should).

// Candidate is a clean, well-structured run worth promoting to a playbook.
type Candidate struct {
	RunID     string
	AgentID   string
	AgentName string
	TaskKey   string
	Model     string
	CostUSD   float64
	Why       string // plain Vietnamese pattern description
}

// adoptionMinRuns is how many subsequent runs an adopter needs before the
// after-metric means anything.
const adoptionMinRuns = 3

// DetectCandidates finds recent runs showing the top-performer pattern:
// finished clean (no tool errors), driven with a specific opening prompt
// (file paths / task ref / acceptance criteria), little steering, and not
// already captured as a playbook.
func DetectCandidates(st *store.Store, windowDays int) ([]Candidate, error) {
	rows, err := st.Read().Query(`
		SELECT r.id, r.agent_id, a.name, COALESCE(r.task_key,''), COALESCE(r.model,''), r.cost_usd,
		       COALESCE((SELECT e.payload FROM events e WHERE e.run_id = r.id AND e.kind = 'prompt_proxy'), ''),
		       COALESCE((SELECT CAST(e.payload AS INTEGER) FROM events e WHERE e.run_id = r.id AND e.kind = 'user_msg'), 0)
		FROM runs r JOIN agents a ON a.id = r.agent_id
		WHERE r.status = 'done'
		  AND r.started_at >= ` + windowClause(windowDays) + `
		  AND NOT EXISTS (SELECT 1 FROM playbooks p WHERE p.run_id = r.id)
		  AND NOT EXISTS (SELECT 1 FROM events e WHERE e.run_id = r.id AND e.kind = 'tool_result' AND e.ok = 0)
		ORDER BY r.started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var proxy string
		var steering int
		if err := rows.Scan(&c.RunID, &c.AgentID, &c.AgentName, &c.TaskKey, &c.Model, &c.CostUSD, &proxy, &steering); err != nil {
			return nil, err
		}
		spec := proxySpec(proxy)
		if spec == 0 || steering > 2 {
			continue // not the pattern we want to spread
		}
		c.Why = candidateWhy(spec, steering)
		out = append(out, c)
	}
	return out, rows.Err()
}

func proxySpec(proxyJSON string) int {
	var p struct{ W, Spec int }
	if len(proxyJSON) == 0 {
		return 0
	}
	if err := json.Unmarshal([]byte(proxyJSON), &p); err != nil {
		return 0
	}
	return p.Spec
}

func candidateWhy(spec, steering int) string {
	why := "Run sạch không lỗi tool"
	if spec&capture.SpecHasPath != 0 {
		why += " · prompt nêu rõ file cần sửa"
	}
	if spec&capture.SpecHasTaskRef != 0 {
		why += " · gắn mã task"
	}
	if spec&capture.SpecHasCriteria != 0 {
		why += " · nêu tiêu chí hoàn thành ngay từ đầu"
	}
	if steering == 0 {
		why += " · agent tự chạy đến đích, không cần can thiệp"
	}
	return why + " — mẫu đáng nhân bản."
}

// PromoteCandidate turns a candidate into a playbook card + audit entry.
// The card documents the PATTERN (why), never a human comparison.
func PromoteCandidate(st *store.Store, c Candidate, actor string) (int64, error) {
	var exists int
	st.DB.QueryRow(`SELECT count(*) FROM playbooks WHERE run_id = ?`, c.RunID).Scan(&exists)
	if exists > 0 {
		return 0, fmt.Errorf("run %s already promoted", c.RunID)
	}
	name := "Pattern: " + c.AgentName
	if c.TaskKey != "" {
		name += " · " + c.TaskKey
	}
	res, err := st.DB.Exec(`INSERT INTO playbooks(name, run_id, agent_id, task_key, prompt, model, cost_usd, top_files, notes, created_at, created_by)
		VALUES(?, ?, ?, ?, '', ?, ?, '[]', ?, ?, ?)`,
		name, c.RunID, c.AgentID, c.TaskKey, c.Model, c.CostUSD, redact.String(c.Why), store.Now(), actor)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	_, _ = st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, ?, 'playbook_promoted', ?, 1, ?)`, c.RunID, store.Now(), actor, name)
	return id, nil
}

// RecordAdoption marks that an operator started a run from a playbook.
// metric_before freezes their done-rate at adoption time.
func RecordAdoption(st *store.Store, playbookID int64, operatorID, runID string, windowDays int) (int64, error) {
	var before sql.NullFloat64
	if operatorID != "" {
		if ob, err := OperatorBehaviorAgg(st, operatorID, windowDays); err == nil && ob.Runs > 0 {
			before = sql.NullFloat64{Float64: ob.DoneRate, Valid: true}
		}
	}
	res, err := st.DB.Exec(`INSERT INTO adoptions(playbook_id, operator_id, run_id, adopted_at, metric_before)
		VALUES(?, ?, NULLIF(?, ''), ?, ?)`, playbookID, nullStr(operatorID), runID, store.Now(), before)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ComputeAdoptionOutcomes fills metric_after for adoptions whose operator
// has completed enough runs since adopting. Outcomes stay PRIVATE (tech-lead
// coaching data) — they are never part of a published card.
func ComputeAdoptionOutcomes(st *store.Store) (int, error) {
	rows, err := st.DB.Query(`SELECT id, COALESCE(operator_id,''), adopted_at FROM adoptions WHERE computed_at IS NULL`)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id        int64
		op, since string
	}
	var ps []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.op, &p.since); err != nil {
			rows.Close()
			return 0, err
		}
		ps = append(ps, p)
	}
	rows.Close()
	done := 0
	for _, p := range ps {
		if p.op == "" {
			continue
		}
		var runs int
		var rate sql.NullFloat64
		err := st.Read().QueryRow(`SELECT count(*),
			avg(CASE WHEN status IN ('done','failed','killed') THEN CASE WHEN status='done' THEN 1.0 ELSE 0.0 END END)
			FROM runs WHERE operator_id = ? AND started_at > ?`, p.op, p.since).Scan(&runs, &rate)
		if err != nil || runs < adoptionMinRuns || !rate.Valid {
			continue
		}
		if _, err := st.DB.Exec(`UPDATE adoptions SET metric_after = ?, computed_at = ? WHERE id = ?`,
			rate.Float64, store.Now(), p.id); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

// AdoptionRow is one adopter's outcome for a playbook.
type AdoptionRow struct {
	OperatorID string
	AdoptedAt  string
	Before     *float64
	After      *float64
	Improved   *bool
	EnoughData bool
}

// AdoptionReport lists a playbook's adopters and their before/after.
func AdoptionReport(st *store.Store, playbookID int64) ([]AdoptionRow, error) {
	rows, err := st.Read().Query(`SELECT COALESCE(operator_id,''), adopted_at, metric_before, metric_after
		FROM adoptions WHERE playbook_id = ? ORDER BY adopted_at`, playbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdoptionRow
	for rows.Next() {
		var r AdoptionRow
		var before, after sql.NullFloat64
		if err := rows.Scan(&r.OperatorID, &r.AdoptedAt, &before, &after); err != nil {
			return nil, err
		}
		if before.Valid {
			r.Before = &before.Float64
		}
		if after.Valid {
			r.After = &after.Float64
			r.EnoughData = true
			if before.Valid {
				improved := after.Float64 > before.Float64
				r.Improved = &improved
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
