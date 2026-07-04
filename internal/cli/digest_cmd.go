package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/integrations/gws"
	"github.com/phuc-nt/dandori/internal/integrations/slack"
	"github.com/phuc-nt/dandori/internal/learn"
)

var flagDigestDays int

// digestCmd is UG2b: send the fleet-health digest to Slack + Gmail.
// Recipients/channel come ONLY from config (digest_recipients,
// integrations.slack_channel) — there is deliberately no --to flag (C2).
var digestCmd = &cobra.Command{
	Use:   "digest",
	Short: "Send the fleet-health digest to Slack + Gmail (destinations are config-only, no --to flag)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		days := flagDigestDays
		if days <= 0 {
			days = cfg.LearnWindowDays
		}
		data, err := learn.BuildDigestData(st, days)
		if err != nil {
			return err
		}
		guard := &integrations.Guard{Cfg: cfg, St: st}
		i := cfg.Integrations
		pub := &integrations.DigestPublisher{
			St: st, Guard: guard, Cfg: cfg,
			Slack: slack.New(i.SlackXoxc, i.SlackXoxd),
			GWS:   gws.NewRunner(guard),
			From:  "me",
		}
		slackRes, gmailRes, err := pub.Send(context.Background(), data)
		if err != nil {
			return err
		}
		fmt.Printf("slack=%s gmail=%s\n", slackRes, gmailRes)
		return nil
	},
}

func init() {
	digestCmd.Flags().IntVar(&flagDigestDays, "days", 0, "window in days (default: config learn_window_days)")
	rootCmd.AddCommand(digestCmd)
}
