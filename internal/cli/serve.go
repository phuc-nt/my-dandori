package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/web"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the Dandori operations console (web UI + background workers)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		srv, err := web.New(cfg, st, flagTemplatesDir)
		if err != nil {
			return err
		}
		wireIntegrations(cfg, st, srv)
		startWorkers(cmd.Context(), cfg, st)
		fmt.Printf("dandori console → http://%s\n", cfg.Listen)
		return srv.ListenAndServe()
	},
}

var flagTemplatesDir string

func init() {
	serveCmd.Flags().StringVar(&flagTemplatesDir, "templates-dir", "",
		"read templates from disk instead of the embedded copies (dev mode)")
	rootCmd.AddCommand(serveCmd)
}
