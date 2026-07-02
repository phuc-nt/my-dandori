// Package ghub imports GitHub PRs into the unified schema via the gh CLI
// (keyring auth — no token in .env). Best effort: missing gh → skip.
package ghub

import (
	"encoding/json"
	"os/exec"
	"strconv"

	"github.com/phuc-nt/dandori/internal/store"
)

type pr struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	UpdatedAt string `json:"updatedAt"`
}

// SyncPRs upserts a repo's recent PRs into work_items (source=github).
func SyncPRs(st *store.Store, repo string) (int, error) {
	if repo == "" {
		return 0, nil
	}
	ctxCmd := exec.Command("gh", "pr", "list", "--repo", repo, "--state", "all", "--limit", "50",
		"--json", "number,title,state,author,updatedAt")
	out, err := ctxCmd.Output()
	if err != nil {
		return 0, err // gh missing or unauthenticated — caller logs and moves on
	}
	var prs []pr
	if err := json.Unmarshal(out, &prs); err != nil {
		return 0, err
	}
	for _, p := range prs {
		key := repo + "#" + strconv.Itoa(p.Number)
		if _, err := st.DB.Exec(`INSERT INTO work_items(source, key, title, status, assignee, project, updated_at)
			VALUES('github', ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source, key) DO UPDATE SET
				title = excluded.title, status = excluded.status, updated_at = excluded.updated_at`,
			key, p.Title, p.State, p.Author.Login, repo, p.UpdatedAt); err != nil {
			return 0, err
		}
	}
	return len(prs), nil
}
