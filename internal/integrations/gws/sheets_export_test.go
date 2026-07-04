package gws

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func exportFixture(t *testing.T, dryRun bool, spreadsheetID string) (*SheetsExporter, *store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "done", 1, 2.5)

	bin, err := filepath.Abs("../testdata/fake-gws")
	if err != nil {
		t.Fatal(err)
	}
	argvOut := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("FAKE_ARGV_OUT", argvOut)

	cfg := &config.Config{DryRun: dryRun, ExportSpreadsheetID: spreadsheetID}
	guard := &integrations.Guard{Cfg: cfg, St: st}
	runner := &Runner{Bin: bin, Guard: guard}
	exporter := &SheetsExporter{Guard: guard, GWS: runner, St: st, Cfg: cfg}
	return exporter, st, argvOut
}

func TestSheetsExportDryRun(t *testing.T) {
	exporter, st, argvOut := exportFixture(t, true, "")
	url, res, err := exporter.Export(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if res != "dry-run" || url != "" {
		t.Errorf("got url=%q res=%q, want dry-run/empty", url, res)
	}
	if lines := readArgvLines(t, argvOut); len(lines) != 0 {
		t.Errorf("dry-run must not exec gws, argv=%v", lines)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM notifications WHERE kind='sheets'`).Scan(&n)
	if n != 0 {
		t.Errorf("dry-run must not record a notification, got %d", n)
	}
}

func TestSheetsExportCreatesAndReusesConfiguredTarget(t *testing.T) {
	exporter, _, argvOut := exportFixture(t, false, "pinned-sheet-id")
	url, res, err := exporter.Export(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if res != "sent" {
		t.Fatalf("first export res=%q, want sent", res)
	}
	if url == "" {
		t.Error("url must not be empty on success")
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 gws call (values update, no create — target pinned), got %v", lines)
	}
	if !equalArgvPrefix(lines[0], []string{"sheets", "spreadsheets", "values", "update"}) {
		t.Errorf("argv: %v", lines[0])
	}
}

func TestSheetsExportDedupSameDayTarget(t *testing.T) {
	exporter, _, argvOut := exportFixture(t, false, "pinned-sheet-id")
	if _, res, err := exporter.Export(context.Background(), 30); err != nil || res != "sent" {
		t.Fatalf("first export: res=%q err=%v", res, err)
	}
	url2, res2, err := exporter.Export(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if res2 != "deduped" {
		t.Errorf("second same-day export: got %q, want deduped", res2)
	}
	if url2 == "" {
		t.Error("deduped export should still return the prior URL")
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Errorf("deduped export must not call gws again, got %d calls", len(lines))
	}
}

func TestSheetsExportAutoCreateWhenUnset(t *testing.T) {
	exporter, st, argvOut := exportFixture(t, false, "")
	url, res, err := exporter.Export(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if res != "sent" || url == "" {
		t.Fatalf("res=%q url=%q", res, url)
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 2 {
		t.Fatalf("expected create + values-update (2 calls), got %v", lines)
	}
	if !equalArgvPrefix(lines[0], []string{"sheets", "spreadsheets", "create"}) {
		t.Errorf("first call should be create, got %v", lines[0])
	}
	saved := st.Setting(exportSpreadsheetSetting)
	if saved != "fake-sheet-id" {
		t.Errorf("created spreadsheet id not persisted: got %q", saved)
	}

	// Second export (still no config id) must reuse the saved id, not create again.
	_, res2, err := exporter.Export(context.Background(), 7) // different window → different dedup key
	if err != nil {
		t.Fatal(err)
	}
	if res2 != "sent" {
		t.Fatalf("res2=%q, want sent (different window is not a dedup hit)", res2)
	}
	lines2 := readArgvLines(t, argvOut)
	if len(lines2) != 3 {
		t.Fatalf("expected 1 more call (values-update only, id reused), got %d total", len(lines2))
	}
	if !equalArgvPrefix(lines2[2], []string{"sheets", "spreadsheets", "values", "update"}) {
		t.Errorf("third call should be values-update (id reused, no second create), got %v", lines2[2])
	}
}
