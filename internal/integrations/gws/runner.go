// Package gws wraps the `gws` CLI (Google Workspace: Sheets, Calendar,
// Gmail, Drive) — keyring auth, no token handling in this process. Every
// invocation goes through Runner.run, the single exec choke point: arg-slice
// only (no shell), 15s timeout, banner-stripped output.
package gws

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"time"
)

// Gate is the narrow slice of integrations.Guard this package needs.
// Guard satisfies this interface structurally.
type Gate interface {
	Allow(action, detail string) bool
}

// ErrGwsMissing is returned when the gws binary is not installed or not on
// PATH (exit 127 / exec.ErrNotFound).
var ErrGwsMissing = errors.New("gws: binary not found")

const defaultTimeout = 15 * time.Second

// Runner is the shared exec wrapper for every gws subcommand.
type Runner struct {
	Bin   string // gws binary path/name; defaults to "gws" via NewRunner
	Guard Gate
}

// NewRunner builds a Runner with Bin resolved from DANDORI_GWS_BIN (test
// override) or "gws" on PATH.
func NewRunner(guard Gate) *Runner {
	bin := os.Getenv("DANDORI_GWS_BIN")
	if bin == "" {
		bin = "gws"
	}
	return &Runner{Bin: bin, Guard: guard}
}

// run executes `gws <args...>` with a 15s timeout and returns the
// banner-stripped stdout. No shell is ever invoked — args are always an
// explicit slice, never interpolated into a single command string.
func (r *Runner) run(ctx context.Context, args ...string) ([]byte, error) {
	out, err := r.exec(ctx, args...)
	if err != nil {
		return nil, err
	}
	return stripBanner(out), nil
}

// exec is the raw exec choke point shared by run (JSON subcommands) and
// callers that need unprocessed stdout (e.g. DriveExport's binary output).
func (r *Runner) exec(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	bin := r.Bin
	if bin == "" {
		bin = "gws"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.Output()
	if err != nil {
		if isMissingBinary(err) {
			return nil, ErrGwsMissing
		}
		return nil, err
	}
	return out, nil
}

// isMissingBinary reports whether err indicates the gws binary could not be
// found or executed: PATH lookup failure (exec.ErrNotFound), a direct path
// that doesn't exist (fs.ErrNotExist, when Bin contains a slash and skips
// PATH lookup), or exit code 127 (shell's "command not found" convention).
func isMissingBinary(err error) bool {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 127 {
		return true
	}
	return false
}

// stripBanner removes the "Using keyring backend: keyring" (and similar)
// preamble gws prints before its JSON payload, by slicing from the first
// '{' or '['. Returns the input unchanged if neither is found.
func stripBanner(b []byte) []byte {
	for i, c := range b {
		if c == '{' || c == '[' {
			return b[i:]
		}
	}
	return b
}
