package learn

import (
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func gateOverrideTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "go.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// fakeAuditor records every Append call so tests can assert exactly how many
// audit entries a call produced without a real govern.Audit/DB round trip.
type fakeAuditor struct{ calls []string }

func (f *fakeAuditor) Append(action, subject, detail string) (int64, error) {
	f.calls = append(f.calls, action+"|"+subject+"|"+detail)
	return int64(len(f.calls)), nil
}

func seedTwoFailingChecks(t *testing.T, st *store.Store, runID string) {
	t.Helper()
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, runID, "a1", "done", 0, 0)
	if _, err := RunChecks(st, runID, t.TempDir(), []string{"exit 1", "exit 2"}); err != nil {
		t.Fatal(err)
	}
}

// Empty reason is rejected before any DB write or audit call — never a
// silent bypass (UB4's core invariant).
func TestOverrideGateEmptyReasonRejected(t *testing.T) {
	st := gateOverrideTestStore(t)
	seedTwoFailingChecks(t, st, "r1")
	aud := &fakeAuditor{}

	err := OverrideGate(st, aud, "r1", "exit 1", "op@x", "")
	if err != ErrOverrideReasonRequired {
		t.Fatalf("err = %v, want ErrOverrideReasonRequired", err)
	}
	if len(aud.calls) != 0 {
		t.Errorf("audit calls = %d, want 0 (rejected before any write)", len(aud.calls))
	}
	var overridden int
	st.DB.QueryRow(`SELECT count(*) FROM gate_results WHERE run_id='r1' AND overridden_at IS NOT NULL`).Scan(&overridden)
	if overridden != 0 {
		t.Errorf("overridden rows = %d, want 0", overridden)
	}
}

// Overriding one failing check only touches that row — the sibling failing
// check (and its flag) must remain untouched (per-check, never blanket).
func TestOverrideGateTouchesOnlyNamedCheck(t *testing.T) {
	st := gateOverrideTestStore(t)
	seedTwoFailingChecks(t, st, "r1")
	aud := &fakeAuditor{}

	if err := OverrideGate(st, aud, "r1", "exit 1", "op@x", "known flaky in this env"); err != nil {
		t.Fatal(err)
	}

	var thisOverridden, otherOverridden int
	st.DB.QueryRow(`SELECT count(*) FROM gate_results WHERE run_id='r1' AND check_name='exit 1' AND overridden_at IS NOT NULL`).Scan(&thisOverridden)
	st.DB.QueryRow(`SELECT count(*) FROM gate_results WHERE run_id='r1' AND check_name='exit 2' AND overridden_at IS NOT NULL`).Scan(&otherOverridden)
	if thisOverridden != 1 {
		t.Errorf("exit 1 overridden rows = %d, want 1", thisOverridden)
	}
	if otherOverridden != 0 {
		t.Errorf("exit 2 overridden rows = %d, want 0 (must not blanket-override)", otherOverridden)
	}

	// One check still failing and un-overridden — the flag must stay open.
	var flagStatus string
	st.DB.QueryRow(`SELECT status FROM flags WHERE run_id='r1'`).Scan(&flagStatus)
	if flagStatus != "open" {
		t.Errorf("flag status = %q, want open (one check still unresolved)", flagStatus)
	}
	if len(aud.calls) != 1 || aud.calls[0][:15] != "gate_overridden" {
		t.Errorf("audit calls = %v, want exactly one gate_overridden", aud.calls)
	}
}

// Overriding the LAST un-overridden failing check resolves the flag — status
// change only, the flag row itself is never deleted (traceability).
func TestOverrideGateResolvesFlagWhenLastCheckCleared(t *testing.T) {
	st := gateOverrideTestStore(t)
	seedTwoFailingChecks(t, st, "r1")
	aud := &fakeAuditor{}

	if err := OverrideGate(st, aud, "r1", "exit 1", "op@x", "reason one"); err != nil {
		t.Fatal(err)
	}
	if err := OverrideGate(st, aud, "r1", "exit 2", "op@x", "reason two"); err != nil {
		t.Fatal(err)
	}

	var flagCount int
	var flagStatus string
	st.DB.QueryRow(`SELECT count(*) FROM flags WHERE run_id='r1'`).Scan(&flagCount)
	st.DB.QueryRow(`SELECT status FROM flags WHERE run_id='r1'`).Scan(&flagStatus)
	if flagCount != 1 {
		t.Fatalf("flags row count = %d, want 1 (never deleted)", flagCount)
	}
	if flagStatus != "resolved" {
		t.Errorf("flag status = %q, want resolved (all failing checks cleared)", flagStatus)
	}
	// Both gate_overridden audits, plus one flag_resolved on the second call.
	var kinds []string
	for _, c := range aud.calls {
		kinds = append(kinds, c)
	}
	if len(kinds) != 3 {
		t.Fatalf("audit calls = %v, want 3 (2x gate_overridden + 1x flag_resolved)", kinds)
	}
}

// A second override of the same check is a no-op: no duplicate audit, the
// original overridden_by/reason/timestamp are preserved (row is immutable
// once set — WHERE overridden_at IS NULL prevents the second UPDATE).
func TestOverrideGateSecondCallOnSameCheckIsNoOp(t *testing.T) {
	st := gateOverrideTestStore(t)
	seedTwoFailingChecks(t, st, "r1")
	aud := &fakeAuditor{}

	if err := OverrideGate(st, aud, "r1", "exit 1", "first@x", "first reason"); err != nil {
		t.Fatal(err)
	}
	firstCalls := len(aud.calls)

	if err := OverrideGate(st, aud, "r1", "exit 1", "second@x", "second reason"); err != nil {
		t.Fatal(err)
	}
	if len(aud.calls) != firstCalls {
		t.Errorf("audit calls after second override = %d, want unchanged at %d (no-op)", len(aud.calls), firstCalls)
	}

	var by, reason string
	st.DB.QueryRow(`SELECT overridden_by, override_reason FROM gate_results WHERE run_id='r1' AND check_name='exit 1'`).Scan(&by, &reason)
	if by != "first@x" || reason != "first reason" {
		t.Errorf("overridden_by/reason = %q/%q, want original values preserved (immutable)", by, reason)
	}
}

// Overriding a check that never failed (or doesn't exist) is a no-op — the
// WHERE clause requires ok=0, so a passing check can never be marked
// "overridden" (that would misrepresent the audit trail).
func TestOverrideGatePassingOrUnknownCheckIsNoOp(t *testing.T) {
	st := gateOverrideTestStore(t)
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "done", 0, 0)
	if _, err := RunChecks(st, "r1", t.TempDir(), []string{"true"}); err != nil {
		t.Fatal(err)
	}
	aud := &fakeAuditor{}

	if err := OverrideGate(st, aud, "r1", "true", "op@x", "reason"); err != nil {
		t.Fatal(err)
	}
	if len(aud.calls) != 0 {
		t.Errorf("audit calls = %d, want 0 (passing check is never overridden)", len(aud.calls))
	}

	if err := OverrideGate(st, aud, "r1", "no-such-check", "op@x", "reason"); err != nil {
		t.Fatal(err)
	}
	if len(aud.calls) != 0 {
		t.Errorf("audit calls after unknown-check override = %d, want 0", len(aud.calls))
	}
}
