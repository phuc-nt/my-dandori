package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/integrations/confluence"
)

var (
	flagReportSpaceID string
	flagReportParent  string
)

var reportCmd = &cobra.Command{
	Use:       "report [confluence]",
	Short:     "Publish the fleet report (Confluence; respects DRY_RUN)",
	Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	ValidArgs: []string{"confluence"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		i := cfg.Integrations
		if i.AtlassianSite == "" || i.AtlassianToken == "" {
			return fmt.Errorf("confluence: ATLASSIAN_* credentials not set")
		}
		spaceID := flagReportSpaceID
		if spaceID == "" {
			spaceID = i.ConfluenceSpaceID
		}
		if spaceID == "" {
			return fmt.Errorf("confluence: set --space-id or CONFLUENCE_SPACE_ID")
		}
		r := &confluence.Reporter{
			St:      st,
			Client:  confluence.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken),
			Guard:   &integrations.Guard{Cfg: cfg, St: st},
			SpaceID: spaceID, Parent: flagReportParent, Window: cfg.LearnWindowDays,
		}
		pageID, err := r.Post(cfg.UserName)
		if err != nil {
			return err
		}
		switch pageID {
		case "":
			fmt.Println("report already posted today (dedup) — nothing to do")
		case "dry-run":
			fmt.Println("[DRY_RUN] report built but not posted (set DRY_RUN=false to publish)")
		default:
			fmt.Printf("fleet report posted: %s/wiki/pages/viewpage.action?pageId=%s\n",
				"https://"+i.AtlassianSite, pageID)
		}
		return nil
	},
}

var contextCmd = &cobra.Command{
	Use:   "context show --confluence PAGE_ID",
	Short: "Print a Confluence page as plain text (operator context)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != "show" {
			return fmt.Errorf("usage: dandori context show --confluence PAGE_ID")
		}
		if flagContextConfluence == "" {
			return fmt.Errorf("--confluence PAGE_ID required")
		}
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		i := cfg.Integrations
		c := confluence.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken)
		title, text, err := c.GetPageText(flagContextConfluence)
		if err != nil {
			return err
		}
		fmt.Printf("# %s\n\n%s\n", title, text)
		return nil
	},
}

var flagContextConfluence string

func init() {
	reportCmd.Flags().StringVar(&flagReportSpaceID, "space-id", "", "Confluence space id (default CONFLUENCE_SPACE_ID)")
	reportCmd.Flags().StringVar(&flagReportParent, "parent", "", "parent page id (optional)")
	contextCmd.Flags().StringVar(&flagContextConfluence, "confluence", "", "page id to fetch")
	rootCmd.AddCommand(reportCmd, contextCmd)
}
