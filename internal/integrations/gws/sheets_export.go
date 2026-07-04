package gws

import (
	"context"
	"fmt"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// exportSpreadsheetSetting persists the id of a Sheets-export target this
// process created (config didn't pin one), so subsequent exports reuse it
// instead of creating a new sheet every time.
const exportSpreadsheetSetting = "gws_export_spreadsheet_id"

// SheetsExporter pushes the fleet leaderboard + ROI snapshot (UC8) to a
// Google Sheet. The target is config-pinned (C2): Cfg.ExportSpreadsheetID
// when set, otherwise a new sheet is created once and its id is saved to
// settings for reuse — no request path ever chooses the destination.
type SheetsExporter struct {
	Guard Gate // outer dedup guard; Runner also gates its own calls
	GWS   *Runner
	St    *store.Store
	Cfg   *config.Config
}

// Export builds today's DigestData and writes it to the Summary sheet of
// the config-pinned (or newly-created-then-saved) spreadsheet. Returns the
// spreadsheet URL and a leg result: "sent", "dry-run", or "deduped".
func (e *SheetsExporter) Export(ctx context.Context, days int) (url, res string, err error) {
	date := time.Now().UTC().Format("2006-01-02")
	target := e.Cfg.ExportSpreadsheetID
	targetKey := target
	if targetKey == "" {
		targetKey = "new"
	}
	dedup := fmt.Sprintf("sheets:%dd:%s:%s", days, date, targetKey)

	var n int
	e.St.DB.QueryRow(`SELECT count(*) FROM notifications WHERE dedup = ?`, dedup).Scan(&n)
	if n > 0 {
		return e.priorURL(dedup), "deduped", nil
	}

	if !e.Guard.Allow("gws.sheets_export", dedup) {
		return "", "dry-run", nil
	}

	data, err := learn.BuildDigestData(e.St, days)
	if err != nil {
		return "", "", err
	}

	id := target
	if id == "" {
		if id = e.St.Setting(exportSpreadsheetSetting); id == "" {
			newID, newURL, err := e.GWS.SheetsCreate(ctx, "Dandori fleet export")
			if err != nil {
				return "", "", err
			}
			if newID == "" {
				// Guard blocked SheetsCreate itself (shouldn't happen since the
				// outer Guard already allowed, but stay defensive).
				return "", "dry-run", nil
			}
			id = newID
			url = newURL
			if err := e.St.SetSetting(exportSpreadsheetSetting, id); err != nil {
				return "", "", fmt.Errorf("persist spreadsheet id: %w", err)
			}
		}
	}

	rows := buildSheetRows(data)
	if err := e.GWS.SheetsValuesUpdate(ctx, id, "Summary!A1", rows); err != nil {
		return "", "", err
	}
	if url == "" {
		url = "https://docs.google.com/spreadsheets/d/" + id + "/edit"
	}

	e.St.DB.Exec(`INSERT INTO notifications(kind, dedup, sent_at, detail)
		VALUES('sheets', ?, ?, ?) ON CONFLICT(dedup) DO NOTHING`, dedup, store.Now(), url)
	e.audit("sheets_exported", id, url)
	return url, "sent", nil
}

// priorURL recovers the URL stored on notifications.detail for a dedup key
// (so a "deduped" leg still tells the caller where the sheet lives).
func (e *SheetsExporter) priorURL(dedup string) string {
	var detail string
	e.St.DB.QueryRow(`SELECT detail FROM notifications WHERE dedup = ?`, dedup).Scan(&detail)
	return detail
}

func (e *SheetsExporter) audit(action, subject, detail string) {
	_, _ = e.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(NULL, ?, ?, ?, 1, ?)`, store.Now(), action, subject, detail)
}

// buildSheetRows formats DigestData as RAW string rows: header, leaderboard,
// a blank separator, then the fleet ROI summary. Numbers use fixed decimals
// since Sheets values are always strings under valueInputOption=RAW.
func buildSheetRows(d *learn.DigestData) [][]string {
	rows := [][]string{
		{fmt.Sprintf("Dandori fleet leaderboard (%dd window)", d.WindowDays)},
		{"Agent", "Grade", "Composite", "Runs", "Cost USD", "Useful %"},
	}
	for _, row := range d.Board {
		grade := row.Grade.Letter
		if row.Grade.Uncalibrated {
			grade += "*"
		}
		rows = append(rows, []string{
			row.AgentName, grade,
			fmt.Sprintf("%.1f", row.Composite),
			fmt.Sprintf("%d", row.Runs),
			fmt.Sprintf("%.2f", row.CostUSD),
			fmt.Sprintf("%.0f", row.ROI.UsefulPct),
		})
	}
	rows = append(rows, []string{})
	rows = append(rows, []string{"Fleet ROI"})
	if d.FleetROI != nil {
		rows = append(rows,
			[]string{"Total USD", fmt.Sprintf("%.2f", d.FleetROI.TotalUSD)},
			[]string{"Wasted USD", fmt.Sprintf("%.2f", d.FleetROI.WastedUSD)},
			[]string{"Useful %", fmt.Sprintf("%.0f", d.FleetROI.UsefulPct)},
		)
	}
	rows = append(rows, []string{"AI change-failure rate", fmt.Sprintf("%.0f%%", d.CFR.Value)})
	return rows
}
