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
//
// G5 (risk score, internal/govern/risk.go) is intentionally ABSENT here and
// must stay that way: it needs local `events` + `audit_log` history and the
// findOrCreateApproval/waitDecision machinery, none of which exist on the
// offline snapshot path. Do not add a DB query here to "helpfully" support
// it — see TestSnapshotEvaluateHasNoRiskBranch.
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
	// G1.5 secret-Deny (regex-only half of checkSecrets): a strict-secret
	// pattern in Command/Content is denied here too, since it needs no DB
	// lookup and central-mode pre-tool checks must not be weaker than local.
	// The PII→gate half of G1.5 is LOCAL-ONLY: it needs findOrCreateApproval
	// (a DB row + wait-for-human), which the snapshot has no access to on a
	// dev machine evaluating offline. A PII-bearing call in central mode
	// falls through to whatever gate/band rule would otherwise apply.
	if hit, kind := snapshotSecretMatch(tc); hit {
		return Decision{Deny, fmt.Sprintf("[dandori G1.5] %s detected — value withheld from logs/audit", kind)}
	}
	// Central mode is hard-stop ONLY — it never applies the local downgrade-gate
	// (Budget.Mode, ExpensiveModels, per-run model, agent/project scoping).
	// BudgetExceeded here is a single global bool computed server-side with no
	// per-run model info and no DB to run runModel/nullAllowGate against, so
	// there is nothing to downgrade against. A dev machine pulling this
	// snapshot always hard-denies mutating tools once the org-wide budget is
	// exhausted; the downgrade-gate only exists in Engine.checkBudget's local
	// evaluation path (internal/govern/budget.go). This is intentional, not a
	// TODO: closing it would require pushing per-run model + per-scope budgets
	// into the snapshot payload, which is out of scope here.
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

// snapshotSecretMatch runs the same strict-secret/Bearer check checkSecrets
// uses locally, scoped to Command+Content and capped the same way (scanCap,
// head+tail) — kept as a plain function (no DB) so it is safe to call from
// the snapshot's offline Evaluate.
func snapshotSecretMatch(tc ToolCall) (bool, string) {
	for _, window := range scanWindows(tc.Command, tc.Content) {
		if kind, ok := secretKind(window); ok {
			return true, kind
		}
	}
	return false, ""
}
