package capture

import (
	"bytes"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

const payloadCap = 4096

// Ingestor writes hook/watcher activity into the store.
type Ingestor struct {
	Cfg *config.Config
	St  *store.Store
}

// EnsureRun upserts the agent and run for a session, returning the run id.
func (g *Ingestor) EnsureRun(sessionID, cwd, transcriptPath, source string) (string, error) {
	var runID string
	err := g.St.DB.QueryRow(`SELECT id FROM runs WHERE session_id = ?`, sessionID).Scan(&runID)
	if err == nil {
		return runID, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	agentName, project := ResolveAttribution(cwd)
	agentID := slugify(agentName)
	if _, err := g.St.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?, ?, ?)
		ON CONFLICT(name) DO NOTHING`, agentID, agentName, store.Now()); err != nil {
		return "", err
	}
	runID = sessionID
	_, err = g.St.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, cwd, transcript_path, started_at, source)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO NOTHING`,
		runID, sessionID, agentID, project, cwd, transcriptPath, store.Now(), source)
	return runID, err
}

// AddEvent inserts one event row and returns its id.
func (g *Ingestor) AddEvent(runID, kind, toolName string, ok sql.NullInt64, payload string) (int64, error) {
	res, err := g.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, ?, ?, ?, ?, ?)`, runID, store.Now(), kind, toolName, ok, payload)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SessionStart registers the run and snapshots the git state so Stop can
// compute the session's code delta (hooks run as separate processes, so the
// before-state is persisted in the run row + settings).
func (g *Ingestor) SessionStart(in HookInput) error {
	runID, err := g.EnsureRun(in.SessionID, in.CWD, in.TranscriptPath, "hook")
	if err != nil {
		return err
	}
	if gs := SnapshotGit(in.CWD); gs.IsRepo {
		_, _ = g.St.DB.Exec(`UPDATE runs SET head_before = ? WHERE id = ? AND head_before IS NULL`, gs.Head, runID)
		_ = g.St.SetSetting("gitdirty:"+runID, fmt.Sprintf("%d,%d", gs.Added, gs.Deleted))
	}
	return nil
}

// PreTool records a tool_use event and returns run and event ids for the
// guardrail engine to reference in decisions and audit entries.
func (g *Ingestor) PreTool(in HookInput) (runID string, eventID int64, err error) {
	runID, err = g.EnsureRun(in.SessionID, in.CWD, in.TranscriptPath, "hook")
	if err != nil {
		return "", 0, err
	}
	eventID, err = g.AddEvent(runID, "tool_use", in.ToolName, sql.NullInt64{}, truncate(in.ToolInput, payloadCap))
	return runID, eventID, err
}

// PostTool records the tool_result and opportunistically reconciles usage.
func (g *Ingestor) PostTool(in HookInput) error {
	runID, err := g.EnsureRun(in.SessionID, in.CWD, in.TranscriptPath, "hook")
	if err != nil {
		return err
	}
	ok := sql.NullInt64{Int64: 1, Valid: true}
	if isErrorResponse(in.ToolResponse) {
		ok.Int64 = 0
	}
	if _, err := g.AddEvent(runID, "tool_result", in.ToolName, ok, truncate(in.ToolResponse, payloadCap)); err != nil {
		return err
	}
	return g.maybeReconcile(runID, in.TranscriptPath)
}

// Stop marks the run finished, records the session git delta and does a
// final usage reconcile.
func (g *Ingestor) Stop(in HookInput) error {
	runID, err := g.EnsureRun(in.SessionID, in.CWD, in.TranscriptPath, "hook")
	if err != nil {
		return err
	}
	if _, err := g.St.DB.Exec(`UPDATE runs SET status = CASE WHEN status = 'running' THEN 'done' ELSE status END,
		ended_at = ? WHERE id = ?`, store.Now(), runID); err != nil {
		return err
	}
	g.recordGitDelta(runID, in.CWD)
	// Session-scoped settings keys are no longer needed once the run ends.
	_, _ = g.St.DB.Exec(`DELETE FROM settings WHERE key IN (?, ?)`,
		"gitdirty:"+runID, "reconciled_at:"+runID)
	return g.ReconcileUsage(runID, in.TranscriptPath)
}

// recordGitDelta computes the session's code volume from the SessionStart
// snapshot (best-effort: capture is fail-open, git errors are ignored).
func (g *Ingestor) recordGitDelta(runID, cwd string) {
	after := SnapshotGit(cwd)
	if !after.IsRepo {
		return
	}
	var headBefore sql.NullString
	_ = g.St.DB.QueryRow(`SELECT head_before FROM runs WHERE id = ?`, runID).Scan(&headBefore)
	before := GitState{Head: headBefore.String, IsRepo: headBefore.Valid && headBefore.String != ""}
	if dirty := g.St.Setting("gitdirty:" + runID); dirty != "" {
		fmt.Sscanf(dirty, "%d,%d", &before.Added, &before.Deleted)
	}
	if !before.IsRepo {
		// No start snapshot (older run / hook missed) — record HEAD only.
		_, _ = g.St.DB.Exec(`UPDATE runs SET head_after = ? WHERE id = ?`, after.Head, runID)
		return
	}
	added, deleted := SessionDelta(cwd, before, after)
	_, _ = g.St.DB.Exec(`UPDATE runs SET head_after = ?, lines_added = ?, lines_deleted = ? WHERE id = ?`,
		after.Head, added, deleted, runID)
}

// maybeReconcile throttles transcript reparsing to once per 10s per run —
// keeps budget checks fresh without reparsing on every tool call.
func (g *Ingestor) maybeReconcile(runID, transcriptPath string) error {
	key := "reconciled_at:" + runID
	if last := g.St.Setting(key); last != "" {
		if t, err := time.Parse(time.RFC3339, last); err == nil && time.Since(t) < 10*time.Second {
			return nil
		}
	}
	if err := g.ReconcileUsage(runID, transcriptPath); err != nil {
		return err
	}
	return g.St.SetSetting(key, store.Now())
}

// ReconcileUsage reparses the transcript and SETs run usage/cost (idempotent).
func (g *Ingestor) ReconcileUsage(runID, transcriptPath string) error {
	if transcriptPath == "" {
		return nil
	}
	u, err := ParseTranscript(transcriptPath)
	if err != nil {
		return err // caller decides; hook command logs and exits 0
	}
	cost := g.Cfg.Cost(u.Model, u.Input, u.Output, u.CacheRead, u.CacheWrite)
	taskKey := FindTaskKey(u.FirstUser)
	_, err = g.St.DB.Exec(`UPDATE runs SET model = COALESCE(NULLIF(?, ''), model),
		input_tokens = ?, output_tokens = ?, cache_read_tokens = ?, cache_write_tokens = ?,
		cost_usd = ?, task_key = COALESCE(NULLIF(task_key, ''), NULLIF(?, ''), task_key)
		WHERE id = ?`,
		u.Model, u.Input, u.Output, u.CacheRead, u.CacheWrite, cost, taskKey, runID)
	if err != nil {
		return err
	}
	return g.recordUserMsgs(runID, u.MidRunMsgs)
}

// recordUserMsgs keeps a single user_msg-count event per run up to date.
// The count is MID-RUN steering messages only (autonomy metric input;
// SET semantics like usage).
func (g *Ingestor) recordUserMsgs(runID string, count int) error {
	payload := strconv.Itoa(count)
	res, err := g.St.DB.Exec(`UPDATE events SET payload = ? WHERE run_id = ? AND kind = 'user_msg'`, payload, runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err = g.AddEvent(runID, "user_msg", "", sql.NullInt64{}, payload)
	}
	return err
}

// isErrorResponse heuristically detects tool failure from the raw response.
func isErrorResponse(raw []byte) bool {
	return bytes.Contains(raw, []byte(`"is_error":true`)) ||
		bytes.Contains(raw, []byte(`"success":false`)) ||
		bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"Error`))
}
