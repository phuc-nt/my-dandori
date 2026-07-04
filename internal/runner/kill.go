package runner

import (
	"syscall"
	"time"

	"github.com/phuc-nt/dandori/internal/govern"
)

// KillOutcome distinguishes an actual OS-signal from a DB-only mark (for bulk
// reporting — a hook/wrap run not in the registry can only be marked).
type KillOutcome int

const (
	Marked   KillOutcome = iota // status set to killed; no process signalled
	Signaled                    // process group signalled AND status set
)

func (o KillOutcome) String() string {
	if o == Signaled {
		return "signaled"
	}
	return "marked"
}

// Kill terminates a console run's process group (if we still hold a live
// entry) AND marks the run killed with audit. The signal is sent WHILE
// holding the same per-proc lock the reaper uses to set Exited — so there is
// no window where the PID could have exited and been reused (C3). Runs not in
// the registry (hook/wrap, or already reaped) are marked-only.
func (l *Launcher) Kill(runID, actor, reason string) (KillOutcome, error) {
	out := Marked
	if rp, ok := l.Reg.Get(runID); ok {
		rp.mu.Lock()
		if !rp.Exited {
			// negative pgid → the whole process group (Setpgid at start).
			_ = syscall.Kill(-rp.PID, syscall.SIGTERM)
			out = Signaled
		}
		rp.mu.Unlock()

		if out == Signaled {
			select {
			case <-doneOrNever(rp.doneCh):
			case <-time.After(5 * time.Second):
			}
			rp.mu.Lock()
			if !rp.Exited {
				_ = syscall.Kill(-rp.PID, syscall.SIGKILL)
			}
			rp.mu.Unlock()
		}
	}
	// status='killed' + kill event + audit (idempotent; killed is terminal).
	err := govern.KillRun(l.St, runID, actor, reason)
	return out, err
}

// doneOrNever returns ch, or a never-ready channel when ch is nil (adopted
// live orphans have no waiter — Kill falls through to the grace timeout).
func doneOrNever(ch chan struct{}) <-chan struct{} {
	if ch != nil {
		return ch
	}
	return make(chan struct{})
}
