package runner

import (
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// Launch a long-lived child, Kill it, verify the OS process actually dies and
// the run is marked killed with outcome Signaled.
func TestKillTerminatesProcessGroup(t *testing.T) {
	proj := t.TempDir()
	l := testLauncher(t, proj)
	l.Cfg.AgentBinaries["claude"] = "/bin/sleep"
	// argvFor forces [-p prompt]; sleep -p 30 → sleep treats -p as an operand
	// and errors fast on some systems. Use a prompt that sleep accepts: "30".
	runID, err := l.Launch("claude", "30", proj, "phuc@console", "")
	if err != nil {
		t.Fatal(err)
	}
	// Wait until the process is registered + alive.
	rp, ok := l.Reg.Get(runID)
	if !ok {
		t.Fatal("run not registered")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !alive(rp.PID) {
		time.Sleep(20 * time.Millisecond)
	}

	out, err := l.Kill(runID, "phuc@console", "test")
	if err != nil {
		t.Fatal(err)
	}
	if out != Signaled {
		t.Errorf("kill outcome = %v, want Signaled", out)
	}
	// Process group must be gone.
	if alive(rp.PID) {
		time.Sleep(200 * time.Millisecond)
		if alive(rp.PID) {
			t.Error("process still alive after kill")
		}
	}
	var status string
	l.St.DB.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&status)
	if status != "killed" {
		t.Errorf("status = %q, want killed", status)
	}
}

// Kill on an already-exited run must NOT signal (no reused-PID hit) but still
// marks killed → outcome Marked.
func TestKillAfterExitIsMarkedOnly(t *testing.T) {
	proj := t.TempDir()
	l := testLauncher(t, proj)
	l.Cfg.AgentBinaries["claude"] = "/bin/echo"
	runID, _ := l.Launch("claude", "hi", proj, "phuc@console", "")
	// Wait for exit + reaper cleanup (registry entry removed).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := l.Reg.Get(runID); !ok {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	out, err := l.Kill(runID, "phuc@console", "late")
	if err != nil {
		t.Fatal(err)
	}
	if out != Marked {
		t.Errorf("kill after exit outcome = %v, want Marked (no signal)", out)
	}
}

func TestReconcileDeadToFailedLiveToAdopted(t *testing.T) {
	proj := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = proj
	// A real live process to adopt.
	live := exec.Command("/bin/sleep", "30")
	live.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	live.Start()
	defer syscall.Kill(-live.Process.Pid, syscall.SIGKILL)
	livePID := live.Process.Pid

	st.DB.Exec(`INSERT INTO agents(id,name,created_at) VALUES('a','a','now')`)
	// dead pid (very high, not running)
	st.DB.Exec(`INSERT INTO runs(id,session_id,agent_id,source,status,pid,started_at) VALUES('dead','dead','a','console','running',2147480000,'now')`)
	// live pid
	st.DB.Exec(`INSERT INTO runs(id,session_id,agent_id,source,status,pid,started_at) VALUES('live','live','a','console','running',?,'now')`, livePID)
	// hook run (no pid) must be untouched
	st.DB.Exec(`INSERT INTO runs(id,session_id,agent_id,source,status,started_at) VALUES('hook','hook','a','hook','running','now')`)

	reg := NewRegistry()
	Reconcile(reg, st)

	var deadStatus, liveStatus, hookStatus string
	st.DB.QueryRow(`SELECT status FROM runs WHERE id='dead'`).Scan(&deadStatus)
	st.DB.QueryRow(`SELECT status FROM runs WHERE id='live'`).Scan(&liveStatus)
	st.DB.QueryRow(`SELECT status FROM runs WHERE id='hook'`).Scan(&hookStatus)
	if deadStatus != "failed" {
		t.Errorf("dead pid run status = %q, want failed", deadStatus)
	}
	if liveStatus != "orphaned" {
		t.Errorf("live pid run status = %q, want orphaned", liveStatus)
	}
	if hookStatus != "running" {
		t.Errorf("hook run clobbered: %q (should be untouched)", hookStatus)
	}
	// Live orphan re-registered → killable.
	if rp, ok := reg.Get("live"); !ok || rp.PID != livePID {
		t.Error("live orphan not adopted into registry")
	}
}
