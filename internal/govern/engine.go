// Package govern is the guardrail engine: every tool call flows through an
// ordered chain of checks. Unlike capture (fail-open), guardrails are a
// contract — engine errors on block rules fail CLOSED (deny).
package govern

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

type Verdict string

const (
	Allow Verdict = "allow"
	Deny  Verdict = "deny"
	Ask   Verdict = "ask"
)

type Decision struct {
	Verdict Verdict
	Reason  string
}

// ToolCall is the guardrail-relevant slice of a PreToolUse hook input.
type ToolCall struct {
	RunID    string
	AgentID  string
	Project  string
	CWD      string
	ToolName string
	Command  string   // Bash command, if any
	Paths    []string // file paths touched (Write/Edit + extracted from Bash)
	// Content is the payload a mutating tool is about to write: Write's
	// `content`, Edit's `new_string`, NotebookEdit's `new_source`. Populated
	// by ExtractToolCall — checkSecrets scans this alongside Command since a
	// secret/PII value can arrive in file content, not just a shell command.
	Content string
}

type Engine struct {
	Cfg   *config.Config
	St    *store.Store
	Audit *Audit
}

func NewEngine(cfg *config.Config, st *store.Store) *Engine {
	return &Engine{Cfg: cfg, St: st, Audit: &Audit{St: st, Actor: "dandori-engine"}}
}

// Evaluate runs the check chain in fixed order: kill switch → sandbox →
// block rules → secrets → budget → risk score (G5) → permission gate. First
// hit wins.
func (e *Engine) Evaluate(ctx context.Context, tc ToolCall) Decision {
	if d, hit := e.checkKill(tc); hit {
		return e.record(tc, "kill_block", d)
	}
	if d, hit := e.checkSandbox(tc); hit {
		return e.record(tc, "sandbox_block", d)
	}
	rules, err := e.loadRules()
	if err != nil {
		// rules-load, FailClosed (contract.go): cannot evaluate contract rules → deny
		return e.record(tc, "engine_error", Decision{Deny, "[dandori] internal error evaluating guardrails: " + err.Error()})
	}
	if d, hit := e.checkBlockRules(tc, rules); hit {
		return e.record(tc, "guardrail_block", d)
	}
	if d, hit := e.checkSecrets(ctx, tc); hit {
		return e.record(tc, "secrets_block", d)
	}
	if d, hit := e.checkBudget(tc); hit {
		return e.record(tc, "budget_block", d)
	}
	if d, hit := e.checkRisk(ctx, tc); hit {
		return e.record(tc, "risk_gate", d)
	}
	if d, hit := e.checkGate(ctx, tc, rules); hit {
		return e.record(tc, "gate_decision", d)
	}
	return Decision{Verdict: Allow}
}

// record persists the decision as an event + audit entry, then returns it.
// Allow decisions from the gate (approved) are also recorded for provenance.
func (e *Engine) record(tc ToolCall, action string, d Decision) Decision {
	detail := fmt.Sprintf("tool=%s verdict=%s reason=%s", tc.ToolName, d.Verdict, d.Reason)
	kind := "guardrail_block"
	if action == "gate_decision" {
		kind = "permission_ask"
	}
	var ok int64
	if d.Verdict == Allow {
		ok = 1
	}
	_, _ = e.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, ?, ?, ?, ?, ?)`, tc.RunID, store.Now(), kind, tc.ToolName, ok, detail)
	if id, err := e.Audit.Append(action, tc.RunID, detail); err == nil {
		if d.Verdict != Allow {
			d.Reason = fmt.Sprintf("%s (audit #%d)", d.Reason, id)
		}
	}
	return d
}

// checkKill denies everything when the global kill switch is on or this run
// was individually killed. A safety control must fail closed: if the kill
// state cannot be read, the call is denied rather than silently allowed.
func (e *Engine) checkKill(tc ToolCall) (Decision, bool) {
	if e.St.Setting("kill_switch_global") == "1" {
		return Decision{Deny, "[dandori kill] global kill switch is ON — all agent tool calls are blocked"}, true
	}
	var status string
	err := e.St.DB.QueryRow(`SELECT status FROM runs WHERE id = ?`, tc.RunID).Scan(&status)
	if err != nil && err != sql.ErrNoRows {
		return Decision{Deny, "[dandori] internal error checking kill state — denying for safety"}, true
	}
	if status == "killed" {
		return Decision{Deny, "[dandori kill] this run was killed by an operator"}, true
	}
	return Decision{}, false
}
