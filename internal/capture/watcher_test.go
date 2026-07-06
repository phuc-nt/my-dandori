package capture

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherCreatesAndReconciles(t *testing.T) {
	g := testIngestor(t)
	projects := t.TempDir()
	dir := filepath.Join(projects, "-work-proj")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "watched-1.jsonl"), []byte(fixtureTranscript), 0o644)
	// Metadata-only file must be skipped.
	os.WriteFile(filepath.Join(dir, "meta.jsonl"), []byte(`{"type":"custom-title","customTitle":"x"}`+"\n"), 0o644)

	n, err := g.ScanProjects(projects, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("processed: %d, want 1", n)
	}
	var source string
	var input int64
	err = g.St.DB.QueryRow(`SELECT source, input_tokens FROM runs WHERE session_id='watched-1'`).Scan(&source, &input)
	if err != nil {
		t.Fatal(err)
	}
	if source != "watcher" || input != 110 {
		t.Errorf("source=%s input=%d", source, input)
	}

	// Second sweep must not duplicate the run.
	if _, err := g.ScanProjects(projects, time.Time{}); err != nil {
		t.Fatal(err)
	}
	var count int
	g.St.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&count)
	if count != 1 {
		t.Errorf("runs duplicated: %d", count)
	}
}

func TestWatcherSetsTimesFromTranscript(t *testing.T) {
	g := testIngestor(t)
	projects := t.TempDir()
	dir := filepath.Join(projects, "-work-proj")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "timed-1.jsonl")
	os.WriteFile(path, []byte(tsFixture), 0o644)
	// Make the file stale so the run gets closed on this sweep.
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(path, old, old)

	if _, err := g.ScanProjects(projects, time.Time{}); err != nil {
		t.Fatal(err)
	}
	var started, ended, status string
	err := g.St.DB.QueryRow(`SELECT started_at, COALESCE(ended_at,''), status
		FROM runs WHERE session_id='timed-1'`).Scan(&started, &ended, &status)
	if err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Errorf("stale run should be done, got %s", status)
	}
	// started_at = first transcript ts, ended_at = last — never the mtime,
	// and ended_at strictly after started_at (no negative duration).
	if started != "2026-07-06T10:00:00Z" {
		t.Errorf("started_at = %s, want transcript first ts", started)
	}
	if ended != "2026-07-06T10:03:30Z" {
		t.Errorf("ended_at = %s, want transcript last ts", ended)
	}
	if ended <= started {
		t.Errorf("duration must be positive: %s .. %s", started, ended)
	}
}

func TestWatcherNoTimestampsLeavesEndedNull(t *testing.T) {
	g := testIngestor(t)
	projects := t.TempDir()
	dir := filepath.Join(projects, "-work-proj")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "notime-1.jsonl")
	// fixtureTranscript carries no timestamps.
	os.WriteFile(path, []byte(fixtureTranscript), 0o644)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(path, old, old)

	if _, err := g.ScanProjects(projects, time.Time{}); err != nil {
		t.Fatal(err)
	}
	var ended sql.NullString
	var status string
	err := g.St.DB.QueryRow(`SELECT ended_at, status FROM runs WHERE session_id='notime-1'`).Scan(&ended, &status)
	if err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Errorf("stale run should still close: %s", status)
	}
	if ended.Valid {
		t.Errorf("ended_at must stay NULL when no timestamp (got %q) — never fabricate the mtime", ended.String)
	}
}

func TestWatcherRespectsHookRun(t *testing.T) {
	g := testIngestor(t)
	projects := t.TempDir()
	dir := filepath.Join(projects, "-work-proj")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "hooked-1.jsonl")
	os.WriteFile(path, []byte(fixtureTranscript), 0o644)

	// Run already created by hooks.
	if _, err := g.EnsureRun("hooked-1", "/work/proj", path, "hook"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ScanProjects(projects, time.Time{}); err != nil {
		t.Fatal(err)
	}
	var source string
	var input int64
	g.St.DB.QueryRow(`SELECT source, input_tokens FROM runs WHERE session_id='hooked-1'`).Scan(&source, &input)
	if source != "hook" {
		t.Errorf("watcher must not change source: %s", source)
	}
	if input != 110 {
		t.Errorf("watcher must reconcile usage for hook runs too: %d", input)
	}
}
