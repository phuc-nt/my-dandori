package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/learn"
)

// statsCmd is the terminal twin of the dashboards (UH2) — same learn engine.
var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Fleet stats: runs, cost, grades (same engine as the web console)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		rows, err := learn.Leaderboard(st, cfg.LearnWindowDays)
		if err != nil {
			return err
		}
		var cost float64
		var runs int
		for _, r := range rows {
			cost += r.CostUSD
			runs += r.Runs
		}
		fmt.Printf("window: %dd · agents: %d · runs: %d · cost: $%.2f\n\n",
			cfg.LearnWindowDays, len(rows), runs, cost)
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "AGENT\tGRADE\tCOMPOSITE\tACCEPT\tSUCCESS\tAUTONOMY\tRELIAB\tRUNS\tCOST\tUSEFUL%")
		for _, r := range rows {
			grade := r.Grade.Letter
			if r.Grade.Uncalibrated {
				grade += "*"
			}
			fmt.Fprintf(w, "%s\t%s\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%d\t$%.2f\t%.0f%%\n",
				r.AgentName, grade, r.Composite,
				r.Metrics.Acceptance.Value, r.Metrics.Success.Value,
				r.Metrics.Autonomy.Value, r.Metrics.Reliability.Value,
				r.Runs, r.CostUSD, r.ROI.UsefulPct)
		}
		w.Flush()
		fmt.Println("\n(* = uncalibrated: fleet < 5, static bands)")
		return nil
	},
}

// leaderboardCmd is an alias view sorted the same way as /dash/org.
var leaderboardCmd = &cobra.Command{
	Use:   "leaderboard",
	Short: "Cross-fleet leaderboard (alias of stats)",
	RunE:  statsCmd.RunE,
}

func init() {
	rootCmd.AddCommand(statsCmd)
	rootCmd.AddCommand(leaderboardCmd)
}
