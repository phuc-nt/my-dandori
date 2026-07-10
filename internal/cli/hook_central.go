package cli

import (
	"os"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/ingest"
	"github.com/phuc-nt/dandori/internal/store"
)

// runHookCentral handles hook events when this machine is connected to a
// central server. No local database is opened. Contracts preserved:
//
//	capture  → fail-open: every error ends in the spool or stderr, exit 0
//	pre-tool → local eval on the cached policy snapshot; with NO snapshot
//	           available at all, mutating tools are denied (fail-closed
//	           narrow), read-only tools pass
func runHookCentral(cfg *config.Config, event string, in capture.HookInput) error {
	c := ingest.NewClient(cfg)
	agentName, project := capture.ResolveAttribution(in.CWD)
	rec := ingest.Record{
		SessionID: in.SessionID,
		AgentName: agentName,
		Project:   project,
		CWD:       in.CWD,
	}

	switch event {
	case "session-start":
		ingest.SaveSessionGit(in.SessionID, in.CWD)
		injectContextCentral(c, in, capture.AgentID(agentName)) // best-effort, fail-open
		return nil

	case "pre-tool":
		rec.Type = "event"
		rec.ULID = ingest.NewULID()
		rec.Kind = "tool_use"
		rec.Tool = in.ToolName
		rec.Payload = capture.Truncate(in.ToolInput)
		rec.ClientTS = store.Now()
		if err := c.AppendEvent(rec); err != nil {
			logAndAllow(err) // capture leg is fail-open; verdict still runs
		}
		return centralPreToolVerdict(c, in, capture.AgentID(agentName), project)

	case "post-tool":
		rec.Type = "event"
		rec.ULID = ingest.NewULID()
		rec.Kind = "tool_result"
		rec.Tool = in.ToolName
		okv := int64(1)
		if capture.IsErrorResponse(in.ToolResponse) {
			okv = 0
		}
		rec.OK = &okv
		rec.Payload = capture.Truncate(in.ToolResponse)
		rec.ClientTS = store.Now()
		if err := c.AppendEvent(rec); err != nil {
			return logAndAllow(err)
		}
		c.FlushThrottled()
		return nil

	case "stop":
		rec.Type = "finalize"
		rec.Finalize = ingest.DeriveFinalize(cfg, in.SessionID, in.CWD, in.TranscriptPath)
		if err := c.AppendEvent(rec); err != nil {
			return logAndAllow(err)
		}
		if err := c.Flush(); err != nil {
			return logAndAllow(err) // spooled — relays on next success
		}
		return nil
	}
	return nil
}

// centralPreToolVerdict evaluates guardrails against the policy snapshot.
// A Deny/Ask verdict additionally spools a guardrail-decision record so the
// central server can create a co-signed audit_log row for it (Phase 4) —
// today only local-mode Engine.Evaluate does that (engine.go's record()).
func centralPreToolVerdict(c *ingest.Client, in capture.HookInput, agentID, project string) error {
	tc := govern.ExtractToolCall(in.SessionID, agentID, project, in.CWD, in.ToolName, in.ToolInput)
	snap := c.Policy()
	if snap == nil {
		// central-no-snapshot, FailClosedMutating (contract.go): deny only
		// what can change state — capture keeps spooling, sessions keep reading.
		if govern.MutatingTool(tc.ToolName) {
			return printVerdict(govern.Decision{Verdict: govern.Deny,
				Reason: "[dandori] không lấy được chính sách từ server trung tâm — lệnh ghi/sửa bị chặn để an toàn (đọc vẫn chạy)"})
		}
		return nil
	}
	d, action := snap.Evaluate(tc)
	switch action {
	case "":
		// Allow, nothing to record.
	case govern.ActionRiskWouldGate:
		// G5 log-mode observation only — NOT a central audit record (it is
		// deliberately absent from govern.AuditActionSet: log mode never
		// blocked anything, so there is no guardrail decision to co-sign).
		// This mirrors local mode's emitRiskWouldGate, just written from the
		// dev machine's own hook process instead of the engine.
		emitRiskWouldGateCentral(c, in.SessionID, tc.ToolName, d.Reason)
	default:
		spoolGuardrailDecision(c, in.SessionID, tc.ToolName, action, d.Reason, snap.FetchedAt)
	}
	return printVerdict(d)
}

// emitRiskWouldGateCentral records the client-side twin of local mode's
// emitRiskWouldGate: a risk_would_gate capture event, spooled through the
// same best-effort capture path as every other central-mode event (fail-open
// — a spool failure here must never affect the tool call's own verdict,
// which centralPreToolVerdict already returned).
func emitRiskWouldGateCentral(c *ingest.Client, sessionID, tool, reason string) {
	rec := ingest.Record{
		Type:      "event",
		SessionID: sessionID,
		ULID:      ingest.NewULID(),
		Kind:      "risk_would_gate",
		Tool:      tool,
		Payload:   reason,
		ClientTS:  store.Now(),
	}
	if err := c.AppendEvent(rec); err != nil {
		logAndAllow(err)
	}
}

// spoolGuardrailDecision records a Deny/Ask verdict for central audit. This
// is capture-adjacent (best-effort, fail-open, contract.go's
// capture-event-append mode) — a spool failure here must never turn into a
// second verdict or block the hook; the printed verdict from Evaluate
// already stands on its own.
func spoolGuardrailDecision(c *ingest.Client, sessionID, tool, action, reason, fetchedAt string) {
	host, _ := os.Hostname()
	rec := ingest.Record{
		Type:              "event",
		SessionID:         sessionID,
		ULID:              ingest.NewULID(),
		Kind:              action,
		Tool:              tool,
		Payload:           reason,
		ClientTS:          store.Now(),
		Action:            action,
		Machine:           host,
		SnapshotFetchedAt: fetchedAt,
	}
	if err := c.AppendEvent(rec); err != nil {
		logAndAllow(err)
	}
}
