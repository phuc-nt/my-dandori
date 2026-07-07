package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/learn"
)

var knowledgeCmd = &cobra.Command{
	Use:   "knowledge",
	Short: "Knowledge Units pipeline: detect candidates from captured fleet data",
}

var knowledgeDetectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Run all detectors and nominate qualifying candidates (review at /knowledge)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		nominated, skipped, err := learn.DetectKnowledgeUnits(st, cfg.LearnWindowDays)
		if err != nil {
			return err
		}
		fmt.Printf("nominated %d candidate(s), skipped %d (already pending or below sample gate)\n", nominated, skipped)
		if nominated > 0 {
			fmt.Println("review tại /knowledge")
		}
		return nil
	},
}

func init() {
	knowledgeCmd.AddCommand(knowledgeDetectCmd)
	rootCmd.AddCommand(knowledgeCmd)
}
