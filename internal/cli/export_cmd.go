package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/integrations/gws"
)

var (
	flagExportFormat string
	flagExportOut    string
	flagExportSheets bool
	flagExportDays   int
)

var exportCmd = &cobra.Command{
	Use:   "export [compliance]",
	Short: "Export the compliance bundle (audit chain + verify + governance state), or --sheets for UC8",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagExportSheets {
			return runSheetsExport()
		}
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		bundle, err := govern.BuildComplianceBundle(st, cfg.UserName)
		if err != nil {
			return err
		}
		out := os.Stdout
		if flagExportOut != "" {
			f, err := os.Create(flagExportOut)
			if err != nil {
				return err
			}
			defer f.Close()
			out = f
		}
		switch flagExportFormat {
		case "csv":
			err = bundle.WriteCSV(out)
		case "json", "":
			err = bundle.WriteJSON(out)
		default:
			return fmt.Errorf("unknown format %q (json|csv)", flagExportFormat)
		}
		if err != nil {
			return err
		}
		if flagExportOut != "" {
			fmt.Printf("compliance bundle → %s (chain ok: %v, %d audit entries, %d signed / %d unsigned, %d coverage gaps, %d missing-audit)\n",
				flagExportOut, bundle.Verify.OK, len(bundle.AuditLog), bundle.SignedCount, bundle.UnsignedCount,
				len(bundle.Coverage.DetectorFlags), len(bundle.Coverage.MissingAudit))
		}
		return nil
	},
}

// runSheetsExport is UC8: push the fleet leaderboard + ROI snapshot to the
// config-pinned spreadsheet (or a newly-created one, saved for reuse).
// Destination is NEVER a flag — Cfg.ExportSpreadsheetID is the only source
// (C2).
func runSheetsExport() error {
	cfg, st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	guard := &integrations.Guard{Cfg: cfg, St: st}
	exporter := &gws.SheetsExporter{
		Guard: guard, GWS: gws.NewRunner(guard), St: st, Cfg: cfg,
	}
	days := flagExportDays
	if days <= 0 {
		days = cfg.LearnWindowDays
	}
	url, res, err := exporter.Export(context.Background(), days)
	if err != nil {
		return err
	}
	if url != "" {
		fmt.Printf("%s: %s\n", res, url)
	} else {
		fmt.Println(res)
	}
	return nil
}

func init() {
	exportCmd.Flags().StringVar(&flagExportFormat, "format", "json", "json | csv (csv = flat audit trail)")
	exportCmd.Flags().StringVar(&flagExportOut, "out", "", "output file (default stdout)")
	exportCmd.Flags().BoolVar(&flagExportSheets, "sheets", false, "export fleet leaderboard/ROI to the config-pinned Google Sheet (UC8)")
	exportCmd.Flags().IntVar(&flagExportDays, "days", 0, "window in days (default: config learn_window_days)")
	rootCmd.AddCommand(exportCmd)
}
