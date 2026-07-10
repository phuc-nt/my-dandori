package learn

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// realFleetDBPath locates the real fleet DB at ~/.dandori/dandori.db, used
// ONLY as a read source to copy from — the migration-017-on-real-schema test
// must never open or modify this path directly. Returns "" if it does not
// exist (test skips rather than fails — CI/sandboxed environments won't have
// a real fleet DB).
func realFleetDBPath(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".dandori", "dandori.db")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// copyFleetDB copies the real fleet DB file into a fresh t.TempDir() so the
// migration test can open+migrate the COPY — the original at fleetPath is
// only ever read here, never opened by store.Open or written to.
func copyFleetDB(t *testing.T, fleetPath string) string {
	t.Helper()
	src, err := os.Open(fleetPath)
	if err != nil {
		t.Fatalf("reading real fleet DB (read-only copy source): %v", err)
	}
	defer src.Close()

	dstPath := filepath.Join(t.TempDir(), "fleet-copy.db")
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		t.Fatalf("copying fleet DB to temp dir: %v", err)
	}
	return dstPath
}

func miningTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mining.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a','a',datetime('now'))
		ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	return st
}

// miningRun seeds one runs row with an explicit started/ended pair so window
// clauses ("now - N days") always include it (startedAgoMin small).
func miningRun(t *testing.T, st *store.Store, id, status, project string, retryOf string, cost float64) {
	t.Helper()
	started := time.Now().UTC().Add(-10 * time.Minute)
	ended := started.Add(5 * time.Minute)
	var endedAt any
	if status == "running" {
		endedAt = nil
	} else {
		endedAt = ended.Format(time.RFC3339)
	}
	var retryOfVal any
	if retryOf == "" {
		retryOfVal = nil
	} else {
		retryOfVal = retryOf
	}
	_, err := st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, status, started_at, ended_at, cost_usd, retry_of, source)
		VALUES(?,?,?,?,?,?,?,?,?,'hook')`,
		id, id, "a", project, status, started.Format(time.RFC3339), endedAt, cost, retryOfVal)
	if err != nil {
		t.Fatal(err)
	}
}

func miningSteeringMsg(t *testing.T, st *store.Store, runID, text string) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, datetime('now'), 'steering_msg', ?)`,
		runID, text); err != nil {
		t.Fatal(err)
	}
}

func miningGuardrailBlock(t *testing.T, st *store.Store, runID string) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, ok) VALUES(?, datetime('now'), 'guardrail_block', 0)`,
		runID); err != nil {
		t.Fatal(err)
	}
}

func miningDismiss(t *testing.T, st *store.Store, runID string) {
	t.Helper()
	if err := DismissMiningRun(st, runID, "tester", "noise"); err != nil {
		t.Fatal(err)
	}
}

func miningNominateUnit(t *testing.T, st *store.Store, provRunIDs []string) int64 {
	t.Helper()
	id, err := NominateUnit(st, NominateParams{
		Kind: KindSkill, Name: "minted-skill", Title: "t", Body: "body",
		ProvenanceRun: provRunIDs, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func signalKinds(sigs []MiningSignal) map[string]bool {
	out := map[string]bool{}
	for _, s := range sigs {
		out[s.Kind] = true
	}
	return out
}

func findMined(runs []MinedRun, runID string) *MinedRun {
	for i := range runs {
		if runs[i].RunID == runID {
			return &runs[i]
		}
	}
	return nil
}

func TestMineRunsCorrectiveSteering(t *testing.T) {
	st := miningTestStore(t)
	miningRun(t, st, "r-corrective-yes", "done", "p", "", 1)
	miningSteeringMsg(t, st, "r-corrective-yes", "sai rồi, fix lại")
	miningSteeringMsg(t, st, "r-corrective-yes", "won't work, thử lại")

	miningRun(t, st, "r-corrective-no", "done", "p", "", 1)
	miningSteeringMsg(t, st, "r-corrective-no", "sai rồi, fix lại")

	runs, err := MineRuns(st, 0)
	if err != nil {
		t.Fatal(err)
	}

	yes := findMined(runs, "r-corrective-yes")
	if yes == nil || !signalKinds(yes.Signals)["corrective"] {
		t.Errorf("r-corrective-yes: want corrective signal, got %+v", yes)
	}
	no := findMined(runs, "r-corrective-no")
	if no != nil && signalKinds(no.Signals)["corrective"] {
		t.Errorf("r-corrective-no: corrective count=1 < min=2, should NOT fire")
	}
}

func TestMineRunsGuardrailBlockThenDone(t *testing.T) {
	st := miningTestStore(t)
	miningRun(t, st, "r-guard-done", "done", "p", "", 1)
	miningGuardrailBlock(t, st, "r-guard-done")

	miningRun(t, st, "r-guard-failed", "failed", "p", "", 1)
	miningGuardrailBlock(t, st, "r-guard-failed")

	runs, err := MineRuns(st, 0)
	if err != nil {
		t.Fatal(err)
	}

	done := findMined(runs, "r-guard-done")
	if done == nil || !signalKinds(done.Signals)["guardrail"] {
		t.Errorf("r-guard-done: want guardrail signal, got %+v", done)
	}
	failed := findMined(runs, "r-guard-failed")
	if failed != nil && signalKinds(failed.Signals)["guardrail"] {
		t.Errorf("r-guard-failed: guardrail_block on a FAILED run must not fire (spec requires done)")
	}
}

func TestMineRunsCostOutlier(t *testing.T) {
	st := miningTestStore(t)
	// Project "p" median-establishing runs at cost 1.0 (5 runs, >= sample floor).
	for _, id := range []string{"c1", "c2", "c3", "c4", "c5"} {
		miningRun(t, st, id, "done", "p", "", 1.0)
	}
	miningRun(t, st, "r-cost-outlier", "done", "p", "", 5.0) // 5x median > 3x -> fires
	miningRun(t, st, "r-cost-normal", "done", "p", "", 2.5)  // 2.5x median <= 3x -> no fire

	runs, err := MineRuns(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	outlier := findMined(runs, "r-cost-outlier")
	if outlier == nil || !signalKinds(outlier.Signals)["cost-outlier"] {
		t.Errorf("r-cost-outlier: want cost-outlier signal, got %+v", outlier)
	}
	normal := findMined(runs, "r-cost-normal")
	if normal != nil && signalKinds(normal.Signals)["cost-outlier"] {
		t.Errorf("r-cost-normal: 2.5x median must NOT fire (threshold is >3x)")
	}
}

func TestMineRunsFailRetrySuccess(t *testing.T) {
	st := miningTestStore(t)
	miningRun(t, st, "root-fail", "failed", "p", "", 1)
	miningRun(t, st, "retry-done", "done", "p", "root-fail", 1)

	runs, err := MineRuns(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	tail := findMined(runs, "retry-done")
	if tail == nil || !signalKinds(tail.Signals)["retry"] {
		t.Errorf("retry-done: want retry signal (done at tail, failed ancestor), got %+v", tail)
	}
	root := findMined(runs, "root-fail")
	if root != nil && signalKinds(root.Signals)["retry"] {
		t.Errorf("root-fail: the failed root itself is not done, must not carry the retry signal")
	}
}

// TestMineRunsRetryChainCyclicGuard seeds a cyclic retry_of (a->b->a, not
// creatable via handleRetry today but a possible future/corrupt-data state,
// M1) and asserts MineRuns returns without hanging, bounded by the depth cap.
func TestMineRunsRetryChainCyclicGuard(t *testing.T) {
	st := miningTestStore(t)
	miningRun(t, st, "cyc-a", "failed", "p", "cyc-b", 1)
	miningRun(t, st, "cyc-b", "done", "p", "cyc-a", 1)

	done := make(chan struct{})
	var runs []MinedRun
	var err error
	go func() {
		runs, err = MineRuns(st, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("MineRuns hung on cyclic retry_of — depth cap not effective")
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = runs // reaching here without timeout is the assertion
}

// TestMineRunsOrphanRootLineage seeds a run whose retry_of points at a
// non-existent parent row (deleted/foreign) and asserts its lineage still
// surfaces via the orphan-root union rather than vanishing from signal (ii).
func TestMineRunsOrphanRootLineage(t *testing.T) {
	st := miningTestStore(t)
	// orphan root: retry_of references a run id that was never inserted.
	miningRun(t, st, "orphan-root-done", "failed", "p", "ghost-parent", 1)
	miningRun(t, st, "orphan-retry-done", "done", "p", "orphan-root-done", 1)

	runs, err := MineRuns(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	tail := findMined(runs, "orphan-retry-done")
	if tail == nil || !signalKinds(tail.Signals)["retry"] {
		t.Errorf("orphan-retry-done: orphan-root lineage must still surface the retry signal, got %+v", tail)
	}
}

func TestMineRunsRankOrder(t *testing.T) {
	st := miningTestStore(t)
	// r-two-signals: corrective (>=2) AND guardrail-block-then-done.
	miningRun(t, st, "r-two-signals", "done", "p", "", 1)
	miningSteeringMsg(t, st, "r-two-signals", "sai rồi")
	miningSteeringMsg(t, st, "r-two-signals", "won't work")
	miningGuardrailBlock(t, st, "r-two-signals")

	// r-one-signal: guardrail-block-then-done only.
	miningRun(t, st, "r-one-signal", "done", "p", "", 1)
	miningGuardrailBlock(t, st, "r-one-signal")

	runs, err := MineRuns(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	two := findMined(runs, "r-two-signals")
	one := findMined(runs, "r-one-signal")
	if two == nil || one == nil {
		t.Fatalf("expected both runs mined, got two=%+v one=%+v", two, one)
	}
	if len(two.Signals) < 2 {
		t.Fatalf("r-two-signals should carry >=2 distinct signals, got %d", len(two.Signals))
	}
	var idxTwo, idxOne = -1, -1
	for i, mr := range runs {
		if mr.RunID == "r-two-signals" {
			idxTwo = i
		}
		if mr.RunID == "r-one-signal" {
			idxOne = i
		}
	}
	if idxTwo > idxOne {
		t.Errorf("rank order wrong: 2-signal run (idx %d) should rank above 1-signal run (idx %d)", idxTwo, idxOne)
	}
}

func TestMineRunsDismissIsReadingListOnly(t *testing.T) {
	st := miningTestStore(t)
	miningRun(t, st, "r-dismiss", "done", "p", "", 1)
	miningGuardrailBlock(t, st, "r-dismiss")

	before, err := MineRuns(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if findMined(before, "r-dismiss") == nil {
		t.Fatal("setup: r-dismiss should be mined before dismiss")
	}

	miningDismiss(t, st, "r-dismiss")

	after, err := MineRuns(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if findMined(after, "r-dismiss") != nil {
		t.Error("r-dismiss should be absent from the mining queue after dismiss")
	}

	// M2: dismiss must NOT suppress the run from a run-detail-style query —
	// it must still be fully readable directly off the runs table.
	var status string
	if err := st.Read().QueryRow(`SELECT status FROM runs WHERE id = ?`, "r-dismiss").Scan(&status); err != nil {
		t.Fatalf("dismissed run must still be queryable from runs (run-detail surface): %v", err)
	}
	if status != "done" {
		t.Errorf("dismissed run status = %q, want done (untouched)", status)
	}

	// Assert mining_dismissals was never mirrored into any audit-like table —
	// the only side effect is the one row in mining_dismissals itself.
	var n int
	if err := st.Read().QueryRow(`SELECT count(*) FROM mining_dismissals WHERE run_id = ?`, "r-dismiss").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("mining_dismissals row count = %d, want 1", n)
	}
}

func TestMineRunsMintedBadge(t *testing.T) {
	st := miningTestStore(t)
	miningRun(t, st, "r-minted", "done", "p", "", 1)
	miningGuardrailBlock(t, st, "r-minted")

	unitID := miningNominateUnit(t, st, []string{"r-minted"})

	runs, err := MineRuns(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	mr := findMined(runs, "r-minted")
	if mr == nil {
		t.Fatal("r-minted should still be mined (đã đúc does not hide a run)")
	}
	if mr.MintedUnitID != unitID {
		t.Errorf("MintedUnitID = %d, want %d", mr.MintedUnitID, unitID)
	}
}

func TestMineRunsEmptyWindow(t *testing.T) {
	st := miningTestStore(t)
	runs, err := MineRuns(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("empty DB: want 0 mined runs, got %d", len(runs))
	}
}

// TestMigrationsApplyCleanOnFleetCopy copies the REAL fleet DB (never
// touches the original) into a fresh temp dir and asserts migrations apply
// cleanly to the latest user_version, and pre-existing knowledge_units rows
// (if any) default origin='human' (no backfill, per spec).
func TestMigrationsApplyCleanOnFleetCopy(t *testing.T) {
	fleetPath := realFleetDBPath(t)
	if fleetPath == "" {
		t.Skip("no real fleet DB found at ~/.dandori/dandori.db — skipping copy-based migration test")
	}
	copyPath := copyFleetDB(t, fleetPath)

	st, err := store.Open(copyPath)
	if err != nil {
		t.Fatalf("opening copied fleet DB failed migration: %v", err)
	}
	defer st.Close()

	var version int
	if err := st.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 21 {
		t.Errorf("user_version after migrate = %d, want 21", version)
	}

	rows, err := st.DB.Query(`SELECT origin FROM knowledge_units`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var origin string
		if err := rows.Scan(&origin); err != nil {
			t.Fatal(err)
		}
		if origin != "human" {
			t.Errorf("pre-existing knowledge_units row has origin=%q, want 'human' (no backfill)", origin)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
