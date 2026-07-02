package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/ingest"
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
		if err := st.EnableReadPool(); err != nil {
			fmt.Println("read pool disabled:", err) // reads fall back to the writer conn
		}
		srv, err := web.New(cfg, st, flagTemplatesDir)
		if err != nil {
			return err
		}
		wireIntegrations(cfg, st, srv)
		startWorkers(cmd.Context(), cfg, st)
		// The ingest listener is a SEPARATE server: routable bind, token
		// auth, zero console routes. It only starts when a token is set —
		// no token, no network surface.
		if cfg.IngestToken != "" {
			ing := ingest.NewServer(cfg, st)
			go func() {
				fmt.Printf("dandori ingest  → http://%s (token-authed)\n", cfg.IngestListen)
				if err := ing.ListenAndServe(); err != nil {
					fmt.Println("ingest listener:", err)
				}
			}()
		}
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
