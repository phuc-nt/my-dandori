package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/capture"
)

// hookCmd is invoked by Claude Code hooks with the event payload on stdin.
// Capture is fail-open: internal errors are logged to stderr and the command
// exits 0 so the user's Claude session is never broken by Dandori itself.
// The guardrail engine (pre-tool) is wired in by the govern package.
var hookCmd = &cobra.Command{
	Use:       "hook [session-start|pre-tool|post-tool|stop]",
	Short:     "Claude Code hook entrypoint (reads event JSON on stdin)",
	Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	ValidArgs: []string{"session-start", "pre-tool", "post-tool", "stop"},
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return logAndAllow(err)
		}
		var in capture.HookInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return logAndAllow(fmt.Errorf("decode hook input: %w (raw: %.200s)", err, raw))
		}
		if in.SessionID == "" {
			return logAndAllow(fmt.Errorf("hook input missing session_id"))
		}
		cfg, st, err := openStore()
		if err != nil {
			return logAndAllow(err)
		}
		defer st.Close()
		ing := &capture.Ingestor{Cfg: cfg, St: st}

		switch args[0] {
		case "session-start":
			err = ing.SessionStart(in)
		case "pre-tool":
			return runPreTool(cfg, st, ing, in)
		case "post-tool":
			err = ing.PostTool(in)
		case "stop":
			err = ing.Stop(in)
		}
		if err != nil {
			return logAndAllow(err)
		}
		return nil
	},
}

// logAndAllow reports an internal capture error without failing the hook.
func logAndAllow(err error) error {
	fmt.Fprintln(os.Stderr, "dandori hook:", err)
	return nil // exit 0 — never break the session for capture errors
}

func init() {
	rootCmd.AddCommand(hookCmd)
}
