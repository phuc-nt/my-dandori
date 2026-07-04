package learn

import (
	"database/sql"
	"log"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

// postCheckMaxBatches bounds how many post-check batches (each one a full
// RunChecks pass over cfg.PostActionChecks) may execute concurrently across
// overlapping run finalizes. This is a process-wide cap independent of
// MaxConcurrentLaunches — it exists so a burst of finalizes cannot fork an
// unbounded number of "sh -c <check>" processes at once (H2 risk 2).
const postCheckMaxBatches = 2

// postCheckSem is package-level by design: PostCheck is called from the
// launcher's reap goroutine, one per finished run, and there is no shared
// Launcher/Config instance to hang the semaphore off inside this package.
var postCheckSem = make(chan struct{}, postCheckMaxBatches)

// PostCheck is G6: the automatic, agent-triggered sibling of the operator-run
// G7 quality gate (RunChecks). It runs ONLY commands from cfg.PostActionChecks
// (operator config.yaml — never DB/UI/agent/task text) against the run's cwd,
// AFTER the agent has already modified it (see config.go's PostActionChecks
// doc comment for the H2 threat model this implies).
//
// PostCheck NEVER returns an error to the caller and NEVER panics out of it:
// the launcher's reap goroutine calls this after FinalizeRun, and a post-check
// failure must not fail, delay-crash, or retroactively mark the run itself.
// Every failure path is logged (visible), never silently swallowed.
func PostCheck(cfg *config.Config, st *store.Store, runID, cwd string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("post_check: recovered panic run=%s: %v", runID, r)
		}
	}()
	if cfg == nil || len(cfg.PostActionChecks) == 0 {
		return // default EMPTY = opt-in (H2); no-op, no exec at all
	}

	postCheckSem <- struct{}{}
	defer func() { <-postCheckSem }()

	results, err := RunChecks(st, runID, cwd, cfg.PostActionChecks)
	ok := err == nil
	for _, res := range results {
		if !res.OK {
			ok = false
			break
		}
	}
	if emitErr := emitPostCheckEvent(st, runID, ok); emitErr != nil {
		log.Printf("post_check: failed to emit event run=%s: %v", runID, emitErr)
	}
	if err != nil {
		// Logged, not propagated: the reap must never fail because a
		// post-check command failed to execute (distinct from a check
		// simply reporting non-zero — RunChecks already records that as a
		// gate_result + flag; this branch is an infra failure, e.g. the DB
		// write for a gate_result row failing).
		log.Printf("post_check: RunChecks error run=%s: %v", runID, err)
	}
}

// emitPostCheckEvent records the post_check outcome as a run event so it is
// visible in the run timeline alongside tool_use/run_stdout events, the same
// way other lifecycle events are written directly via st.DB (there is no
// Ingestor available in this call path — the launcher's reap already drained
// the Ingestor-backed pipes and is past FinalizeRun).
func emitPostCheckEvent(st *store.Store, runID string, ok bool) error {
	okVal := 0
	if ok {
		okVal = 1
	}
	_, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, ?, 'post_check', '', ?, '')`, runID, store.Now(), sql.NullInt64{Int64: int64(okVal), Valid: true})
	return err
}
