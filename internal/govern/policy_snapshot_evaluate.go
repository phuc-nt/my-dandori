package govern

import "fmt"

// Central guardrail-decision action discriminators. This is the closed set
// a Deny/Ask verdict from Evaluate can carry — it mirrors Engine.record's
// action naming (engine.go) so central and local audit rows use the same
// vocabulary. AuditActionSet (below) is the server-side whitelist: a client
// cannot get an arbitrary string into the audit action column just by
// spooling one, because the client never derived this string itself — see
// the discriminator return below, not client-side regex-parsing of Reason.
const (
	ActionKillBlock      = "kill_block"
	ActionGuardrailBlock = "guardrail_block"
	ActionSecretsBlock   = "secrets_block"
	ActionBudgetBlock    = "budget_block"
	ActionPermissionAsk  = "permission_ask"
	// ActionRiskGate is G5's central-mode escalation (gate mode, over
	// threshold) — same vocabulary slot as local Engine.record's "risk_gate",
	// but the verdict here is always immediate Ask (no findOrCreateApproval/
	// waitDecision machinery exists offline; see Evaluate's risk branch).
	ActionRiskGate = "risk_gate"
	// ActionRiskWouldGate is NOT a server audit action (it is never in
	// AuditActionSet — log mode never spools a guardrail-decision record for
	// central audit, exactly like local log mode never denies). It is the
	// signal Evaluate hands back to the CLIENT so hook_central can emit its
	// own local risk_would_gate observation event, mirroring emitRiskWouldGate.
	ActionRiskWouldGate = "risk_would_gate"
)

// AuditActionSet is the whitelist a server applying a client-spooled
// guardrail-decision record must check the action against before writing it
// into audit_log — an unrecognized action is normalized rather than trusted
// verbatim (see ingest/apply.go's run-owner + dedup path).
var AuditActionSet = map[string]bool{
	ActionKillBlock:      true,
	ActionGuardrailBlock: true,
	ActionSecretsBlock:   true,
	ActionBudgetBlock:    true,
	ActionPermissionAsk:  true,
}

// Evaluate mirrors Engine.Evaluate's fixed order against the snapshot:
// kill → block → secrets → budget → risk (G5) → gate/band. Same contract,
// evaluated offline. Bad rule patterns fail CLOSED like the engine does. The
// second return value is the action discriminator for Deny/Ask verdicts
// (empty for Allow) — a dev machine spooling a guardrail-decision record for
// central audit uses THIS value, not a regex parse of Reason, so the action
// that ends up in the server's audit_log is the same one the evaluator
// itself picked, not something the client invented.
//
// Evaluate performs NO DATABASE ACCESS — this is the invariant
// TestSnapshotEvaluateHasNoRiskBranch asserts, and it must keep holding as
// more checks are added. G5's score is precomputed server-side into
// RiskScores/RiskThreshold/RiskMode/RiskGuardedTools by BuildPolicySnapshot
// (see populateRiskScores) — Evaluate only ever reads those already-resolved
// snapshot fields, the same way it reads Rules/Bands/KilledRuns for every
// other check. Do not add a DB query here to "helpfully" recompute a score.
func (p *PolicySnapshot) Evaluate(tc ToolCall) (Decision, string) {
	if p.KillGlobal {
		return Decision{Deny, "[dandori kill] global kill switch is ON — all agent tool calls are blocked"}, ActionKillBlock
	}
	for _, id := range p.KilledRuns {
		if id == tc.RunID {
			return Decision{Deny, "[dandori kill] this run was killed by an operator"}, ActionKillBlock
		}
	}
	for i := range p.Rules {
		r := &p.Rules[i]
		if r.Kind != "block" {
			continue
		}
		hit, err := p.ruleMatches(r, tc)
		if err != nil {
			return Decision{Deny, "[dandori] internal error evaluating guardrails: " + err.Error()}, ActionGuardrailBlock
		}
		if hit {
			return Decision{Deny, fmt.Sprintf("[dandori G1] blocked: %s (rule #%d)", r.Description, r.ID)}, ActionGuardrailBlock
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
		return Decision{Deny, fmt.Sprintf("[dandori G1.5] %s detected — value withheld from logs/audit", kind)}, ActionSecretsBlock
	}
	// Central mode CANNOT apply the local downgrade-gate (Engine.checkBudget,
	// budget.go): that gate decides by the run's OWN model (cheap vs
	// ExpensiveModels), and a central active run's runs.model is NULL almost
	// always — ingest only writes model at transcript reconciliation or
	// FinalizeRun, never mid-run the way local's maybeReconcile does every
	// 10s (internal/capture/ingest.go). There is no model here to downgrade
	// against, so central does not attempt model-based parity with local.
	//
	// Instead: budget.mode "hard" keeps the pre-v14 hard-stop (deny every
	// mutating call once ANY applicable scope — global/agent/project, see
	// populateBudgetExceeded — is over). budget.mode "" or "downgrade"
	// (default) escalates to Ask instead of denying or downgrading: the human
	// at the dev machine sees the same permission prompt G4/G5 use and decides
	// whether to keep going (possibly switching model themselves via /model)
	// or stop. This is a deliberate divergence from local, not a TODO — local
	// keeps its model-aware downgrade-gate unchanged.
	if isEditTool(tc.ToolName) || tc.ToolName == "Bash" {
		scopeType, scopeID, exceeded := p.budgetExceededScope(tc)
		if exceeded {
			if p.BudgetMode == "hard" {
				return Decision{Deny, fmt.Sprintf(
					"[dandori G3] monthly budget exhausted for %s %s — mutating tool calls are blocked until the budget is raised",
					scopeType, scopeID)}, ActionBudgetBlock
			}
			return Decision{Ask, fmt.Sprintf(
				"[dandori G3] budget vượt trần (%s %s) — cần người duyệt tại máy này (central không biết model của run này để tự downgrade)",
				scopeType, scopeID)}, ActionPermissionAsk
		}
	}
	// G5 risk score: guarded tool + precomputed score >= threshold. Unlike
	// local checkRisk (which calls findOrCreateApproval + waits for a human
	// decision), central gate mode escalates to an IMMEDIATE Ask — the same
	// posture G4's gate rules take here, since there is no interactive
	// approval-wait loop available offline. Log mode (default) never denies
	// or short-circuits (parity with local checkRisk's log-mode fallthrough
	// to checkGate) — it only remembers "would have gated" so an eventual
	// Allow at the end of this function carries ActionRiskWouldGate instead
	// of "", letting hook_central emit its own local observation event
	// (mirroring emitRiskWouldGate's local write). A Deny/Ask from a LATER
	// check (gate rule, supervised band) still wins with its own action —
	// only the final Allow gets overridden.
	riskGateDeny, riskWouldGate := p.evaluateRisk(tc)
	if riskGateDeny {
		return Decision{Ask, fmt.Sprintf("[dandori G5] risk score %d ≥ threshold %d — cần người duyệt tại máy này",
			p.RiskScores[tc.RunID], p.RiskThreshold)}, ActionRiskGate
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
			return Decision{Deny, "[dandori] internal error evaluating guardrails: " + err.Error()}, ActionGuardrailBlock
		}
		if hit {
			return Decision{Ask, fmt.Sprintf("[dandori G4] %s (rule #%d) — cần người duyệt tại máy này", r.Description, r.ID)}, ActionPermissionAsk
		}
	}
	if band == BandSupervised && (isEditTool(tc.ToolName) || tc.ToolName == "Bash") {
		return Decision{Ask, "[dandori G4] supervised band: edits and shell commands require approval"}, ActionPermissionAsk
	}
	if riskWouldGate {
		return Decision{Verdict: Allow}, ActionRiskWouldGate
	}
	return Decision{Verdict: Allow}, ""
}

// evaluateRisk applies G5 purely from snapshot fields (no DB access — see
// Evaluate's invariant comment). gateDeny=true means gate mode is over
// threshold on a guarded tool — Evaluate must Ask immediately. wouldGate=true
// means log mode is over threshold on a guarded tool — Evaluate keeps
// evaluating but must tag its eventual Allow with ActionRiskWouldGate.
func (p *PolicySnapshot) evaluateRisk(tc ToolCall) (gateDeny, wouldGate bool) {
	guarded := false
	for _, name := range p.RiskGuardedTools {
		if name == tc.ToolName {
			guarded = true
			break
		}
	}
	if !guarded {
		return false, false
	}
	if p.RiskScores[tc.RunID] < p.RiskThreshold {
		return false, false
	}
	if p.RiskMode != "gate" {
		return false, true
	}
	return true, false
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
