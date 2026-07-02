package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/integrations/ghub"
	"github.com/phuc-nt/dandori/internal/integrations/jira"
)

var syncCmd = &cobra.Command{
	Use:       "sync [jira|github]",
	Short:     "Pull external work items into the unified schema (read-only)",
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"jira", "github"},
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
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
