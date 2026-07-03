package cli

import (
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
func centralPreToolVerdict(c *ingest.Client, in capture.HookInput, agentID, project string) error {
	tc := govern.ExtractToolCall(in.SessionID, agentID, project, in.CWD, in.ToolName, in.ToolInput)
	snap := c.Policy()
	if snap == nil {
		// Never had a policy and the server is unreachable: deny only what
		// can change state — capture keeps spooling, sessions keep reading.
		if govern.MutatingTool(tc.ToolName) {
			return printVerdict(govern.Decision{Verdict: govern.Deny,
				Reason: "[dandori] không lấy được chính sách từ server trung tâm — lệnh ghi/sửa bị chặn để an toàn (đọc vẫn chạy)"})
		}
		return nil
	}
	return printVerdict(snap.Evaluate(tc))
}
