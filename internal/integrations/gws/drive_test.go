package gws

import (
	"context"
	"strings"
	"testing"
)

func TestDriveListNoGuardNeeded(t *testing.T) {
	// DriveList is read-only: pass a guard that always denies to prove it
	// is never consulted (no Allow call) yet the exec still happens.
	g := &fakeGate{allowed: false}
	r, argvOut := newTestRunner(t, g)
	files, err := r.DriveList(context.Background(), "name contains 'Plan'")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.calls) != 0 {
		t.Errorf("DriveList must not call Guard.Allow, calls=%v", g.calls)
	}
	if len(files) != 1 || files[0].ID != "fake-file-id" {
		t.Errorf("files: %+v", files)
	}
	if lines := readArgvLines(t, argvOut); len(lines) != 1 {
		t.Errorf("expected exactly one exec: %v", lines)
	}
}

func TestDriveListHostileQueryRoundTrips(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, argvOut := newTestRunner(t, g)
	hostile := `name contains "Quarterly \ Plan"`
	if _, err := r.DriveList(context.Background(), hostile); err != nil {
		t.Fatal(err)
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Fatalf("argv: %v", lines)
	}
	var params driveListParams
	unmarshalFlagJSON(t, lines[0], "--params", &params)
	if params.Q != hostile {
		t.Errorf("hostile query broke JSON round-trip: got %q, want %q", params.Q, hostile)
	}
}

func TestDriveExportNoGuardNeeded(t *testing.T) {
	g := &fakeGate{allowed: false}
	r, _ := newTestRunner(t, g)
	out, err := r.DriveExport(context.Background(), "file-1", "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.calls) != 0 {
		t.Errorf("DriveExport must not call Guard.Allow, calls=%v", g.calls)
	}
	if !strings.Contains(string(out), "fake exported content") {
		t.Errorf("export content: %q", out)
	}
}

func TestDriveExportRejectsOversized(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, _ := newTestRunner(t, g)
	t.Setenv("FAKE_EXPORT_HUGE", "1")
	_, err := r.DriveExport(context.Background(), "file-1", "text/plain")
	if err != ErrExportTooLarge {
		t.Errorf("DriveExport(11MB) error = %v, want ErrExportTooLarge", err)
	}
}
