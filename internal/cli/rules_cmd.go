package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/govern"
)

var (
	flagSimPattern string
	flagSimScope   string
	flagSimTarget  string
	flagSimDays    int
)

var rulesCmd = &cobra.Command{
	Use:   "rules [simulate]",
	Short: "Guardrail rule tools (simulate a candidate rule against history)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != "simulate" {
			return fmt.Errorf("usage: dandori rules simulate --pattern '...'")
		}
		if flagSimPattern == "" {
			return fmt.Errorf("--pattern required")
		}
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		res, err := govern.Simulate(st, flagSimPattern, flagSimScope, flagSimTarget, flagSimDays)
		if err != nil {
			return err
		}
		fmt.Printf("would have fired %d time(s) across %d tool calls in the last %d days\n",
			res.Hits, res.Total, flagSimDays)
		if len(res.Samples) > 0 {
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "RUN\tAGENT\tTOOL\tMATCHED")
			for _, s := range res.Samples {
				run := s.RunID
				if len(run) > 12 {
					run = run[:12]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", run, s.Agent, s.Tool, s.Excerpt)
			}
			w.Flush()
		}
		return nil
	},
}

func init() {
	rulesCmd.Flags().StringVar(&flagSimPattern, "pattern", "", "candidate rule regex")
	rulesCmd.Flags().StringVar(&flagSimScope, "scope", "global", "global | agent | project")
	rulesCmd.Flags().StringVar(&flagSimTarget, "target", "", "scope target")
	rulesCmd.Flags().IntVar(&flagSimDays, "days", 30, "history window")
	rootCmd.AddCommand(rulesCmd)
}
