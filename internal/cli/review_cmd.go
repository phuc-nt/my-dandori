package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/learn"
)

var reviewCmd = &cobra.Command{
	Use:   "review <agent>",
	Short: "AI-generated performance review of an agent (cached weekly)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		if cfg.OpenRouterKey == "" {
			return fmt.Errorf("OPENROUTER_API_KEY not set")
		}
		reviewer := learn.NewAIReviewer(st, cfg.OpenRouterKey, cfg.OpenRouterModel)
		text := reviewer.Review(args[0], cfg.LearnWindowDays)
		if text == "" {
			return fmt.Errorf("no review available (agent has no runs in window, or upstream error)")
		}
		fmt.Println(text)
		fmt.Println("\n[AI-generated — verify numbers at /provenance]")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(reviewCmd)
}
