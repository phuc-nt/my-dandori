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
func runPreTool(cfg *config.Config, st *store.Store, ing *capture.Ingestor, in capture.HookInput) error {
	runID, _, err := ing.PreTool(in)
	if err != nil {
		return logAndAllow(err) // capture is fail-open
	}
	var agentID, project string
	_ = st.DB.QueryRow(`SELECT COALESCE(agent_id,''), COALESCE(project,'') FROM runs WHERE id = ?`, runID).
		Scan(&agentID, &project)

	tc := govern.ExtractToolCall(runID, agentID, project, in.CWD, in.ToolName, in.ToolInput)
	engine := govern.NewEngine(cfg, st)
	d := engine.Evaluate(contextOf(), tc)
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
