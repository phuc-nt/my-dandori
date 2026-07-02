package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/govern"
)

var bandCmd = &cobra.Command{
	Use:   "band <agent> [supervised|gated|trusted]",
	Short: "Show or set an agent's autonomy band (grades with consequences)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		agent := args[0]
		if len(args) == 1 {
			fmt.Printf("%s: %s\n", agent, govern.BandFor(st, agent))
			return nil
		}
		if err := govern.SetBand(st, agent, args[1], cfg.UserName, "set via CLI"); err != nil {
			return err
		}
		fmt.Printf("%s → %s\n", agent, args[1])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(bandCmd)
}
