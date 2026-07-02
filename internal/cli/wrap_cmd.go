package cli

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
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
		runID, err := ing.EnsureRun(sessionID, cwd, "", "wrap")
		if err != nil {
			return err
		}
		if err := applyWrapAttribution(st, runID, runtime); err != nil {
			return fmt.Errorf("attribution: %w", err)
		}

		// Ctrl+C goes to the whole process group: let the child handle it and
		// keep the parent alive so the run is always finalized (no stuck
		// 'running' rows polluting metrics).
		signal.Ignore(os.Interrupt, syscall.SIGTERM)
		before := capture.SnapshotGit(cwd)
		started := time.Now()
		child := exec.Command(args[0], args[1:]...)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		runErr := child.Run()
		exitCode := child.ProcessState.ExitCode()
		after := capture.SnapshotGit(cwd)

		finalizeWrapRun(cfg, st, ing, runID, cwd, runtime, before, after, started, exitCode)
		if runErr != nil {
			// Propagate the agent's exit code faithfully.
			os.Exit(max(exitCode, 1))
		}
		return nil
	},
}

// applyWrapAttribution overrides agent/task when flags are set. The agent id
// is resolved by NAME after upsert (hooks slugify ids; a same-named agent may
// already exist under a different id — blindly updating agent_id would hit
// the FK and silently misattribute the run).
func applyWrapAttribution(st *store.Store, runID, runtime string) error {
	if flagWrapAgent != "" {
		if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?, ?, ?)
			ON CONFLICT(name) DO NOTHING`, flagWrapAgent, flagWrapAgent, store.Now()); err != nil {
			return err
		}
		var agentID string
		if err := st.DB.QueryRow(`SELECT id FROM agents WHERE name = ?`, flagWrapAgent).Scan(&agentID); err != nil {
			return err
		}
		if _, err := st.DB.Exec(`UPDATE runs SET agent_id = ? WHERE id = ?`, agentID, runID); err != nil {
			return err
		}
	}
	if flagWrapTask != "" {
		if _, err := st.DB.Exec(`UPDATE runs SET task_key = ? WHERE id = ?`, flagWrapTask, runID); err != nil {
			return err
		}
	}
	_, err := st.DB.Exec(`UPDATE runs SET runtime = ? WHERE id = ?`, runtime, runID)
	return err
}

func finalizeWrapRun(cfg *config.Config, st *store.Store, ing *capture.Ingestor,
	runID, cwd, runtime string, before, after capture.GitState, started time.Time, exitCode int) {

	status := "done"
	if exitCode != 0 {
		status = "failed"
	}
	added, deleted := capture.SessionDelta(cwd, before, after)
	u := capture.AdapterFor(runtime, cfg.ProjectsDir).UsageAfterRun(cwd, started.Unix())
	cost := cfg.Cost(u.Model, u.Input, u.Output, u.CacheRead, u.CacheWrite)
	_, _ = st.DB.Exec(`UPDATE runs SET status = ?, ended_at = ?,
		lines_added = ?, lines_deleted = ?, head_before = ?, head_after = ?,
		model = COALESCE(NULLIF(?, ''), model),
		input_tokens = ?, output_tokens = ?, cache_read_tokens = ?, cache_write_tokens = ?, cost_usd = ?
		WHERE id = ?`,
		status, store.Now(), added, deleted, before.Head, after.Head,
		u.Model, u.Input, u.Output, u.CacheRead, u.CacheWrite, cost, runID)
	ok := sql.NullInt64{Int64: 0, Valid: true}
	if exitCode == 0 {
		ok.Int64 = 1
	}
	_, _ = ing.AddEvent(runID, "wrap_exec", runtime, ok,
		fmt.Sprintf("exit=%d duration=%s +%d/-%d lines", exitCode, time.Since(started).Round(time.Second), added, deleted))
	fmt.Fprintf(os.Stderr, "\n[dandori] run %s: %s · %s · $%.4f · +%d/-%d lines\n",
		runID, status, time.Since(started).Round(time.Second), cost, added, deleted)
}

func init() {
	wrapCmd.Flags().StringVar(&flagWrapAgent, "agent", "", "agent name for attribution")
	wrapCmd.Flags().StringVar(&flagWrapRuntime, "runtime", "", "claude-code | codex | generic (default: detect)")
	wrapCmd.Flags().StringVar(&flagWrapTask, "task", "", "task key (e.g. SCRUM-42)")
	rootCmd.AddCommand(wrapCmd)
}
