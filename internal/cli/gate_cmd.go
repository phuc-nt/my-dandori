package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/learn"
)

var (
	flagGateSession string
	flagGateChecks  string
	flagGateDir     string
)

var gateCmd = &cobra.Command{
	Use:   "gate [run]",
	Short: "Quality gate (G7): run configured checks independently of the agent",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		checks := cfg.GateChecks
		if flagGateChecks != "" {
			checks = splitChecks(flagGateChecks)
		}
		results, err := learn.RunChecks(st, flagGateSession, flagGateDir, checks)
		if err != nil {
			return err
		}
		failed := 0
		for _, r := range results {
			mark := "PASS"
			if !r.OK {
				mark = "FAIL"
				failed++
			}
			fmt.Printf("[%s] %s\n", mark, r.Check)
			if !r.OK && r.Output != "" {
				fmt.Println(indent(r.Output))
			}
		}
		if failed > 0 {
			return fmt.Errorf("quality gate: %d/%d checks failed", failed, len(results))
		}
		fmt.Println("quality gate: all checks passed")
		return nil
	},
}

func splitChecks(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = "    " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func init() {
	gateCmd.Flags().StringVar(&flagGateSession, "session", "", "run/session id to attach results and flags to")
	gateCmd.Flags().StringVar(&flagGateChecks, "checks", "", "comma-separated check commands (default from config gate_checks)")
	gateCmd.Flags().StringVar(&flagGateDir, "dir", "", "directory to run checks in (default CWD)")
	rootCmd.AddCommand(gateCmd)
}
