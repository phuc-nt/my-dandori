package govern

import (
	"fmt"
	"regexp"

	"github.com/phuc-nt/dandori/internal/store"
)

// Policy simulator (UE4): replay a candidate rule against the captured
// tool-call history — "how many times would this have fired in the last 30
// days?" — before anyone dares enable it. Deterministic, full provenance.

type SimSample struct {
	EventID int64
	RunID   string
	Agent   string
	Tool    string
	Excerpt string
}

type SimResult struct {
	Hits    int
	Total   int
	Samples []SimSample // capped at 20
}

// Simulate replays tool_use events in the window through one candidate rule.
// The rule is NOT persisted — this is a dry read.
func Simulate(st *store.Store, pattern, scopeType, scopeID string, windowDays int) (*SimResult, error) {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}
	rule := Rule{Kind: "block", Pattern: pattern, ScopeType: scopeType, ScopeID: scopeID, re: re}

	rows, err := st.DB.Query(fmt.Sprintf(`SELECT e.id, COALESCE(e.tool_name,''), COALESCE(e.payload,''),
			r.id, COALESCE(r.agent_id,''), COALESCE(r.project,''), COALESCE(r.cwd,'')
		FROM events e JOIN runs r ON r.id = e.run_id
		WHERE e.kind = 'tool_use' AND e.ts >= datetime('now', '-%d days')`, windowDays))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := &SimResult{}
	for rows.Next() {
		var eventID int64
		var tool, payload, runID, agent, project, cwd string
		if err := rows.Scan(&eventID, &tool, &payload, &runID, &agent, &project, &cwd); err != nil {
			return nil, err
		}
		res.Total++
		tc := ExtractToolCall(runID, agent, project, cwd, tool, []byte(payload))
		if !rule.appliesTo(tc) || !rule.matches(tc) {
			continue
		}
		res.Hits++
		if len(res.Samples) < 20 {
			excerpt := tc.Command
			if excerpt == "" && len(tc.Paths) > 0 {
				excerpt = tc.Paths[0]
			}
			if len(excerpt) > 120 {
				excerpt = excerpt[:120] + "…"
			}
			res.Samples = append(res.Samples, SimSample{
				EventID: eventID, RunID: runID, Agent: agent, Tool: tool, Excerpt: excerpt,
			})
		}
	}
	return res, rows.Err()
}

// CreateRule persists a new guardrail rule with validation + audit.
func CreateRule(st *store.Store, kind, pattern, description, scopeType, scopeID string, critical bool, actor string) (int64, error) {
	if kind != "block" && kind != "gate" {
		return 0, fmt.Errorf("kind must be block or gate")
	}
	if _, err := regexp.Compile("(?i)" + pattern); err != nil {
		return 0, fmt.Errorf("invalid pattern: %w", err)
	}
	switch scopeType {
	case "global":
		scopeID = ""
	case "agent", "project":
		if scopeID == "" {
			return 0, fmt.Errorf("scope %s needs a target", scopeType)
		}
	default:
		return 0, fmt.Errorf("scope must be global, agent or project")
	}
	crit := 0
	if critical {
		crit = 1
	}
	res, err := st.DB.Exec(`INSERT INTO guardrail_rules(kind, pattern, description, enabled, critical, scope_type, scope_id)
		VALUES(?, ?, ?, 1, ?, ?, ?)`, kind, pattern, description, crit, scopeType, scopeID)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	a := &Audit{St: st, Actor: actor}
	_, err = a.Append("create_rule", fmt.Sprintf("rule:%d", id),
		fmt.Sprintf("%s %s [%s %s] %s", kind, pattern, scopeType, scopeID, description))
	return id, err
}

// DeleteRule removes a rule with audit.
func DeleteRule(st *store.Store, id int64, actor string) error {
	if _, err := st.DB.Exec(`DELETE FROM guardrail_rules WHERE id = ?`, id); err != nil {
		return err
	}
	a := &Audit{St: st, Actor: actor}
	_, err := a.Append("delete_rule", fmt.Sprintf("rule:%d", id), "")
	return err
}
