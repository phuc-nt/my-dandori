package cli

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/store"
)

var loopCmd = &cobra.Command{
	Use:   "loop [run]",
	Short: "Run one closed-loop governance cycle (grade → flag → Jira → band action)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		res, err := govern.RunClosedLoop(st, cfg, jiraFlagSink(cfg, st))
		if err != nil {
			return err
		}
		fmt.Printf("closed loop: %d flagged · %d auto-demoted · %d proposals opened · %d proposals applied · %d recovered\n",
			res.Flagged, res.Demoted, res.Proposed, res.Applied, res.Resolved)
		for _, d := range res.Details {
			fmt.Println("  -", d)
		}
		return nil
	},
}

// jiraFlagSink builds the flag→Jira leg (DRY_RUN-guarded inside flagToJira).
func jiraFlagSink(cfg *config.Config, st *store.Store) func(int64) {
	return func(flagID int64) {
		defer recoverLog("closed-loop flag sink")
		if err := flagToJira(cfg, st, flagID); err != nil {
			log.Println("closed-loop flag→jira:", err)
		}
	}
}

// closedLoopWorker runs the governance cycle on startup and every 6 hours.
func closedLoopWorker(ctx context.Context, cfg *config.Config, st *store.Store) {
	run := func() {
		defer recoverLog("closed loop")
		res, err := govern.RunClosedLoop(st, cfg, jiraFlagSink(cfg, st))
		if err != nil {
			log.Println("closed loop:", err)
			return
		}
		if res.Flagged+res.Demoted+res.Proposed+res.Applied+res.Resolved > 0 {
			log.Printf("closed loop: flagged=%d demoted=%d proposed=%d applied=%d recovered=%d",
				res.Flagged, res.Demoted, res.Proposed, res.Applied, res.Resolved)
		}
	}
	run()
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

func init() {
	rootCmd.AddCommand(loopCmd)
	registerWorker(closedLoopWorker)
}
