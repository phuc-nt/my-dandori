package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/govern"
)

var (
	flagRunsStatus string
	flagRunsLimit  int
)

var runsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List captured runs",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		q := `SELECT id, COALESCE(agent_id,''), COALESCE(project,''), COALESCE(task_key,''),
			status, cost_usd, COALESCE(started_at,''), source FROM runs`
		qargs := []any{}
		if flagRunsStatus != "" {
			q += ` WHERE status = ?`
			qargs = append(qargs, flagRunsStatus)
		}
		q += ` ORDER BY started_at DESC LIMIT ?`
		qargs = append(qargs, flagRunsLimit)
		rows, err := st.DB.Query(q, qargs...)
		if err != nil {
			return err
		}
		defer rows.Close()
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "RUN\tAGENT\tPROJECT\tTASK\tSTATUS\tCOST\tSTARTED\tSRC")
		for rows.Next() {
			var id, agent, project, task, status, started, source string
			var cost float64
			if err := rows.Scan(&id, &agent, &project, &task, &status, &cost, &started, &source); err != nil {
				return err
			}
			if len(id) > 12 {
				id = id[:12]
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t$%.3f\t%s\t%s\n", id, agent, project, task, status, cost, started, source)
		}
		w.Flush()
		return rows.Err()
	},
}

var budgetCmd = &cobra.Command{
	Use:   "budget",
	Short: "Show budget scopes and current-month spend",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		eng := govern.NewEngine(cfg, st)
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "SCOPE\tTARGET\tSPEND\tLIMIT\tUSED")
		printScope := func(scopeType, scopeID string, limit float64) {
			spend, _ := eng.SpendMonth(scopeType, scopeID)
			pct := 0.0
			if limit > 0 {
				pct = spend / limit * 100
			}
			target := scopeID
			if target == "" {
				target = "—"
			}
			fmt.Fprintf(w, "%s\t%s\t$%.2f\t$%.2f\t%.0f%%\n", scopeType, target, spend, limit, pct)
		}
		rows, err := st.DB.Query(`SELECT scope_type, scope_id, limit_usd FROM budgets ORDER BY scope_type`)
		if err != nil {
			return err
		}
		defer rows.Close()
		haveGlobal := false
		for rows.Next() {
			var t, id string
			var limit float64
			if err := rows.Scan(&t, &id, &limit); err != nil {
				return err
			}
			if t == "global" {
				haveGlobal = true
			}
			printScope(t, id, limit)
		}
		if !haveGlobal {
			printScope("global", "", cfg.Budget.GlobalMonthlyUSD)
		}
		return w.Flush()
	},
}

var flagApprovalsPending bool

var approvalsCmd = &cobra.Command{
	Use:   "approvals",
	Short: "List approvals (--pending for the open queue)",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		q := `SELECT id, COALESCE(run_id,''), action, status, COALESCE(decided_by,''), COALESCE(decision_note,''), requested_at FROM approvals`
		if flagApprovalsPending {
			q += ` WHERE status = 'pending'`
		}
		q += ` ORDER BY id DESC LIMIT 50`
		rows, err := st.DB.Query(q)
		if err != nil {
			return err
		}
		defer rows.Close()
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tRUN\tACTION\tSTATUS\tBY\tNOTE\tREQUESTED")
		for rows.Next() {
			var id int64
			var run, action, status, by, note, at string
			if err := rows.Scan(&id, &run, &action, &status, &by, &note, &at); err != nil {
				return err
			}
			if len(run) > 12 {
				run = run[:12]
			}
			if len(action) > 40 {
				action = action[:40] + "…"
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", id, run, action, status, by, note, at)
		}
		w.Flush()
		return rows.Err()
	},
}

func init() {
	runsCmd.Flags().StringVar(&flagRunsStatus, "status", "", "filter by status")
	runsCmd.Flags().IntVarP(&flagRunsLimit, "limit", "n", 20, "max rows")
	approvalsCmd.Flags().BoolVar(&flagApprovalsPending, "pending", false, "only pending")
	rootCmd.AddCommand(runsCmd, budgetCmd, approvalsCmd)
}
