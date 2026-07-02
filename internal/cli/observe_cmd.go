package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/observer"
	"github.com/phuc-nt/dandori/internal/store"
)

var observeCmd = &cobra.Command{
	Use:   "observe",
	Short: "Master Observer: detect fleet insights and apply approved actions",
}

var observeRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run one observer cycle (applier → detectors)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		res, err := observer.RunObserver(st, cfg)
		if err != nil {
			return err
		}
		fmt.Printf("observer: surfaced=%d proposed=%d applied=%d deduped=%d\n",
			res.Surfaced, res.Proposed, res.Applied, res.Deduped)
		for _, d := range res.Details {
			fmt.Println("  •", d)
		}
		return nil
	},
}

// observerWorker ticks the observer inside serve. INTERNAL effects only —
// external publishing is the flywheel's job, with its own human review.
func observerWorker(ctx context.Context, cfg *config.Config, st *store.Store) {
	tick := time.NewTicker(30 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			func() {
				// A panic in any detector or the applier must not take down
				// serve (console + ingest listener) — recover like every peer.
				defer recoverLog("observer worker")
				if _, err := observer.RunObserver(st, cfg); err != nil {
					fmt.Println("observer worker:", err)
				}
			}()
		}
	}
}

func init() {
	observeCmd.AddCommand(observeRunCmd)
	rootCmd.AddCommand(observeCmd)
	registerWorker(observerWorker)
}
