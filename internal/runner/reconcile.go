package runner

import (
	"log"
	"os"
	"syscall"

	"github.com/phuc-nt/dandori/internal/store"
)

// Reconcile runs once at serve startup to resolve console-launched runs left
// mid-flight by a previous serve process:
//
//   - dead pid (Signal(0) fails)  → status 'failed' (it can't finish)
//   - live pid                    → RE-REGISTER (adopt) so a later Kill can
//     still signal its group, and mark 'orphaned'
//
// Only console launches are touched (source='console' AND pid IS NOT NULL) —
// hook/wrap runs never carry a pid and are left alone. An adopted orphan has
// no waiter (Wait would ECHILD — it's not our child anymore), so its Kill
// path uses SIGKILL-without-waiter and lets govern.KillRun finalize the row;
// it has no live log (the old scanner died with the previous serve).
func Reconcile(reg *Registry, st *store.Store) {
	rows, err := st.DB.Query(`SELECT id, pid FROM runs
		WHERE source = 'console' AND pid IS NOT NULL AND status IN ('running','orphaned')`)
	if err != nil {
		return
	}
	type row struct {
		id  string
		pid int
	}
	var pending []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.pid) == nil {
			pending = append(pending, r)
		}
	}
	rows.Close()

	for _, r := range pending {
		if !alive(r.pid) {
			st.DB.Exec(`UPDATE runs SET status = 'failed', ended_at = ? WHERE id = ?`, store.Now(), r.id)
			continue
		}
		p, _ := os.FindProcess(r.pid)
		reg.Add(&RunProc{RunID: r.id, PID: r.pid, Process: p, doneCh: nil}) // adopted: no waiter
		st.DB.Exec(`UPDATE runs SET status = 'orphaned' WHERE id = ?`, r.id)
		log.Printf("runner: adopted live orphan %s (pid=%d) — killable, no live log", r.id, r.pid)
	}
}

// alive reports whether pid is a live process (signal 0 probes existence).
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
