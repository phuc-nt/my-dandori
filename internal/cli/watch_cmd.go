package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "One-shot transcript sweep: capture sessions that bypassed hooks",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		ing := &capture.Ingestor{Cfg: cfg, St: st}
		n, err := ing.WatchOnce(cfg.ProjectsDir)
		if err != nil {
			return err
		}
		fmt.Printf("watcher: %d transcript(s) processed\n", n)
		return nil
	},
}

// watcherWorker runs the same sweep on a ticker inside `dandori serve`.
func watcherWorker(ctx context.Context, cfg *config.Config, st *store.Store) {
	ing := &capture.Ingestor{Cfg: cfg, St: st}
	interval := time.Duration(cfg.WatchIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := ing.WatchOnce(cfg.ProjectsDir); err != nil {
				fmt.Println("watcher:", err)
			}
		}
	}
}

func init() {
	rootCmd.AddCommand(watchCmd)
	registerWorker(watcherWorker)
}
