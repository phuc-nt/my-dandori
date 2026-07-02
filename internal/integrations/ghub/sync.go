// Package ghub imports GitHub PRs into the unified schema via the gh CLI
// (keyring auth — no token in .env). Best effort: missing gh → skip.
// Revert PRs are resolved back to their original PR so learn can compute
// AI-CFR (change failure rate).
package ghub

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/phuc-nt/dandori/internal/store"
)

// PR is the payload stored in work_items for github rows.
type PR struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	State      string `json:"state"`
	Author     string `json:"author"`
	CreatedAt  string `json:"createdAt"`
	MergedAt   string `json:"mergedAt"`
	RevertOf   int    `json:"revert_of,omitempty"`   // this PR reverts #N
	RevertedBy int    `json:"reverted_by,omitempty"` // this PR was reverted by #N
}

type ghPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	CreatedAt string `json:"createdAt"`
	MergedAt  string `json:"mergedAt"`
	Body      string `json:"body"`
}

var revertBodyRe = regexp.MustCompile(`[Rr]everts ([\w.-]+/[\w.-]+)?#(\d+)`)

// SyncPRs upserts a repo's recent PRs (all states) into work_items and
// resolves revert relationships. Returns the number of PRs upserted.
func SyncPRs(st *store.Store, repo string) (int, error) {
	if repo == "" {
		return 0, nil
	}
	cmd := exec.Command("gh", "pr", "list", "--repo", repo, "--state", "all", "--limit", "100",
		"--json", "number,title,state,author,createdAt,mergedAt,body")
	out, err := cmd.Output()
	if err != nil {
		return 0, err // gh missing or unauthenticated — caller logs and moves on
	}
	var raw []ghPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return 0, err
	}

	// Pass 1: build PR map. Pass 2: resolve reverts.
	prs := make(map[int]*PR, len(raw))
	for _, p := range raw {
		prs[p.Number] = &PR{Number: p.Number, Title: p.Title, State: p.State,
			Author: p.Author.Login, CreatedAt: p.CreatedAt, MergedAt: p.MergedAt}
	}
	for _, p := range raw {
		if p.MergedAt == "" {
			continue // only a MERGED revert marks the original as failed
		}
		target := revertTarget(p.Body, repo)
		if target == 0 || prs[target] == nil {
			continue
		}
		prs[p.Number].RevertOf = target
		prs[target].RevertedBy = p.Number
	}

	for _, p := range prs {
		payload, _ := json.Marshal(p)
		key := repo + "#" + strconv.Itoa(p.Number)
		if _, err := st.DB.Exec(`INSERT INTO work_items(source, key, title, status, assignee, project, updated_at, payload)
			VALUES('github', ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source, key) DO UPDATE SET
				title = excluded.title, status = excluded.status, updated_at = excluded.updated_at,
				payload = excluded.payload`,
			key, p.Title, p.State, p.Author, repo, nonEmpty(p.MergedAt, p.CreatedAt), string(payload)); err != nil {
			return 0, err
		}
	}
	return len(prs), nil
}

// revertTarget extracts the original PR number from a revert PR's body.
// Cross-repo references ("Reverts other/repo#5") are ignored — they don't
// point at a PR in this repo. GitHub's auto-title carries no number, so the
// body reference is the only reliable signal.
func revertTarget(body, repo string) int {
	m := revertBodyRe.FindStringSubmatch(body)
	if m == nil {
		return 0
	}
	if ref := m[1]; ref != "" && !strings.EqualFold(ref, repo) {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
