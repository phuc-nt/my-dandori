package learn

import (
	"strconv"

	"github.com/phuc-nt/dandori/internal/store"
)

// The 4 on-demand mining signal queries (split from knowledge_mining.go for
// file-size — this half is pure SQL-sketch-to-Go, no merge/rank logic).

// ---------------------------------------------------------------------------
// Signal (i): corrective steering ≥ miningCorrectiveMin
// ---------------------------------------------------------------------------

// miningCorrectiveSteering pulls steering_msg rows on done runs and classifies
// each with the existing classify() keyword heuristic (steering_econ.go:203)
// — the only signal needing Go post-processing, since classify() rules
// cannot be expressed in SQL.
func miningCorrectiveSteering(st *store.Store, days int) (map[string]MiningSignal, error) {
	rows, err := st.Read().Query(`
		SELECT e.run_id, e.payload
		FROM events e JOIN runs r ON r.id = e.run_id
		WHERE e.kind = 'steering_msg' AND r.status = 'done' AND r.ended_at IS NOT NULL
		` + insightWindowClauseCol("r.started_at", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var runID, payload string
		if err := rows.Scan(&runID, &payload); err != nil {
			return nil, err
		}
		if classify(payload) == "corrective" {
			counts[runID]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := map[string]MiningSignal{}
	for runID, n := range counts {
		if n < miningCorrectiveMin {
			continue
		}
		out[runID] = MiningSignal{
			Kind:      "corrective",
			Evidence:  formatEvidenceN(n, "corrective steering"),
			Heuristic: true,
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Signal (ii): fail-retry-success (recursive CTE)
// ---------------------------------------------------------------------------

// miningFailRetrySuccess finds the done run at the tail of a retry chain that
// has a failed/killed ancestor somewhere in the same lineage. M1: the CTE
// includes an orphan-root union (retry_of IS NULL OR retry_of NOT IN runs —
// keeps a lineage whose parent row is gone from vanishing entirely) and a
// depth<64 cycle guard (bounds any accidental cyclic retry_of; not
// creatable today per handlers_launch.go:107, but this is a robustness
// contract, not a clean-data assumption).
func miningFailRetrySuccess(st *store.Store, days int) (map[string]MiningSignal, error) {
	rows, err := st.Read().Query(`
		WITH RECURSIVE chain(id, root, depth) AS (
		  SELECT id, id, 0 FROM runs
		    WHERE retry_of IS NULL OR retry_of NOT IN (SELECT id FROM runs)
		  UNION ALL
		  SELECT r.id, c.root, c.depth+1 FROM runs r JOIN chain c ON r.retry_of = c.id
		    WHERE c.depth < ` + strconv.Itoa(miningRetryDepthCap) + `
		)
		SELECT c.id, c.depth FROM chain c JOIN runs done_r ON done_r.id = c.id
		WHERE c.depth >= 1 AND done_r.status = 'done'
		  AND done_r.started_at IS NOT NULL` + insightWindowClauseCol("done_r.started_at", days) + `
		  AND EXISTS (SELECT 1 FROM chain c2 JOIN runs fr ON fr.id = c2.id
		              WHERE c2.root = c.root AND fr.status IN ('failed','killed'))
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]MiningSignal{}
	for rows.Next() {
		var runID string
		var depth int
		if err := rows.Scan(&runID, &depth); err != nil {
			return nil, err
		}
		out[runID] = MiningSignal{
			Kind:      "retry",
			Evidence:  "retry depth " + strconv.Itoa(depth),
			Heuristic: true,
		}
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Signal (iii): guardrail-block-then-done
// ---------------------------------------------------------------------------

// miningGuardrailBlockThenDone finds done runs that were blocked (ok=0) by a
// guardrail_block event at least once — the run finished anyway, which is
// worth a human reading whether the block was a false-positive friction
// point or a save.
func miningGuardrailBlockThenDone(st *store.Store, days int) (map[string]MiningSignal, error) {
	rows, err := st.Read().Query(`
		SELECT r.id, COUNT(*) FROM runs r JOIN events e ON e.run_id = r.id
		WHERE r.status = 'done' AND e.kind = 'guardrail_block' AND e.ok = 0
		  AND r.started_at IS NOT NULL` + insightWindowClauseCol("r.started_at", days) + `
		GROUP BY r.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]MiningSignal{}
	for rows.Next() {
		var runID string
		var n int
		if err := rows.Scan(&runID, &n); err != nil {
			return nil, err
		}
		out[runID] = MiningSignal{
			Kind:      "guardrail",
			Evidence:  formatEvidenceN(n, "guardrail block"),
			Heuristic: true,
		}
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Signal (iv): cost-outlier > 3× project median (fleet median fallback)
// ---------------------------------------------------------------------------

// miningDoneCostRow is one done run's cost, used for the per-project median
// computed in Go (SQLite has no percentile/median function).
type miningDoneCostRow struct {
	runID   string
	project string
	cost    float64
}

// miningCostOutlier loads done runs (id, project, cost_usd) in the window,
// computes the median cost per project in Go, falls back to the fleet-wide
// median when a project has fewer than miningCostMinProjectSample done runs,
// and flags cost_usd > miningCostOutlierMultiple × median. Two-sided label
// per spec: this can mean either "đáng học" (hard task, finished anyway) or
// "đáng tránh" (a costly loop) — mining does not judge which.
func miningCostOutlier(st *store.Store, days int) (map[string]MiningSignal, error) {
	rows, err := st.Read().Query(`
		SELECT id, COALESCE(project,''), cost_usd FROM runs
		WHERE status = 'done' AND started_at IS NOT NULL` + insightWindowClauseCol("started_at", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []miningDoneCostRow
	byProject := map[string][]float64{}
	for rows.Next() {
		var row miningDoneCostRow
		if err := rows.Scan(&row.runID, &row.project, &row.cost); err != nil {
			return nil, err
		}
		all = append(all, row)
		byProject[row.project] = append(byProject[row.project], row.cost)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}

	fleetCosts := make([]float64, len(all))
	for i, row := range all {
		fleetCosts[i] = row.cost
	}
	fleetMedian := median(fleetCosts)

	projectMedian := map[string]float64{}
	for project, costs := range byProject {
		if len(costs) >= miningCostMinProjectSample {
			projectMedian[project] = median(costs)
		} else {
			projectMedian[project] = fleetMedian
		}
	}

	out := map[string]MiningSignal{}
	for _, row := range all {
		m := projectMedian[row.project]
		if m <= 0 {
			continue // no honest baseline to compare against (all-zero-cost project/fleet)
		}
		if row.cost > miningCostOutlierMultiple*m {
			multiple := row.cost / m
			out[row.runID] = MiningSignal{
				Kind: "cost-outlier",
				Evidence: formatEvidenceMultiple(multiple) +
					" — đọc để biết đáng học (task khó đã xong) hay đáng tránh (loop lãng phí)",
				Heuristic: true,
			}
		}
	}
	return out, nil
}
