package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/integrations/confluence"
	"github.com/phuc-nt/dandori/internal/integrations/slack"
	"github.com/phuc-nt/dandori/internal/learn"
)

var flywheelCmd = &cobra.Command{
	Use:   "flywheel",
	Short: "Best-practice flywheel: detect patterns, promote playbooks, publish, track adoption",
}

var flywheelDetectCmd = &cobra.Command{
	Use:   "detect",
	Short: "List playbook candidates (clean, well-prompted runs not yet captured)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		cands, err := learn.DetectCandidates(st, cfg.LearnWindowDays)
		if err != nil {
			return err
		}
		if len(cands) == 0 {
			fmt.Println("no candidates — need clean runs with specific prompts in the window")
			return nil
		}
		for _, c := range cands {
			fmt.Printf("• run %s · %s · %s\n    %s\n", c.RunID, c.AgentName, c.TaskKey, c.Why)
		}
		fmt.Printf("\npromote with: dandori flywheel promote <run-id>\n")
		return nil
	},
}

var flywheelPromoteCmd = &cobra.Command{
	Use:   "promote <run-id>",
	Short: "Nominate a candidate run as a knowledge unit (playbook, pending review)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		cands, err := learn.DetectCandidates(st, cfg.LearnWindowDays)
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.RunID == args[0] {
				id, err := learn.PromoteCandidate(st, c, cfg.UserName)
				if err != nil {
					return err
				}
				fmt.Printf("nominated unit #%d — review tại /knowledge\n", id)
				return nil
			}
		}
		return fmt.Errorf("run %s is not a current candidate (already promoted, has errors, or out of window)", args[0])
	},
}

var flywheelPublishCmd = &cobra.Command{
	Use:   "publish <playbook-id>",
	Short: "Publish a playbook card to Slack + Confluence (DRY_RUN-guarded)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return err
		}
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		i := cfg.Integrations
		pub := &integrations.FlywheelPublisher{
			St:           st,
			Guard:        &integrations.Guard{Cfg: cfg, St: st},
			Slack:        slack.New(i.SlackXoxc, i.SlackXoxd),
			SlackChannel: i.SlackChannel,
			Confluence:   confluence.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken),
			SpaceID:      i.ConfluenceSpaceID,
		}
		s, c, err := pub.Publish(id, cfg.UserName)
		if err != nil {
			return err
		}
		fmt.Printf("slack=%s confluence=%s\n", s, c)
		return nil
	},
}

var flywheelAdoptionCmd = &cobra.Command{
	Use:   "adoption <playbook-id>",
	Short: "Show a playbook's adopters and before/after outcome (private)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return err
		}
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		if n, err := learn.ComputeAdoptionOutcomes(st); err == nil && n > 0 {
			fmt.Printf("(computed %d pending outcome(s))\n", n)
		}
		rows, err := learn.AdoptionReport(st, id)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			fmt.Println("no adoptions yet")
			return nil
		}
		fmt.Println("PRIVATE coaching data —", learn.OperatorCaveat)
		for _, r := range rows {
			line := fmt.Sprintf("• %s adopted %s", r.OperatorID, r.AdoptedAt)
			if r.Before != nil {
				line += fmt.Sprintf(" · before %.0f%%", *r.Before*100)
			}
			if r.After != nil {
				line += fmt.Sprintf(" · after %.0f%%", *r.After*100)
			}
			if r.Improved != nil {
				if *r.Improved {
					line += " · CẢI THIỆN"
				} else {
					line += " · chưa cải thiện"
				}
			}
			fmt.Println(line)
		}
		fmt.Println(learn.RegressionToMeanCaveat)
		return nil
	},
}

func init() {
	flywheelCmd.AddCommand(flywheelDetectCmd, flywheelPromoteCmd, flywheelPublishCmd, flywheelAdoptionCmd)
	rootCmd.AddCommand(flywheelCmd)
}
