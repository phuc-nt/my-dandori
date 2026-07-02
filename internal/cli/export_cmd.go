package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/govern"
)

var (
	flagExportFormat string
	flagExportOut    string
)

var exportCmd = &cobra.Command{
	Use:   "export [compliance]",
	Short: "Export the compliance bundle (audit chain + verify + governance state)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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
			fmt.Printf("compliance bundle → %s (chain ok: %v, %d audit entries)\n",
				flagExportOut, bundle.Verify.OK, len(bundle.AuditLog))
		}
		return nil
	},
}

func init() {
	exportCmd.Flags().StringVar(&flagExportFormat, "format", "json", "json | csv (csv = flat audit trail)")
	exportCmd.Flags().StringVar(&flagExportOut, "out", "", "output file (default stdout)")
	rootCmd.AddCommand(exportCmd)
}
