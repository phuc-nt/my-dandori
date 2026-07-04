package capture

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

// runcore holds the capture logic shared by the CLI `wrap` command and the
// async console launcher (v6): create+attribute a run, and finalize it with
// git delta, usage, cost and status. Extracting it keeps the two launch
// paths DRY — neither reimplements attribution or finalization.

// Attribution overrides agent/task/runtime for a run. Empty fields are left
// as resolved by EnsureRun.
type Attribution struct {
	AgentName string
	TaskKey   string
	Runtime   string
}

// StartRun creates (or reuses) the run row for a session and applies
// attribution + the git before-snapshot. Returns the run id and the snapshot
// the reaper needs for the code delta.
func StartRun(st *store.Store, ing *Ingestor, sessionID, cwd, source string, attr Attribution) (runID string, before GitState, err error) {
	runID, err = ing.EnsureRun(sessionID, cwd, "", source)
	if err != nil {
		return "", GitState{}, err
	}
	if err = ApplyAttribution(st, runID, attr); err != nil {
		return "", GitState{}, fmt.Errorf("attribution: %w", err)
	}
	return runID, SnapshotGit(cwd), nil
}

// ApplyAttribution overrides agent/task and records the runtime. The agent id
// is resolved by NAME after upsert (ids are slugified; a same-named agent may
// exist under a different id — blindly setting agent_id would hit the FK and
// silently misattribute the run).
func ApplyAttribution(st *store.Store, runID string, attr Attribution) error {
	if attr.AgentName != "" {
		if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?, ?, ?)
			ON CONFLICT(name) DO NOTHING`, attr.AgentName, attr.AgentName, store.Now()); err != nil {
			return err
		}
		var agentID string
		if err := st.DB.QueryRow(`SELECT id FROM agents WHERE name = ?`, attr.AgentName).Scan(&agentID); err != nil {
			return err
		}
		if _, err := st.DB.Exec(`UPDATE runs SET agent_id = ? WHERE id = ?`, agentID, runID); err != nil {
			return err
		}
	}
	if attr.TaskKey != "" {
		if _, err := st.DB.Exec(`UPDATE runs SET task_key = ? WHERE id = ?`, attr.TaskKey, runID); err != nil {
			return err
		}
	}
	_, err := st.DB.Exec(`UPDATE runs SET runtime = ? WHERE id = ?`, attr.Runtime, runID)
	return err
}

// FinalizeRun records the git delta, usage, cost and terminal status of a
// finished run. exitCode 0 → done, else failed (a later KillRun may overwrite
// to killed — that is the terminal truth). Idempotent enough for the reaper.
func FinalizeRun(cfg *config.Config, st *store.Store, ing *Ingestor,
	runID, cwd, runtime string, before, after GitState, started time.Time, exitCode int) {

	status := "done"
	if exitCode != 0 {
		status = "failed"
	}
	added, deleted := SessionDelta(cwd, before, after)
	u := AdapterFor(runtime, cfg.ProjectsDir).UsageAfterRun(cwd, started.Unix())
	cost := cfg.Cost(u.Model, u.Input, u.Output, u.CacheRead, u.CacheWrite)
	// A run killed by an operator stays 'killed' — the reaper must not
	// downgrade it to 'failed' just because the signalled child exited
	// non-zero (kill is the terminal truth, not the exit code).
	_, _ = st.DB.Exec(`UPDATE runs SET
		status = CASE WHEN status = 'killed' THEN 'killed' ELSE ? END,
		ended_at = ?, exit_code = ?,
		lines_added = ?, lines_deleted = ?, head_before = ?, head_after = ?,
		model = COALESCE(NULLIF(?, ''), model),
		input_tokens = ?, output_tokens = ?, cache_read_tokens = ?, cache_write_tokens = ?, cost_usd = ?
		WHERE id = ?`,
		status, store.Now(), exitCode, added, deleted, before.Head, after.Head,
		u.Model, u.Input, u.Output, u.CacheRead, u.CacheWrite, cost, runID)
	ok := sql.NullInt64{Int64: 0, Valid: true}
	if exitCode == 0 {
		ok.Int64 = 1
	}
	_, _ = ing.AddEvent(runID, "wrap_exec", runtime, ok,
		fmt.Sprintf("exit=%d duration=%s +%d/-%d lines", exitCode, time.Since(started).Round(time.Second), added, deleted))
}
