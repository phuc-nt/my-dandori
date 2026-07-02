package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/store"
)

// demoSeedCmd fills the DB with sample data for development/screenshots.
// Hidden: not part of the operator workflow; safe to delete the DB after.
var demoSeedCmd = &cobra.Command{
	Use:    "demo-seed",
	Hidden: true,
	Short:  "Insert sample agents/runs/events for development",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		ts := func(d int) string { return time.Now().UTC().AddDate(0, 0, -d).Format(time.RFC3339) }
		agents := []string{"claude-backend", "claude-frontend", "claude-docs", "cursor-infra", "claude-qa"}
		for _, a := range agents {
			st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?, ?, ?) ON CONFLICT(name) DO NOTHING`, a, a, ts(30))
		}
		type r struct {
			id, agent, status, task string
			daysAgo                 int
			cost                    float64
		}
		runs := []r{
			{"demo-r1", "claude-backend", "done", "SCRUM-1", 1, 2.10},
			{"demo-r2", "claude-backend", "done", "SCRUM-2", 2, 1.40},
			{"demo-r3", "claude-frontend", "done", "", 1, 0.90},
			{"demo-r4", "claude-frontend", "failed", "SCRUM-3", 3, 3.20},
			{"demo-r5", "claude-docs", "done", "", 5, 0.30},
			{"demo-r6", "cursor-infra", "killed", "", 2, 4.80},
			{"demo-r7", "claude-qa", "done", "SCRUM-4", 1, 1.10},
			{"demo-r8", "claude-qa", "running", "", 0, 0.55},
		}
		for _, x := range runs {
			st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, task_key, status, started_at, ended_at, cost_usd, model)
				VALUES(?, ?, ?, 'demo-project', ?, ?, ?, ?, ?, 'claude-sonnet-5') ON CONFLICT(session_id) DO NOTHING`,
				x.id, x.id, x.agent, x.task, x.status, ts(x.daysAgo), ts(x.daysAgo), x.cost)
		}
		ev := func(run, kind, tool string, ok int, payload string) {
			st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload) VALUES(?, ?, ?, ?, ?, ?)`,
				run, store.Now(), kind, tool, ok, payload)
		}
		ev("demo-r1", "tool_use", "Edit", 1, `{"file_path":"api/users.go"}`)
		ev("demo-r1", "tool_result", "Edit", 1, "ok")
		ev("demo-r4", "tool_result", "Bash", 0, "test failure")
		ev("demo-r4", "guardrail_block", "Bash", 0, "tool=Bash verdict=deny reason=[demo]")
		ev("demo-r6", "kill", "", 0, "runaway loop")
		ev("demo-r8", "permission_ask", "Bash", 0, "git push gated")
		st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('demo-r8','git push origin main','pushing code needs approval', ?)`, store.Now())
		st.DB.Exec(`INSERT INTO flags(run_id, reason, created_at) VALUES('demo-r4','quality gate failed', ?)`, store.Now())
		st.DB.Exec(`INSERT INTO work_items(source, key, title, status, updated_at) VALUES('jira','SCRUM-1','demo issue','Done',?) ON CONFLICT(source,key) DO NOTHING`, store.Now())
		fmt.Println("demo data seeded")
		return nil
	},
}

func init() { rootCmd.AddCommand(demoSeedCmd) }
