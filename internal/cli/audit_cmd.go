package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/govern"
)

var auditCmd = &cobra.Command{
	Use:   "audit [verify|list]",
	Short: "Inspect the tamper-evident audit trail",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		sub := "list"
		if len(args) == 1 {
			sub = args[0]
		}
		if sub == "verify" {
			broken, err := govern.Verify(st)
			if err != nil {
				return err
			}
			if broken != 0 {
				return fmt.Errorf("audit chain BROKEN at entry #%d — records were modified", broken)
			}
			fmt.Println("audit chain OK")
			return nil
		}
		rows, err := st.DB.Query(`SELECT id, ts, actor, action, COALESCE(subject,''), COALESCE(detail,'')
			FROM audit_log ORDER BY id DESC LIMIT 50`)
		if err != nil {
			return err
		}
		defer rows.Close()
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tTS\tACTOR\tACTION\tSUBJECT\tDETAIL")
		for rows.Next() {
			var id int64
			var ts, actor, action, subject, detail string
			if err := rows.Scan(&id, &ts, &actor, &action, &subject, &detail); err != nil {
				return err
			}
			if len(detail) > 60 {
				detail = detail[:60] + "…"
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", id, ts, actor, action, subject, detail)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(auditCmd)
}
