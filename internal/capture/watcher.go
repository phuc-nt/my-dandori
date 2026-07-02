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
	if err := g.ReconcileUsage(runID, path); err != nil {
		return false, err
	}
	if time.Since(mtime) > staleAfter {
		_, err = g.St.DB.Exec(`UPDATE runs SET status = 'done', ended_at = COALESCE(ended_at, ?)
			WHERE id = ? AND status = 'running' AND source = 'watcher'`, mtime.UTC().Format(time.RFC3339), runID)
	}
	return true, err
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
