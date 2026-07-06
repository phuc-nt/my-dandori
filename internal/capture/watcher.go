package capture

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// staleAfter is how long a watcher-sourced run may sit without transcript
// activity before it is considered finished.
const staleAfter = 30 * time.Minute

// ScanProjects walks projectsDir (~/.claude/projects) and ingests any session
// transcript modified after `since`. Sessions unseen by hooks become runs with
// source=watcher; existing runs get their usage reconciled. Returns count of
// transcripts processed.
func (g *Ingestor) ScanProjects(projectsDir string, since time.Time) (int, error) {
	matches, err := filepath.Glob(filepath.Join(projectsDir, "*", "*.jsonl"))
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, path := range matches {
		fi, err := os.Stat(path)
		if err != nil || fi.ModTime().Before(since) {
			continue
		}
		ingested, err := g.ingestTranscript(path, fi.ModTime())
		if err != nil || !ingested {
			continue // one bad transcript must not stop the sweep
		}
		processed++
	}
	return processed, nil
}

// ingestTranscript ensures a run exists for the transcript and reconciles it.
// Returns false when the file carries no session data (titles, snapshots).
func (g *Ingestor) ingestTranscript(path string, mtime time.Time) (bool, error) {
	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	u, err := ParseTranscript(path)
	if err != nil {
		return false, err
	}
	if u.Model == "" && u.FirstUser == "" {
		return false, nil // metadata-only file — not a session
	}
	cwd := u.CWD
	if cwd == "" {
		cwd = filepath.Dir(path)
	}
	runID, err := g.EnsureRun(sessionID, cwd, path, "watcher")
	if err != nil {
		return false, err
	}
	// First sighting snapshots the git before-state (idempotent via the
	// head_before-IS-NULL guard). The watcher sees a session only after it
	// starts, so this delta is partial — it misses edits made before the
	// first sweep. Partial-but-real beats the 0 lines watcher runs recorded
	// before; provenance in docs notes the undercount.
	g.snapshotStart(runID, cwd)
	// A stale transcript means the session finished. Flip status BEFORE
	// reconciling so reconcileWatcherTimes (which sets ended_at only once the
	// run is no longer 'running') records the true end time on this sweep.
	if time.Since(mtime) > staleAfter {
		// ended_at is left to reconcile (from the transcript's last
		// timestamp), NOT the file mtime — mtime is when the watcher last
		// touched the file, which for a long-idle session drifts far past
		// the real end and produced the negative durations this fixes. When
		// the transcript has no usable timestamp, ended_at stays NULL
		// (unknown, not fabricated).
		if _, err = g.St.DB.Exec(`UPDATE runs SET status = 'done'
			WHERE id = ? AND status = 'running' AND source = 'watcher'`, runID); err != nil {
			return true, err
		}
		g.recordGitDelta(runID, cwd)
	}
	if err := g.ReconcileUsage(runID, path); err != nil {
		return false, err
	}
	return true, nil
}

// WatchOnce performs one watcher sweep using the persisted checkpoint.
func (g *Ingestor) WatchOnce(projectsDir string) (int, error) {
	since := time.Time{}
	if v := g.St.Setting("last_watch_ts"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	start := time.Now().UTC()
	n, err := g.ScanProjects(projectsDir, since)
	if err == nil {
		err = g.St.SetSetting("last_watch_ts", start.Format(time.RFC3339))
	}
	return n, err
}
