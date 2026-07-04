package runner

import (
	"bufio"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

var (
	// ErrAtCapacity means the concurrency cap is reached (HTTP 429 upstream).
	ErrAtCapacity = errors.New("đã đạt số run tối đa chạy đồng thời — thử lại sau")
	// errRefused means the global kill switch is on.
	errRefused = errors.New("global kill switch đang BẬT — từ chối phóng agent")
)

const (
	maxLogRows   = 2000    // per-run cap on run_stdout events
	maxLineBytes = 1 << 20 // 1MB single-line cap
)

// Launcher owns the shared registry + concurrency semaphore for a serve
// process. One instance is constructed in serve and passed to handlers.
type Launcher struct {
	Cfg *config.Config
	St  *store.Store
	Ing *capture.Ingestor
	Reg *Registry
	sem *semaphore.Weighted
}

func New(cfg *config.Config, st *store.Store, ing *capture.Ingestor) *Launcher {
	n := int64(cfg.MaxConcurrentLaunches)
	if n < 1 {
		n = 4
	}
	return &Launcher{Cfg: cfg, St: st, Ing: ing, Reg: NewRegistry(), sem: semaphore.NewWeighted(n)}
}

// Launch starts an agent-run asynchronously under capture and returns its run
// id immediately. Everything after Start happens in a reaper goroutine. Every
// failure path is fail-safe: it never crashes serve and never leaks a
// semaphore slot or a stuck 'running' row.
func (l *Launcher) Launch(agent, prompt, cwd, launchedBy, retryOf string) (string, error) {
	// 1. LIVE kill-switch read (never cached).
	if l.St.Setting("kill_switch_global") == "1" {
		return "", errRefused
	}
	// 2. Resolve binary from the absolute-path allowlist (never $PATH).
	absBin, err := l.resolveAgent(agent)
	if err != nil {
		return "", err
	}
	// 3. cwd must resolve inside ProjectsDir.
	if err := validateWorkDir(cwd, l.Cfg.ProjectsDir); err != nil {
		return "", err
	}
	// 4. LOCKED argv — prompt is a flag value, never a positional.
	argv, err := argvFor(agent, absBin, prompt)
	if err != nil {
		return "", err
	}
	// 5. Concurrency cap. release() is idempotent and invoked on every path.
	if !l.sem.TryAcquire(1) {
		return "", ErrAtCapacity
	}
	var relOnce sync.Once
	release := func() { relOnce.Do(func() { l.sem.Release(1) }) }

	runtime := capture.DetectRuntime(agent)
	sessionID := "console-" + newULID()
	runID, before, err := capture.StartRun(l.St, l.Ing, sessionID, cwd, "console",
		capture.Attribution{AgentName: agent, Runtime: runtime})
	if err != nil {
		release()
		return "", err
	}

	// 6. Spawn under its own process group (plain exec.Command — NO
	// CommandContext: its cancel would kill only the leader and race our
	// group-kill).
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	started := time.Now()
	if err := cmd.Start(); err != nil {
		capture.FinalizeRun(l.Cfg, l.St, l.Ing, runID, cwd, runtime, before, before, started, 1)
		release()
		return "", fmt.Errorf("start %s: %w", agent, err)
	}
	pgid := cmd.Process.Pid // == group id (Setpgid)
	l.St.DB.Exec(`UPDATE runs SET pid = ?, launched_by = ?, retry_of = NULLIF(?, '') WHERE id = ?`,
		pgid, launchedBy, retryOf, runID)
	// The launch prompt is stored as a redacted event for retry prefill.
	l.Ing.AddEvent(runID, "launch_prompt", "", sql.NullInt64{}, prompt)

	rp := &RunProc{RunID: runID, PID: pgid, Process: cmd.Process, doneCh: make(chan struct{})}
	l.Reg.Add(rp)

	go l.reap(cmd, rp, stdout, stderr, runID, cwd, runtime, before, started, release)
	return runID, nil
}

// reap drains the child's pipes to EOF, waits for exit, finalizes the run and
// releases the slot. Draining BEFORE Wait is the StdoutPipe contract (Wait
// closes the pipes). release+recover guarantee no leak even on panic.
func (l *Launcher) reap(cmd *exec.Cmd, rp *RunProc, stdout, stderr io.Reader,
	runID, cwd, runtime string, before capture.GitState, started time.Time, release func()) {
	defer func() { _ = recover(); release() }()

	var wg sync.WaitGroup
	wg.Add(2)
	var written int
	var wmu sync.Mutex
	go l.scan(stdout, runID, &wg, &written, &wmu)
	go l.scan(stderr, runID, &wg, &written, &wmu)
	wg.Wait() // drain pipes to EOF FIRST

	err := cmd.Wait() // THEN reap
	// Mark exited SYNCHRONOUSLY with Wait, BEFORE any slow work (SnapshotGit
	// shells out to git for tens of ms). Otherwise a concurrent Kill in that
	// window sees rp.Exited==false and signals -pgid at a PID the OS has
	// already reaped and possibly reused — the exact PID-reuse hit C3 exists
	// to prevent (reviewer H1).
	rp.MarkExited()
	exit := 0
	if err != nil {
		exit = 1
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		}
	}
	after := capture.SnapshotGit(cwd)
	capture.FinalizeRun(l.Cfg, l.St, l.Ing, runID, cwd, runtime, before, after, started, exit)
	l.Reg.Remove(runID)
}

// scan streams one pipe into run_stdout events, capping total rows per run and
// oversized lines. It always drains to EOF (so the child never blocks) even
// after the cap — it just stops persisting.
func (l *Launcher) scan(r io.Reader, runID string, wg *sync.WaitGroup, written *int, wmu *sync.Mutex) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	capped := false
	for sc.Scan() {
		line := sc.Text()
		wmu.Lock()
		if *written >= maxLogRows {
			if !capped {
				capped = true
				l.Ing.AddEvent(runID, "run_stdout", "", sql.NullInt64{}, "…(log truncated)")
				*written++
			}
			wmu.Unlock()
			continue // keep draining, stop persisting
		}
		*written++
		wmu.Unlock()
		l.Ing.AddEvent(runID, "run_stdout", "", sql.NullInt64{}, line)
	}
	if err := sc.Err(); err != nil {
		// A too-long line trips this; note it and keep the reap moving.
		l.Ing.AddEvent(runID, "run_stdout", "", sql.NullInt64{}, "…(line truncated)")
	}
}

// resolveAgent maps an agent name to its allowlisted absolute binary path.
// Never consults $PATH. os.Stat + IsRegular guards a bad config.
func (l *Launcher) resolveAgent(agent string) (string, error) {
	path, ok := l.Cfg.AgentBinaries[agent]
	if !ok || path == "" {
		return "", fmt.Errorf("agent %q không có trong allowlist agent_binaries", agent)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("binary của agent %q không truy cập được: %w", agent, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("binary của agent %q không phải file thường", agent)
	}
	return path, nil
}

// validateWorkDir ensures cwd resolves INSIDE projectsDir, blocking both
// `../` traversal and a sibling like `/proj-evil` when projectsDir is `/proj`.
func validateWorkDir(cwd, projectsDir string) error {
	if projectsDir == "" {
		return errors.New("projects_dir chưa cấu hình — không thể xác thực cwd")
	}
	absP, _ := filepath.Abs(projectsDir)
	absC, _ := filepath.Abs(cwd)
	rel, err := filepath.Rel(absP, absC)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("cwd %q nằm ngoài projects_dir", cwd)
	}
	return nil
}

// newULID is a small sortable unique id (ms timestamp + random) — enough to
// avoid same-nanosecond session collisions without importing the ingest pkg.
func newULID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%013x-%x", time.Now().UnixMilli(), b)
}
