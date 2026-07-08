package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/ingest"
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
		// hook-input-decode (contract.go): stdin/JSON/session_id errors happen
		// before the event type or tool name is known, so there is nothing to
		// deny-mutating against yet — this is an accepted narrow gap, not a
		// fail-open hole (we cannot deny what we cannot parse).
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
		// config-load (contract.go, FailClosedMutating): a config load error
		// silently dropped central policy in favor of local-DB mode. The
		// fallback itself is the safe default (local DB still enforces
		// guardrails), but the drop must be visible, not silent.
		cfg, cfgErr := loadConfig()
		if cfgErr == nil && ingest.Enabled(cfg) {
			// Central mode: no local DB — derive locally, POST/spool records.
			return runHookCentral(cfg, args[0], in)
		}
		if cfgErr != nil {
			fmt.Fprintln(os.Stderr, "dandori: config load failed, central policy unavailable, falling back to local:", cfgErr)
		}

		// Break-glass: read BEFORE openStore so a broken config/DB can never
		// suppress the override — env only, never the config file (the file
		// itself may be what's broken).
		breakGlass := os.Getenv("DANDORI_GOVERN_FAIL_OPEN") == "1"
		if breakGlass {
			fmt.Fprintln(os.Stderr, "dandori: GOVERN FAIL-OPEN mode active (DANDORI_GOVERN_FAIL_OPEN=1)")
		}

		// store-open (contract.go, FailClosedMutating): openStore failing
		// (disk full, corrupt WAL, lock past busy_timeout) used to allow every
		// tool unconditionally. Only the pre-tool event carries a verdict to
		// give — session-start/post-tool/stop are pure capture and stay
		// fail-open (exit 0) same as before. pre-tool now denies mutating
		// tools only, unless break-glass is set — read-only calls still pass.
		cfg, st, err := openStore()
		if err != nil {
			if breakGlass || args[0] != "pre-tool" {
				return logAndAllow(err)
			}
			fmt.Fprintln(os.Stderr, "dandori hook:", err)
			return denyMutatingOrAllow(in.ToolName,
				"[dandori] engine không khả dụng (DB lỗi) — lệnh ghi/sửa bị chặn để an toàn (đọc vẫn chạy)")
		}
		defer st.Close()
		ing := &capture.Ingestor{Cfg: cfg, St: st}

		switch args[0] {
		case "session-start":
			if err = ing.SessionStart(in); err != nil {
				return logAndAllow(err)
			}
			injectContextLocal(cfg, st, ing, in) // best-effort, fail-open
			return nil
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
