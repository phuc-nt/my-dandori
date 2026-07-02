package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/learn"
)

var attributionCmd = &cobra.Command{
	Use:   "attribution",
	Short: "Code volume per agent (git deltas per run) and how much got reverted",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		rows, err := learn.Attribution(st, cfg.LearnWindowDays)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "AGENT\tRUNS\t+LINES\t-LINES\tREVERTED RUNS")
		for _, r := range rows {
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\n", r.AgentID, r.Runs, r.LinesAdded, r.LinesDeleted, r.RevertedRuns)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(attributionCmd)
}
