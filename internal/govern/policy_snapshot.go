package govern

import (
	"fmt"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// PolicySnapshot is the governance state a dev machine pulls from the central
// server so pre-tool checks evaluate LOCALLY — no per-tool-call network round
// trip, no single-writer contention on the server (red-team C3). Verdicts
// that need a human (gate rules, supervised band) come back as Ask: the human
// at the dev machine decides in Claude Code's own permission prompt.
type PolicySnapshot struct {
	FetchedAt      string            `json:"fetched_at"`
	KillGlobal     bool              `json:"kill_global"`
	KilledRuns     []string          `json:"killed_runs"`
	BudgetExceeded bool              `json:"budget_exceeded"`
	Rules          []SnapshotRule    `json:"rules"`
	Bands          map[string]string `json:"bands"`
}

type SnapshotRule struct {
	ID          int64  `json:"id"`
	Kind        string `json:"kind"`
	Pattern     string `json:"pattern"`
	Description string `json:"description"`
	Critical    bool   `json:"critical"`
	ScopeType   string `json:"scope_type"`
	ScopeID     string `json:"scope_id"`
}

// BuildPolicySnapshot assembles the snapshot from the store (read pool).
func BuildPolicySnapshot(st *store.Store, monthlyBudget float64) (*PolicySnapshot, error) {
	p := &PolicySnapshot{FetchedAt: store.Now(), Bands: map[string]string{}}
	var kill string
	_ = st.Read().QueryRow(`SELECT value FROM settings WHERE key = 'kill_switch_global'`).Scan(&kill)
	p.KillGlobal = kill == "1"

	rows, err := st.Read().Query(`SELECT id FROM runs WHERE status = 'killed' AND started_at > ?`,
		time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		p.KilledRuns = append(p.KilledRuns, id)
	}
	rows.Close()

	var spent float64
	_ = st.Read().QueryRow(`SELECT COALESCE(SUM(cost_usd),0) FROM runs
		WHERE started_at >= strftime('%Y-%m-01T00:00:00Z','now')`).Scan(&spent)
	p.BudgetExceeded = monthlyBudget > 0 && spent >= monthlyBudget

	rrows, err := st.Read().Query(`SELECT id, kind, pattern, COALESCE(description,''), critical, scope_type, scope_id
		FROM guardrail_rules WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rrows.Close()
	for rrows.Next() {
		var r SnapshotRule
		var crit int
		if err := rrows.Scan(&r.ID, &r.Kind, &r.Pattern, &r.Description, &crit, &r.ScopeType, &r.ScopeID); err != nil {
			return nil, err
		}
		r.Critical = crit == 1
		p.Rules = append(p.Rules, r)
	}
	brows, err := st.Read().Query(`SELECT agent_id, band FROM agent_bands`)
	if err != nil {
		return nil, err
	}
	defer brows.Close()
	for brows.Next() {
		var a, b string
		if err := brows.Scan(&a, &b); err != nil {
			return nil, err
		}
		p.Bands[a] = b
	}
	return p, nil
}

// Evaluate mirrors Engine.Evaluate's fixed order against the snapshot:
// kill → block → budget → gate/band. Same contract, evaluated offline.
// Bad rule patterns fail CLOSED like the engine does.
func (p *PolicySnapshot) Evaluate(tc ToolCall) Decision {
	if p.KillGlobal {
		return Decision{Deny, "[dandori kill] global kill switch is ON — all agent tool calls are blocked"}
	}
	for _, id := range p.KilledRuns {
		if id == tc.RunID {
			return Decision{Deny, "[dandori kill] this run was killed by an operator"}
		}
	}
	for i := range p.Rules {
		r := &p.Rules[i]
		if r.Kind != "block" {
			continue
		}
		hit, err := p.ruleMatches(r, tc)
		if err != nil {
			return Decision{Deny, "[dandori] internal error evaluating guardrails: " + err.Error()}
		}
		if hit {
			return Decision{Deny, fmt.Sprintf("[dandori G1] blocked: %s (rule #%d)", r.Description, r.ID)}
		}
	}
	if p.BudgetExceeded && (isEditTool(tc.ToolName) || tc.ToolName == "Bash") {
		return Decision{Deny, "[dandori G3] monthly budget exhausted — mutating tool calls are blocked until the budget is raised"}
	}
	band := p.Bands[tc.AgentID]
	if band == "" {
		band = BandGated
	}
	for i := range p.Rules {
		r := &p.Rules[i]
		if r.Kind != "gate" {
			continue
		}
		if band == BandTrusted && !r.Critical {
			continue
		}
		hit, err := p.ruleMatches(r, tc)
		if err != nil {
			return Decision{Deny, "[dandori] internal error evaluating guardrails: " + err.Error()}
		}
		if hit {
			return Decision{Ask, fmt.Sprintf("[dandori G4] %s (rule #%d) — cần người duyệt tại máy này", r.Description, r.ID)}
		}
	}
	if band == BandSupervised && (isEditTool(tc.ToolName) || tc.ToolName == "Bash") {
		return Decision{Ask, "[dandori G4] supervised band: edits and shell commands require approval"}
	}
	return Decision{Verdict: Allow}
}

func (p *PolicySnapshot) ruleMatches(r *SnapshotRule, tc ToolCall) (bool, error) {
	if r.ScopeType == "agent" && r.ScopeID != tc.AgentID {
		return false, nil
	}
	if r.ScopeType == "project" && r.ScopeID != tc.Project {
		return false, nil
	}
	re, err := compileCached(r.Pattern)
	if err != nil {
		return false, fmt.Errorf("rule #%d bad pattern: %w", r.ID, err)
	}
	if tc.Command != "" && re.MatchString(tc.Command) {
		return true, nil
	}
	for _, pth := range tc.Paths {
		if re.MatchString(pth) {
			return true, nil
		}
	}
	return false, nil
}

// MutatingTool reports whether a tool can change state — the set the narrow
// fail-closed path denies when NO policy is available at all.
func MutatingTool(name string) bool {
	return isEditTool(name) || name == "Bash"
}
