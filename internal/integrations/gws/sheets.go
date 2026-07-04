package gws

import (
	"context"
	"encoding/json"
	"fmt"
)

type sheetProperties struct {
	Title      string `json:"title"`
	Locale     string `json:"locale,omitempty"`
	AutoRecalc string `json:"autoRecalc,omitempty"`
	TimeZone   string `json:"timeZone,omitempty"`
}

// gridSheetProperties names a single tab inside the spreadsheet. We create
// the spreadsheet with a tab literally named "Summary" so the export's
// "Summary!A1" range resolves — a freshly created spreadsheet otherwise has
// only the default "Sheet1", and writing to "Summary!A1" fails.
type gridSheetProperties struct {
	Title string `json:"title"`
}

type sheetSpec struct {
	Properties gridSheetProperties `json:"properties"`
}

type createSpreadsheetRequest struct {
	Properties sheetProperties `json:"properties"`
	Sheets     []sheetSpec     `json:"sheets,omitempty"`
}

type createSpreadsheetResponse struct {
	SpreadsheetID  string `json:"spreadsheetId"`
	SpreadsheetURL string `json:"spreadsheetUrl"`
}

// SheetsCreate creates a new spreadsheet with the given title and returns
// its id and URL. Sheets export is analytical data, not a state-changing
// write — still Guard-gated per the plan's blanket write rule.
func (r *Runner) SheetsCreate(ctx context.Context, title string) (id, url string, err error) {
	if !r.Guard.Allow("gws.sheets_create", title) {
		return "", "", nil
	}
	req := createSpreadsheetRequest{
		Properties: sheetProperties{Title: title, TimeZone: "UTC"},
		Sheets:     []sheetSpec{{Properties: gridSheetProperties{Title: "Summary"}}},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return "", "", err
	}
	out, err := r.run(ctx, "sheets", "spreadsheets", "create", "--json", string(payload))
	if err != nil {
		return "", "", err
	}
	var resp createSpreadsheetResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", "", fmt.Errorf("gws sheets create: parse response: %w", err)
	}
	return resp.SpreadsheetID, resp.SpreadsheetURL, nil
}

type valuesUpdateParams struct {
	SpreadsheetID    string `json:"spreadsheetId"`
	Range            string `json:"range"`
	ValueInputOption string `json:"valueInputOption"`
}

type valuesUpdateBody struct {
	MajorDimension string     `json:"majorDimension"`
	Values         [][]string `json:"values"`
}

// SheetsValuesUpdate overwrites a range with the given rows (RAW input —
// literal strings, no formula parsing).
func (r *Runner) SheetsValuesUpdate(ctx context.Context, spreadsheetID, rng string, values [][]string) error {
	detail := spreadsheetID + "!" + rng
	if !r.Guard.Allow("gws.sheets_values_update", detail) {
		return nil
	}
	params, err := json.Marshal(valuesUpdateParams{
		SpreadsheetID: spreadsheetID, Range: rng, ValueInputOption: "RAW",
	})
	if err != nil {
		return err
	}
	body, err := json.Marshal(valuesUpdateBody{MajorDimension: "ROWS", Values: values})
	if err != nil {
		return err
	}
	_, err = r.run(ctx, "sheets", "spreadsheets", "values", "update",
		"--params", string(params), "--json", string(body))
	if err != nil {
		return fmt.Errorf("gws sheets values update %s: %w", detail, err)
	}
	return nil
}
