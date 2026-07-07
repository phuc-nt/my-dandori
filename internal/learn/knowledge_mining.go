package learn

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/phuc-nt/dandori/internal/store"
)

// Knowledge mining (v13 P1): surfaces "runs đáng đọc" via 4 SQL signals
// computed ON-DEMAND (no derived table, no cron) — same freshness discipline
// as the v12 detectors (knowledge_detect.go), never stale. Honest-data: rank
// is distinct-signal-count DESC then recency — NOT a weighted score (a
// composite score is a fabricated number, docs/07 §3). No operator column;
// title is "run đáng đọc", never a leaderboard.

// miningCostOutlierMultiple is the cost-outlier threshold (locked default,
// tune after week-1 real queue per plan.md open question #3).
const miningCostOutlierMultiple = 3.0

// miningCorrectiveMin is the minimum corrective-steering count to fire signal
// (i) — locked default, same tuning note as above.
const miningCorrectiveMin = 2

// miningRetryDepthCap bounds the recursive retry-chain CTE (M1): no cyclic
// retry_of is creatable today (handleRetry always makes a NEW run, verified
// handlers_launch.go:107) but the cap is a robustness contract, not a
// clean-data assumption.
const miningRetryDepthCap = 64

// miningCostMinProjectSample is the minimum same-project done-run sample
// before trusting a project-scoped median; below this, cost-outlier falls
// back to the fleet-wide median (too few same-project runs to read a
// meaningful per-project baseline).
const miningCostMinProjectSample = 5

// MiningSignal is one heuristic tag on a mined run, with the evidence number
// that justifies it. Heuristic is always true — every signal here is a
// keyword/threshold heuristic, never a controlled experiment (docs/07 §3).
type MiningSignal struct {
	Kind      string // corrective | retry | guardrail | cost-outlier
	Evidence  string
	Heuristic bool
}

// MinedRun is one run surfaced by MineRuns, with its signal set and whether
// it has already been minted into a knowledge unit ("đã đúc").
type MinedRun struct {
	RunID        string
	Task         string
	Project      string
	StartedAt    string
	Signals      []MiningSignal
	MintedUnitID int64 // 0 if none
}

// MineRuns computes all 4 on-demand mining signals over the window, merges
// them per run_id, filters out mining_dismissals, and ranks by
// distinct-signal-count DESC then started_at DESC (honest-data: NOT a
// weighted score). days<=0 means all-time (mirrors insightWindowClauseCol).
func MineRuns(st *store.Store, days int) ([]MinedRun, error) {
	base, err := miningRunBase(st, days)
	if err != nil {
		return nil, err
	}
	if len(base) == 0 {
		return nil, nil // honest empty — no runs in window
	}

	signals := map[string][]MiningSignal{}

	correctiveSignals, err := miningCorrectiveSteering(st, days)
	if err != nil {
		return nil, err
	}
	mergeMiningSignals(signals, correctiveSignals)

	retrySignals, err := miningFailRetrySuccess(st, days)
	if err != nil {
		return nil, err
	}
	mergeMiningSignals(signals, retrySignals)

	guardrailSignals, err := miningGuardrailBlockThenDone(st, days)
	if err != nil {
		return nil, err
	}
	mergeMiningSignals(signals, guardrailSignals)

	costSignals, err := miningCostOutlier(st, days)
	if err != nil {
		return nil, err
	}
	mergeMiningSignals(signals, costSignals)

	if len(signals) == 0 {
		return nil, nil // honest empty — no run matched any of the 4 signals
	}

	dismissed, err := miningDismissedRunIDs(st)
	if err != nil {
		return nil, err
	}
	minted, err := miningMintedUnitIDs(st)
	if err != nil {
		return nil, err
	}

	var out []MinedRun
	for runID, sigs := range signals {
		if dismissed[runID] {
			continue
		}
		b, ok := base[runID]
		if !ok {
			continue // run vanished between the signal query and the base lookup
		}
		out = append(out, MinedRun{
			RunID:        runID,
			Task:         b.task,
			Project:      b.project,
			StartedAt:    b.startedAt,
			Signals:      sigs,
			MintedUnitID: minted[runID],
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Signals) != len(out[j].Signals) {
			return len(out[i].Signals) > len(out[j].Signals)
		}
		return out[i].StartedAt > out[j].StartedAt
	})
	return out, nil
}

// mergeMiningSignals appends each run's new signals onto the accumulator,
// keeping insertion order stable across signal-query calls (map iteration
// order for the final sort is irrelevant — the sort key is len+recency).
func mergeMiningSignals(acc map[string][]MiningSignal, add map[string]MiningSignal) {
	for runID, sig := range add {
		acc[runID] = append(acc[runID], sig)
	}
}

// miningRunRow is one run's display metadata (task/project/started_at),
// looked up once so each signal query only needs to return run ids.
type miningRunRow struct {
	task      string
	project   string
	startedAt string
}

// miningRunBase loads (task,project,started_at) for every run in the window
// that has a started_at — the universe any signal's run_id must belong to
// for display purposes.
func miningRunBase(st *store.Store, days int) (map[string]miningRunRow, error) {
	rows, err := st.Read().Query(`
		SELECT id, COALESCE(task_key,''), COALESCE(project,''), COALESCE(started_at,'')
		FROM runs WHERE started_at IS NOT NULL` + insightWindowClauseCol("started_at", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]miningRunRow{}
	for rows.Next() {
		var id string
		var row miningRunRow
		if err := rows.Scan(&id, &row.task, &row.project, &row.startedAt); err != nil {
			return nil, err
		}
		out[id] = row
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Dismiss + "đã đúc" lookups
// ---------------------------------------------------------------------------

// miningDismissedRunIDs reads every dismissed run_id (M2: reading-list-only
// — this table has zero governance-suppression power, it is consulted ONLY
// here, never by run-detail/audit/any other surface).
func miningDismissedRunIDs(st *store.Store) (map[string]bool, error) {
	rows, err := st.Read().Query(`SELECT run_id FROM mining_dismissals`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// DismissMiningRun records a permanent, reading-list-only dismiss for one run
// (M2). This INSERT is the entire effect — it is never written to the
// append-only audit chain and has no bearing on run-detail/governance.
func DismissMiningRun(st *store.Store, runID, actor, reason string) error {
	_, err := st.DB.Exec(`INSERT INTO mining_dismissals(run_id, actor, reason, at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(run_id) DO NOTHING`, runID, actor, reason)
	return err
}

// miningMintedUnitIDs scans knowledge_units.provenance_run_ids (JSON array)
// for every run id it mentions, mapping run_id -> the first unit id found
// ("đã đúc" badge). A run could in principle appear in more than one unit's
// provenance; the first match (by ascending unit id, i.e. query order) is
// shown since MinedRun carries only one MintedUnitID slot.
func miningMintedUnitIDs(st *store.Store) (map[string]int64, error) {
	rows, err := st.Read().Query(`SELECT id, provenance_run_ids FROM knowledge_units
		WHERE provenance_run_ids IS NOT NULL AND provenance_run_ids != '' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var unitID int64
		var raw string
		if err := rows.Scan(&unitID, &raw); err != nil {
			return nil, err
		}
		var runIDs []string
		if err := json.Unmarshal([]byte(raw), &runIDs); err != nil {
			continue // malformed provenance JSON — skip rather than fail the whole mine
		}
		for _, runID := range runIDs {
			if _, exists := out[runID]; !exists {
				out[runID] = unitID
			}
		}
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Small formatting helpers
// ---------------------------------------------------------------------------

func formatEvidenceN(n int, label string) string {
	return strconv.Itoa(n) + " " + label
}

func formatEvidenceMultiple(multiple float64) string {
	// one decimal place, e.g. "cost 4.2x median"
	return "cost " + strconv.FormatFloat(multiple, 'f', 1, 64) + "x median"
}
