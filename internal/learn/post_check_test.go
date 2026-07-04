package learn

import (
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func postCheckTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// H2: the default config (PostActionChecks unset/empty) must run ZERO check
// processes — no gate_results, no post_check event. This is the load-bearing
// safety property: G6 must never execute agent-modified code unless the
// operator explicitly opts in.
func TestPostCheckDefaultConfigIsNoOp(t *testing.T) {
	st := postCheckTestStore(t)
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "done", 0, 0)
	cfg := &config.Config{} // PostActionChecks unset → nil → empty

	PostCheck(cfg, st, "r1", t.TempDir())

	var gateRows, events int
	st.DB.QueryRow(`SELECT count(*) FROM gate_results WHERE run_id='r1'`).Scan(&gateRows)
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='r1' AND kind='post_check'`).Scan(&events)
	if gateRows != 0 {
		t.Errorf("gate_results rows = %d, want 0 (default must be no-op)", gateRows)
	}
	if events != 0 {
		t.Errorf("post_check events = %d, want 0 (default must be no-op)", events)
	}
}

// A failing configured check: emits a post_check event (ok=false), records a
// gate_results row, opens a flag — and, critically, PostCheck itself must not
// panic or otherwise surface an error to the caller (there is nothing to
// return — it's a void function precisely so a failure can never propagate).
func TestPostCheckFailingCheckFlagsButDoesNotPropagate(t *testing.T) {
	st := postCheckTestStore(t)
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "done", 0, 0)
	cfg := &config.Config{PostActionChecks: []string{"exit 1"}}

	// Must not panic — if it did, the test itself would fail loudly.
	PostCheck(cfg, st, "r1", t.TempDir())

	var gateRows, failedRows, flags, events, eventOK int
	st.DB.QueryRow(`SELECT count(*) FROM gate_results WHERE run_id='r1'`).Scan(&gateRows)
	st.DB.QueryRow(`SELECT count(*) FROM gate_results WHERE run_id='r1' AND ok=0`).Scan(&failedRows)
	st.DB.QueryRow(`SELECT count(*) FROM flags WHERE run_id='r1'`).Scan(&flags)
	st.DB.QueryRow(`SELECT count(*), COALESCE(sum(ok),0) FROM events WHERE run_id='r1' AND kind='post_check'`).Scan(&events, &eventOK)
	if gateRows != 1 || failedRows != 1 {
		t.Errorf("gate_results=%d failed=%d, want 1/1", gateRows, failedRows)
	}
	if flags != 1 {
		t.Errorf("flags=%d, want 1 (failing check must flag the run)", flags)
	}
	if events != 1 {
		t.Fatalf("post_check events=%d, want 1", events)
	}
	if eventOK != 0 {
		t.Errorf("post_check event ok=%d, want 0 (a failing check)", eventOK)
	}
}

// A passing configured check: post_check event ok=true, gate_results row ok,
// no flag opened.
func TestPostCheckPassingCheckEmitsOKEvent(t *testing.T) {
	st := postCheckTestStore(t)
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "done", 0, 0)
	cfg := &config.Config{PostActionChecks: []string{"true"}}

	PostCheck(cfg, st, "r1", t.TempDir())

	var flags, events, eventOK int
	st.DB.QueryRow(`SELECT count(*) FROM flags WHERE run_id='r1'`).Scan(&flags)
	st.DB.QueryRow(`SELECT count(*), COALESCE(sum(ok),0) FROM events WHERE run_id='r1' AND kind='post_check'`).Scan(&events, &eventOK)
	if flags != 0 {
		t.Errorf("flags=%d, want 0 (all checks passed)", flags)
	}
	if events != 1 || eventOK != 1 {
		t.Errorf("post_check events=%d ok=%d, want 1/1", events, eventOK)
	}
}
