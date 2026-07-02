package learn

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

const (
	checkTimeout = 5 * time.Minute
	outputCap    = 8192
)

// GateResult is the outcome of one quality-gate check command.
type GateResult struct {
	Check  string
	OK     bool
	Output string
}

// RunChecks executes the configured check commands (G7 — the independent
// quality gate: the agent that wrote the code is not the one grading it).
// Checks come from local config only, never from the web UI. A failing check
// records a gate_result and opens a flag on the run.
func RunChecks(st *store.Store, runID, dir string, checks []string) ([]GateResult, error) {
	results := make([]GateResult, 0, len(checks))
	anyFail := false
	for _, check := range checks {
		res := runCheck(dir, check)
		results = append(results, res)
		ok := 0
		if res.OK {
			ok = 1
		}
		if _, err := st.DB.Exec(`INSERT INTO gate_results(run_id, check_name, ok, output, ts)
			VALUES(?, ?, ?, ?, ?)`, runID, res.Check, ok, res.Output, store.Now()); err != nil {
			return results, err
		}
		if !res.OK {
			anyFail = true
		}
	}
	if anyFail && runID != "" {
		_, err := st.DB.Exec(`INSERT INTO flags(run_id, reason, created_at)
			VALUES(?, ?, ?)`, runID, "quality gate failed: see gate_results", store.Now())
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func runCheck(dir, check string) GateResult {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", check)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if len(out) > outputCap {
		out = out[:outputCap]
	}
	res := GateResult{Check: check, OK: err == nil, Output: string(out)}
	if err != nil && ctx.Err() != nil {
		res.Output += fmt.Sprintf("\n(check timed out after %s)", checkTimeout)
	}
	return res
}
