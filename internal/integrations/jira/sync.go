package jira

import (
	"github.com/phuc-nt/dandori/internal/store"
)

// SyncIssues upserts a project's issues into work_items (unified schema C4:
// human work and agent runs on the same table). Issues labeled "agent" are
// marked is_agent. Returns the number of issues upserted.
func SyncIssues(st *store.Store, c *Client, project string) (int, error) {
	issues, err := c.SearchIssues(project)
	if err != nil {
		return 0, err
	}
	for _, is := range issues {
		isAgent := 0
		for _, l := range is.Labels {
			if l == "agent" || l == "ai-agent" {
				isAgent = 1
			}
		}
		if _, err := st.DB.Exec(`INSERT INTO work_items(source, key, title, status, assignee, is_agent, project, updated_at)
			VALUES('jira', ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source, key) DO UPDATE SET
				title = excluded.title, status = excluded.status, assignee = excluded.assignee,
				is_agent = excluded.is_agent, updated_at = excluded.updated_at`,
			is.Key, is.Summary, is.Status, is.Assignee, isAgent, project, is.Updated); err != nil {
			return 0, err
		}
	}
	return len(issues), nil
}
