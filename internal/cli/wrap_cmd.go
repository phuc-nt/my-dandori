package cli

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/capture"
)

var (
	flagWrapAgent   string
	flagWrapRuntime string
	flagWrapTask    string
)

// wrapCmd is the layer-1 capture guarantee for runtimes without hooks
// (codex, aider, any CLI agent): fork/exec with passthrough IO, record the
// run, git delta and best-effort usage. Claude Code should keep using hooks
// (richer: per-tool-call guardrails); wrap has no tool-call interception.
var wrapCmd = &cobra.Command{
	Use:   "wrap [flags] -- <command...>",
	Short: "Run any CLI agent under Dandori capture (multi-runtime)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		if st.Setting("kill_switch_global") == "1" {
			return fmt.Errorf("global kill switch is ON — refusing to launch an agent")
		}
		cwd, _ := os.Getwd()
		runtime := flagWrapRuntime
		if runtime == "" {
			runtime = capture.DetectRuntime(args[0])
		}
		ing := &capture.Ingestor{Cfg: cfg, St: st}
		sessionID := fmt.Sprintf("wrap-%d", time.Now().UnixNano())
		runID, before, err := capture.StartRun(st, ing, sessionID, cwd, "wrap",
			capture.Attribution{AgentName: flagWrapAgent, TaskKey: flagWrapTask, Runtime: runtime})
		if err != nil {
			return err
		}

		// Ctrl+C goes to the whole process group: let the child handle it and
		// keep the parent alive so the run is always finalized (no stuck
		// 'running' rows polluting metrics). CLI-only — the async launcher's
		// serve process keeps its own SIGTERM handler.
		signal.Ignore(os.Interrupt, syscall.SIGTERM)
		started := time.Now()
		child := exec.Command(args[0], args[1:]...)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		runErr := child.Run()
		exitCode := child.ProcessState.ExitCode()
		after := capture.SnapshotGit(cwd)

		capture.FinalizeRun(cfg, st, ing, runID, cwd, runtime, before, after, started, exitCode)
		status := "done"
		if exitCode != 0 {
			status = "failed"
		}
		fmt.Fprintf(os.Stderr, "\n[dandori] run %s: %s · %s\n",
			runID, status, time.Since(started).Round(time.Second))
		if runErr != nil {
			// Propagate the agent's exit code faithfully.
			os.Exit(max(exitCode, 1))
		}
		return nil
	},
}

func init() {
	wrapCmd.Flags().StringVar(&flagWrapAgent, "agent", "", "agent name for attribution")
	wrapCmd.Flags().StringVar(&flagWrapRuntime, "runtime", "", "claude-code | codex | generic (default: detect)")
	wrapCmd.Flags().StringVar(&flagWrapTask, "task", "", "task key (e.g. SCRUM-42)")
	rootCmd.AddCommand(wrapCmd)
}
