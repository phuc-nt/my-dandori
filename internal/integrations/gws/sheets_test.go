package gws

import (
	"context"
	"testing"
)

func TestSheetsCreateGuardSkip(t *testing.T) {
	g := &fakeGate{allowed: false}
	r, argvOut := newTestRunner(t, g)
	id, url, err := r.SheetsCreate(context.Background(), "Report")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" || url != "" {
		t.Errorf("guard=false must return empty values, got id=%q url=%q", id, url)
	}
	if lines := readArgvLines(t, argvOut); len(lines) != 0 {
		t.Errorf("guard=false must not exec, argv=%v", lines)
	}
}

func TestSheetsCreateSuccess(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, argvOut := newTestRunner(t, g)
	id, url, err := r.SheetsCreate(context.Background(), "Report")
	if err != nil {
		t.Fatal(err)
	}
	if id != "fake-sheet-id" {
		t.Errorf("id: %s", id)
	}
	if url == "" {
		t.Error("url must not be empty")
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Fatalf("argv: %v", lines)
	}
	argv := lines[0]
	if !equalArgvPrefix(argv, []string{"sheets", "spreadsheets", "create", "--json"}) {
		t.Errorf("argv: %v", argv)
	}
	var payload createSpreadsheetRequest
	unmarshalFlagJSON(t, argv, "--json", &payload)
	if payload.Properties.Title != "Report" {
		t.Errorf("title: %s", payload.Properties.Title)
	}
	// The create request MUST provision a tab named "Summary" — the export
	// writes to "Summary!A1", and a spreadsheet created without it has only
	// the default "Sheet1", so the values-update would fail against the real
	// Sheets API (caught in the live E2E, not offline).
	if len(payload.Sheets) != 1 || payload.Sheets[0].Properties.Title != "Summary" {
		t.Errorf("create must provision a Summary tab, got sheets: %+v", payload.Sheets)
	}
}

func TestSheetsValuesUpdateSuccess(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, argvOut := newTestRunner(t, g)
	values := [][]string{{"Agent", "Cost"}, {"Claude", "1.23"}}
	err := r.SheetsValuesUpdate(context.Background(), "sheet-1", "A1:C3", values)
	if err != nil {
		t.Fatal(err)
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Fatalf("argv: %v", lines)
	}
	argv := lines[0]
	var params valuesUpdateParams
	unmarshalFlagJSON(t, argv, "--params", &params)
	if params.ValueInputOption != "RAW" {
		t.Errorf("valueInputOption: %s", params.ValueInputOption)
	}
	if params.SpreadsheetID != "sheet-1" || params.Range != "A1:C3" {
		t.Errorf("params: %+v", params)
	}
	var body valuesUpdateBody
	unmarshalFlagJSON(t, argv, "--json", &body)
	if len(body.Values) != 2 || body.Values[0][0] != "Agent" {
		t.Errorf("body.values: %v", body.Values)
	}
}
