package govern

import (
	"database/sql"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
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
	// BudgetMode mirrors config.Budget.Mode's resolved value at snapshot-build
	// time (""/"downgrade" or "hard") — Evaluate reads this instead of config,
	// same offline-only posture as RiskMode/RiskThreshold above.
	BudgetMode string `json:"budget_mode"`
	// BudgetExceededAgents/BudgetExceededProjects extend BudgetExceeded (global
	// only) to the agent/project scopes actually in play for THIS operator's
	// active runs — see populateBudgetExceeded. A tool call's own AgentID or
	// Project being in one of these sets means that scope, not just global, is
	// over its monthly limit. Kept as slices (not a bool-per-run map) because
	// budget scopes are agent/project identity, the same identity Evaluate
	// already has on hand via tc.AgentID/tc.Project — no run-id indirection
	// needed, unlike RiskScores which is inherently per-run.
	BudgetExceededAgents   []string          `json:"budget_exceeded_agents"`
	BudgetExceededProjects []string          `json:"budget_exceeded_projects"`
	Rules                  []SnapshotRule    `json:"rules"`
	Bands                  map[string]string `json:"bands"`
	// RiskScores is G5's server-computed score per run-id, precomputed here
	// (not queried live by Evaluate — see the Evaluate doc comment on why
	// that invariant matters). Scoped to THIS snapshot's operator: only runs
	// owned by the operator the snapshot was built for are present (see
	// BuildPolicySnapshot's operatorID param) — a snapshot never leaks
	// another operator's run ids/scores. Bounded to active runs started in
	// the last 24h (mirrors KilledRuns) so an abandoned/zombie run does not
	// grow this map forever.
	RiskScores map[string]int `json:"risk_scores"`
	// RiskThreshold/RiskMode/RiskGuardedTools mirror config.RiskScore's
	// resolved (default-applied) values at snapshot-build time, so Evaluate
	// can apply G5 purely from snapshot fields — no config access needed
	// offline either.
	RiskThreshold    int      `json:"risk_threshold"`
	RiskMode         string   `json:"risk_mode"`
	RiskGuardedTools []string `json:"risk_guarded_tools"`
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

// BuildPolicySnapshot assembles the snapshot from the store (read pool),
// scoped to operatorID: the RiskScores map only ever contains runs owned by
// this operator (leak fix — a snapshot pulled by operator B's token must
// never contain operator A's run ids/scores). An empty operatorID means
// local/no-central mode, where there is only one operator by construction —
// all runs are included (backward compatible, not a multi-tenant scope).
// riskCfg supplies G5's resolved config (threshold/mode/guarded tools/points)
// so the returned snapshot carries everything Evaluate needs without a config
// lookup of its own. budgetCfg supplies G3's resolved config (global limit +
// Mode) for the same reason — see populateBudgetExceeded.
func BuildPolicySnapshot(st *store.Store, budgetCfg config.Budget, operatorID string, riskCfg config.RiskScore) (*PolicySnapshot, error) {
	p := &PolicySnapshot{
		FetchedAt:        store.Now(),
		Bands:            map[string]string{},
		RiskScores:       map[string]int{},
		BudgetMode:       budgetCfg.Mode,
		RiskThreshold:    riskCfg.ThresholdValue(),
		RiskMode:         riskCfg.Mode,
		RiskGuardedTools: riskCfg.GuardedToolsValue(),
	}
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

	if err := populateBudgetExceeded(st, p, operatorID, budgetCfg); err != nil {
		return nil, err
	}

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

	if err := populateRiskScores(st, p, operatorID, riskCfg); err != nil {
		return nil, err
	}
	return p, nil
}

// populateRiskScores fills p.RiskScores with the G5 score of every active,
// non-zombie run in scope, on the read pool (contention fix — this must never
// contend with the single ingest writer). "Active" mirrors KilledRuns' own
// 24h bound: status='running' AND started_at > now-24h, so an abandoned run
// that never finalized (no reaper exists yet) does not grow this map forever.
// When operatorID is non-empty, only runs owned by that operator are scored
// (leak fix); empty operatorID (local mode) scores every active run.
func populateRiskScores(st *store.Store, p *PolicySnapshot, operatorID string, riskCfg config.RiskScore) error {
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	var rows *sql.Rows
	var err error
	if operatorID != "" {
		rows, err = st.Read().Query(`SELECT id FROM runs
			WHERE status = 'running' AND started_at > ? AND operator_id = ?`, cutoff, operatorID)
	} else {
		rows, err = st.Read().Query(`SELECT id FROM runs
			WHERE status = 'running' AND started_at > ?`, cutoff)
	}
	if err != nil {
		return err
	}
	var runIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		runIDs = append(runIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, id := range runIDs {
		// countDenials=false: these are central runs, and their audit_log
		// denial rows are client-attested (see RiskScoreCentral's doc
		// comment) — zero-weighted here for the same anti-poison reason.
		score, err := riskScoreOn(st.Read(), riskCfg.WindowNValue(), riskCfg.ToolPointsValue(),
			riskCfg.DenialPointsValue(), id, false)
		if err != nil {
			// One run's scoring error must not fail the whole snapshot build
			// (fail-open on OBSERVATION, same posture as checkRisk's log-mode
			// query-error path) — that run is simply absent from RiskScores,
			// which Evaluate treats as score 0 (never gates).
			continue
		}
		p.RiskScores[id] = score
	}
	return nil
}

// MutatingTool reports whether a tool can change state — the set the narrow
// fail-closed path denies when NO policy is available at all.
func MutatingTool(name string) bool {
	return isEditTool(name) || name == "Bash"
}
