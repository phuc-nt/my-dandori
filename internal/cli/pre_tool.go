package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/store"
)

// preToolOutput is the PreToolUse hook decision JSON Claude Code understands.
type preToolOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// runPreTool records the tool_use event, then asks the guardrail engine for a
// verdict. Allow → silent exit 0. Deny/Ask → decision JSON on stdout.
//
// Verdict is intentionally decoupled from capture (pre-tool-ingest,
// contract.go): a capture write failure (DB locked, disk full) must not
// silently skip the guardrail check. When ing.PreTool fails there is no
// run row yet to look up agent/project scoping or record the decision
// against, so a full engine.Evaluate cannot run — the safe fallback is
// deny-mutating/allow-read-only, same narrow rule as the other dispatch
// holes (see denyMutatingOrAllow).
func runPreTool(cfg *config.Config, st *store.Store, ing *capture.Ingestor, in capture.HookInput) error {
	runID, _, err := ing.PreTool(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dandori hook: pre-tool capture failed, verdict falls back to deny-mutating:", err)
		return denyMutatingOrAllow(in.ToolName,
			"[dandori] không ghi được sự kiện tool (DB lỗi) — lệnh ghi/sửa bị chặn để an toàn (đọc vẫn chạy)")
	}
	var agentID, project string
	_ = st.DB.QueryRow(`SELECT COALESCE(agent_id,''), COALESCE(project,'') FROM runs WHERE id = ?`, runID).
		Scan(&agentID, &project)

	tc := govern.ExtractToolCall(runID, agentID, project, in.CWD, in.ToolName, in.ToolInput)
	engine := govern.NewEngine(cfg, st)
	return printVerdict(engine.Evaluate(contextOf(), tc))
}

// denyMutatingOrAllow is the narrow fail-closed fallback shared by every
// dispatch-layer hole in contract.go (store-open, pre-tool-ingest,
// hook-input-decode's mutating cousins): when the guardrail engine itself
// cannot run, a mutating tool is denied (nothing gets written/executed
// blind) but read-only tools still pass — the same shape as
// hook_central.go's no-snapshot fallback and the break-glass override in
// hook_cmd.go.
func denyMutatingOrAllow(toolName, reason string) error {
	if !govern.MutatingTool(toolName) {
		return nil
	}
	return printVerdict(govern.Decision{Verdict: govern.Deny, Reason: reason})
}

// printVerdict emits the hook decision: Allow → silent exit 0, Deny/Ask →
// decision JSON on stdout. Shared by the DB-backed and snapshot eval paths.
func printVerdict(d govern.Decision) error {
	if d.Verdict == govern.Allow {
		return nil
	}
	var out preToolOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = string(d.Verdict)
	out.HookSpecificOutput.PermissionDecisionReason = d.Reason
	b, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dandori hook:", err)
		return nil
	}
	fmt.Println(string(b))
	return nil
}
