package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/ingest"
	"github.com/phuc-nt/dandori/internal/runner"
	"github.com/phuc-nt/dandori/internal/store"
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
		// Console launcher (v6): shared registry + concurrency cap. Reconcile
		// resolves runs left mid-flight by a previous serve (dead → failed,
		// live → adopted so they stay killable).
		ing := &capture.Ingestor{Cfg: cfg, St: st}
		lau := runner.New(cfg, st, ing)
		runner.Reconcile(lau.Reg, st)
		srv.Launcher = lau
		installConsoleShutdown(st)
		wireIntegrations(cfg, st, srv)
		startWorkers(cmd.Context(), cfg, st)
		go srv.RunGC(cmd.Context()) // hourly: expired sessions + stale rate-limiter entries
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

// installConsoleShutdown marks running console-launched runs 'orphaned' on
// SIGTERM/Interrupt so they are reconciled (not left 'running') next startup.
// Children keep running in their own process group — they are NOT killed on
// serve shutdown (a restart re-adopts the live ones). Hook/wrap runs (no pid)
// are untouched.
func installConsoleShutdown(st *store.Store) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		st.DB.Exec(`UPDATE runs SET status = 'orphaned' WHERE source = 'console' AND status = 'running'`)
		os.Exit(0)
	}()
}

var flagTemplatesDir string

func init() {
	serveCmd.Flags().StringVar(&flagTemplatesDir, "templates-dir", "",
		"read templates from disk instead of the embedded copies (dev mode)")
	rootCmd.AddCommand(serveCmd)
}
