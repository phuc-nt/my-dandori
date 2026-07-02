package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/integrations/ghub"
	"github.com/phuc-nt/dandori/internal/integrations/jira"
)

var flagSyncDir string

var syncCmd = &cobra.Command{
	Use:       "sync [jira|github|reverts]",
	Short:     "Pull external signals: work items (jira/github) or local git reverts",
	Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	ValidArgs: []string{"jira", "github", "reverts"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		switch args[0] {
		case "jira":
			i := cfg.Integrations
			if i.AtlassianSite == "" || i.AtlassianToken == "" {
				return fmt.Errorf("jira: ATLASSIAN_SITE_NAME / ATLASSIAN_USER_EMAIL / ATLASSIAN_API_TOKEN not set")
			}
			c := jira.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken)
			n, err := jira.SyncIssues(st, c, i.JiraProject)
			if err != nil {
				return err
			}
			fmt.Printf("jira: %d issues from %s upserted into work_items\n", n, i.JiraProject)
		case "github":
			repo := cfg.Integrations.GithubRepo
			if repo == "" {
				return fmt.Errorf("github: set integrations.github_repo in config")
			}
			n, err := ghub.SyncPRs(st, repo)
			if err != nil {
				return fmt.Errorf("github (gh CLI): %w", err)
			}
			fmt.Printf("github: %d PRs from %s upserted into work_items\n", n, repo)
		case "reverts":
			dir := flagSyncDir
			if dir == "" {
				dir, _ = os.Getwd()
			}
			ing := &capture.Ingestor{Cfg: cfg, St: st}
			n, err := ing.ScanReverts(dir)
			if err != nil {
				return err
			}
			fmt.Printf("reverts: %d new revert detection(s) recorded from %s\n", n, dir)
		}
		return nil
	},
}

func init() {
	syncCmd.Flags().StringVar(&flagSyncDir, "dir", "", "repo directory for revert scan (default CWD)")
	rootCmd.AddCommand(syncCmd)
}
