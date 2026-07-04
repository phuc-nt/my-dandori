// Package runner launches and supervises agent-runs started from the console
// (v6 Commander). It spawns claude asynchronously under its own process
// group, streams output into events, and lets the console kill the whole
// group. The web server never runs a shell — the agent binary comes from an
// absolute-path allowlist and the user's prompt is always a flag VALUE.
package runner

import (
	"os"
	"sync"
)

// RunProc is a live (or adopted-orphan) console process tracked so Kill can
// signal its group. Lifetime is the serve process; only console launches
// enter the registry (hook/wrap runs never do).
type RunProc struct {
	RunID   string
	PID     int           // == process-group id (child started with Setpgid)
	Process *os.Process   // retained handle
	mu      sync.Mutex    // guards Exited; the SAME lock Kill signals under
	Exited  bool          // set by the reaper when Wait returns
	doneCh  chan struct{} // closed on exit; nil for adopted orphans (no waiter)
}

// Registry is the process-scoped set of live console runs.
type Registry struct {
	mu   sync.RWMutex
	byID map[string]*RunProc
}

func NewRegistry() *Registry { return &Registry{byID: map[string]*RunProc{}} }

func (r *Registry) Add(rp *RunProc) {
	r.mu.Lock()
	r.byID[rp.RunID] = rp
	r.mu.Unlock()
}

func (r *Registry) Get(runID string) (*RunProc, bool) {
	r.mu.RLock()
	rp, ok := r.byID[runID]
	r.mu.RUnlock()
	return rp, ok
}

func (r *Registry) Remove(runID string) {
	r.mu.Lock()
	delete(r.byID, runID)
	r.mu.Unlock()
}

// Running returns the run ids currently tracked (for status/UI).
func (r *Registry) Running() map[string]bool {
	r.mu.RLock()
	out := make(map[string]bool, len(r.byID))
	for id := range r.byID {
		out[id] = true
	}
	r.mu.RUnlock()
	return out
}

// MarkExited flags the process exited and wakes any Kill waiter — under the
// per-proc lock, so an in-flight Kill either signals-then-sees-closed or sees
// Exited and skips (no stale-PID signal, C3).
func (rp *RunProc) MarkExited() {
	rp.mu.Lock()
	rp.Exited = true
	if rp.doneCh != nil {
		close(rp.doneCh)
	}
	rp.mu.Unlock()
}
