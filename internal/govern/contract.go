package govern

// FailureMode classifies what happens when a check cannot be evaluated (its
// own internal error, not the tool call it is judging).
type FailureMode int

const (
	// FailClosed: the check runs before the tool's side-effect and denies
	// everything (read and write alike) when it cannot be evaluated. Used by
	// checks that gate the call itself — there is no side-effect yet to
	// preserve by relaxing the deny.
	FailClosed FailureMode = iota
	// FailClosedMutating: the check runs before the side-effect too, but a
	// full deny would break read-only workflows for a failure that is really
	// about capture/config plumbing, not the guardrail decision. Only
	// mutating tools (Write/Edit/NotebookEdit/Bash, see MutatingTool) are
	// denied; reads pass.
	FailClosedMutating
	// FailOpen: the check is capture-only (it runs after the side-effect, or
	// only records the event) — an internal error here must not retroactively
	// block or crash a tool call already in flight.
	FailOpen
)

// CheckFailureModes is the single source of truth for how each named check
// point in the hook path behaves when IT (not the tool call) errors out. It
// covers both the govern.Engine chain and the internal/cli dispatch layer
// that wraps it, since a hole in either layer defeats the guardrail contract.
//
// Rule of thumb (borrowed from the "fail-closed phases" pattern used by
// other outer-harness agents): a check that cannot be evaluated fails CLOSED
// when it runs BEFORE the side-effect it is meant to gate (deny), and OPEN
// when it runs after the side-effect or is capture-only. Mutating-vs-read-only
// is relaxed to FailClosedMutating only where a full deny would break
// read-only workflows that never needed the guardrail to begin with.
//
// This map is documentation + a lookup other code references by comment; it
// does not itself drive control flow (each check still implements its own
// mode inline — see the comments at each site pointing back here).
var CheckFailureModes = map[string]FailureMode{
	// engine.Evaluate chain (internal/govern)
	"kill":       FailClosed,
	"sandbox":    FailClosed,
	"rules-load": FailClosed,
	"block":      FailClosed,
	// secrets covers both halves of G1.5: the strict-secret Deny is a pure
	// regex match (no runtime error possible); the PII-gate half reuses the
	// gate's approval-create/wait path and inherits its FailClosed — an
	// approval that cannot be created/tracked must deny, not silently allow
	// PII-bearing content through.
	"secrets": FailClosed,
	// budget covers checkBudget's whole downgrade-gate path, not just the
	// spend-vs-limit query: runModel, agentPriorModel and the null-allow
	// counter all fail closed the same way — any of them erroring means the
	// engine cannot prove the run's model is cheap enough (or its NULL-model
	// history is clean) to allow it through, so it denies rather than risk an
	// expensive-model bypass.
	"budget": FailClosed,
	// risk covers checkRisk's RiskScore query: in GATE mode a scoring error
	// means the engine cannot prove the run is under threshold, so it denies
	// (fail-closed) — same reasoning as budget. In LOG mode (the default)
	// there is nothing to gate yet, only an observation to skip, so a query
	// error there allows the call through and simply logs nothing; risk.go's
	// checkRisk implements both halves inline, this entry documents the
	// GATE-mode half only (LOG mode is never "closed", it never blocks).
	"risk": FailClosed,
	"gate": FailClosed,

	// dispatch layer (internal/cli) — the hook entrypoint wrapping the engine
	"store-open":          FailClosedMutating,
	"pre-tool-ingest":     FailClosedMutating,
	"central-no-snapshot": FailClosedMutating,
	// hook-input-decode fails OPEN, not closed: when stdin/JSON fails to parse
	// there is no ToolName to judge, so there is nothing to deny against — the
	// call exits 0. This is a deliberate, narrow residual gap, not a mutating
	// deny; the map records the truth rather than an aspiration.
	"hook-input-decode": FailOpen,
	// config-load errors do NOT allow-all and do NOT deny: the hook degrades to
	// local-DB mode (guardrails still evaluated there) and logs a warning that
	// central policy was dropped. It is neither closed nor open in the deny
	// sense — it is a safe fallthrough, classified FailOpen because it never
	// blocks the tool on its own.
	"config-load": FailOpen,

	// capture legs — record-only, run after (or independent of) the
	// tool's side-effect; an error here must never retroactively block it
	"capture-post-tool":    FailOpen,
	"capture-stop":         FailOpen,
	"capture-event-append": FailOpen,
}
