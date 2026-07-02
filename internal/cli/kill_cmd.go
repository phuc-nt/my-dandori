package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/govern"
)

var (
	flagKillAll bool
	flagKillOff bool
)

// contextOf gives subcommands a base context (kept simple on purpose).
func contextOf() context.Context { return context.Background() }

var killCmd = &cobra.Command{
	Use:   "kill [session-id]",
	Short: "Kill switch: stop one run, or --all for the global switch (--off to lift)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		switch {
		case flagKillOff:
			if err := govern.SetGlobalKill(st, false, cfg.UserName, "kill switch lifted via CLI"); err != nil {
				return err
			}
			fmt.Println("global kill switch OFF")
		case flagKillAll:
			if err := govern.SetGlobalKill(st, true, cfg.UserName, "kill switch engaged via CLI"); err != nil {
				return err
			}
			fmt.Println("global kill switch ON — all agent tool calls will be denied")
		case len(args) == 1:
			if err := govern.KillRun(st, args[0], cfg.UserName, "killed via CLI"); err != nil {
				return err
			}
			fmt.Printf("run %s killed\n", args[0])
		default:
			return fmt.Errorf("specify a session id, --all, or --off")
		}
		return nil
	},
}

func init() {
	killCmd.Flags().BoolVar(&flagKillAll, "all", false, "engage the global kill switch")
	killCmd.Flags().BoolVar(&flagKillOff, "off", false, "lift the global kill switch")
	rootCmd.AddCommand(killCmd)
}
