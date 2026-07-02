package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// teamCmd manages the team ↔ member mapping (operators and agents). The
// executive UI reads these to compare team effectiveness.
var teamCmd = &cobra.Command{
	Use:   "team",
	Short: "Manage teams: group operators and agents for team-level analytics",
}

var teamAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Create a team",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		id, err := st.CreateTeam(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("team #%d %q\n", id, args[0])
		return nil
	},
}

var teamAssignCmd = &cobra.Command{
	Use:   "assign <team-name>",
	Short: "Assign an --operator or --agent to a team",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if (flagTeamOperator == "") == (flagTeamAgent == "") {
			return fmt.Errorf("exactly one of --operator or --agent is required")
		}
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		id, err := st.CreateTeam(args[0]) // idempotent by name
		if err != nil {
			return err
		}
		mtype, mid := "operator", flagTeamOperator
		if flagTeamAgent != "" {
			mtype, mid = "agent", flagTeamAgent
		}
		if err := st.AssignMember(id, mtype, mid); err != nil {
			return fmt.Errorf("assign %s %q: not found (operators appear after their first run; agents after first capture)", mtype, mid)
		}
		fmt.Printf("%s %q → team %q\n", mtype, mid, args[0])
		return nil
	},
}

var teamListCmd = &cobra.Command{
	Use:   "list",
	Short: "List teams and their members",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		teams, err := st.ListTeams()
		if err != nil {
			return err
		}
		if len(teams) == 0 {
			fmt.Println("no teams — create one with `dandori team add <name>`")
			return nil
		}
		for _, t := range teams {
			fmt.Printf("• %s (#%d)\n", t.Name, t.ID)
			members, err := st.TeamMembers(t.ID)
			if err != nil {
				return err
			}
			for _, m := range members {
				fmt.Printf("    %-8s %s\n", m.Type, m.ID)
			}
		}
		return nil
	},
}

var (
	flagTeamOperator string
	flagTeamAgent    string
)

func init() {
	teamAssignCmd.Flags().StringVar(&flagTeamOperator, "operator", "", "operator id (username@hostname)")
	teamAssignCmd.Flags().StringVar(&flagTeamAgent, "agent", "", "agent id")
	teamCmd.AddCommand(teamAddCmd, teamAssignCmd, teamListCmd)
	rootCmd.AddCommand(teamCmd)
}
