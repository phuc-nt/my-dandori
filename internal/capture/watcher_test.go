package capture

import (
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
