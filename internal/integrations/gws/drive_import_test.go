package gws

import (
	"context"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/redact"
)

// scanner mirrors contexthub.SecretFragment without importing contexthub
// (gws sits below contexthub/observer in the import graph — see
// drive_import.go's package doc). redact is the same package
// contexthub.SecretFragment is built on, so this test exercises the same
// detection behavior the real caller wires in.
func testScanner(content string) string {
	red := redact.String(content)
	if red == content {
		return ""
	}
	return "found"
}

// Search excludes folders (C1 §6) even when the fixture returns one, and
// wraps the caller's query in a Docs-only, non-trashed filter that DriveList
// marshals as a JSON struct field (M1) — never string-concatenated raw.
func TestDriveImporterSearchExcludesFolders(t *testing.T) {
	g := &fakeGate{allowed: false}
	r, argvOut := newTestRunner(t, g)
	t.Setenv("FAKE_DRIVE_INCLUDE_FOLDER", "1")
	imp := &DriveImporter{GWS: r, Scanner: testScanner}
	files, err := imp.Search(context.Background(), "Plan")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].MimeType != docsMimeType {
		t.Fatalf("Search results = %+v, want exactly 1 Docs file (folder excluded)", files)
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Fatalf("argv: %v", lines)
	}
	var params driveListParams
	unmarshalFlagJSON(t, lines[0], "--params", &params)
	if !strings.Contains(params.Q, docsMimeType) {
		t.Errorf("query %q missing Docs mimeType filter", params.Q)
	}
}

// M1: a hostile `"`/`\` in the search term must round-trip through
// DriveList's JSON struct field without breaking the emitted --params
// payload (DriveList already proves this for its own Q field; this proves
// DriveImporter's wrapped query string is still well-formed end to end).
func TestDriveImporterSearchHostileQuery(t *testing.T) {
	g := &fakeGate{allowed: false}
	r, argvOut := newTestRunner(t, g)
	imp := &DriveImporter{GWS: r, Scanner: testScanner}
	hostile := `Quarterly " Plan \ v2`
	if _, err := imp.Search(context.Background(), hostile); err != nil {
		t.Fatal(err)
	}
	lines := readArgvLines(t, argvOut)
	var params driveListParams
	unmarshalFlagJSON(t, lines[0], "--params", &params)
	// M1: the --params JSON round-trips without break-out (unmarshal above
	// already proves well-formedness). The DSL wrapper additionally escapes
	// ' and \ so the query stays valid: the backslash in the term is doubled,
	// the double-quote is untouched (the DSL literal is single-quoted).
	wantEscaped := `name contains 'Quarterly " Plan \\ v2' and mimeType`
	if !strings.Contains(params.Q, wantEscaped) {
		t.Errorf("params.Q = %q, want it to contain the escaped DSL term %q", params.Q, wantEscaped)
	}
}

// Review blocks (TooBig, no FullText) when the exported doc exceeds the
// 8k-rune merge-budget gate — the size gate on top of DriveExport's own
// 10MB byte cap.
func TestDriveImporterReviewTooBig(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, _ := newTestRunner(t, g)
	t.Setenv("FAKE_EXPORT_HUGE", "1")
	imp := &DriveImporter{GWS: r, Scanner: testScanner}
	res, err := imp.Review(context.Background(), DriveFile{ID: "f1", Name: "Huge"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TooBig || res.FullText != "" {
		t.Errorf("res = %+v, want TooBig=true and FullText empty", res)
	}
}

// C1: a secret-shaped export must never populate FullText — the body must
// not reach the browser when a secret is detected.
func TestDriveImporterReviewSecretBlocked(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, _ := newTestRunner(t, g)
	t.Setenv("FAKE_EXPORT_SECRET", "1")
	imp := &DriveImporter{GWS: r, Scanner: testScanner}
	res, err := imp.Review(context.Background(), DriveFile{ID: "f1", Name: "Leaky"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasSecret || res.FullText != "" {
		t.Errorf("res = %+v, want HasSecret=true and FullText empty (never render a secret)", res)
	}
}

// Review returns the FULL exported text — the caller's most important
// contract (C1): a payload past any historical 2000-rune truncation must
// not hide from the human reviewer.
func TestDriveImporterReviewReturnsFullBody(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, _ := newTestRunner(t, g)
	imp := &DriveImporter{GWS: r, Scanner: testScanner}
	res, err := imp.Review(context.Background(), DriveFile{ID: "f1", Name: "Doc"})
	if err != nil {
		t.Fatal(err)
	}
	const want = "fake exported content"
	if res.FullText != want {
		t.Errorf("FullText = %q, want %q (full, untruncated)", res.FullText, want)
	}
	if res.Runes != len([]rune(want)) {
		t.Errorf("Runes = %d, want %d", res.Runes, len([]rune(want)))
	}
}

// Read-only ops: neither Search nor Review may consult Guard.Allow.
func TestDriveImporterNoGuardNeeded(t *testing.T) {
	g := &fakeGate{allowed: false}
	r, _ := newTestRunner(t, g)
	imp := &DriveImporter{GWS: r, Scanner: testScanner}
	if _, err := imp.Search(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := imp.Review(context.Background(), DriveFile{ID: "f1"}); err != nil {
		t.Fatal(err)
	}
	if len(g.calls) != 0 {
		t.Errorf("Search/Review must never call Guard.Allow, calls=%v", g.calls)
	}
}
