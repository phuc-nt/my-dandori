package capture

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RuntimeAdapter resolves token usage for a wrapped agent run. Adapters are
// best-effort: when a runtime's logs can't be found or parsed, they return a
// zero Usage rather than invented numbers (provenance over completeness).
type RuntimeAdapter interface {
	Name() string
	// UsageAfterRun is called once the wrapped process exits.
	UsageAfterRun(cwd string, startedUnix int64) Usage
}

// DetectRuntime guesses the runtime from the wrapped command's argv[0].
func DetectRuntime(argv0 string) string {
	base := filepath.Base(argv0)
	switch {
	case strings.HasPrefix(base, "claude"):
		return "claude-code"
	case strings.HasPrefix(base, "codex"):
		return "codex"
	}
	return "generic"
}

// AdapterFor returns the adapter for a runtime name (generic fallback).
func AdapterFor(runtime, projectsDir string) RuntimeAdapter {
	switch runtime {
	case "claude-code":
		return claudeAdapter{projectsDir: projectsDir}
	case "codex":
		return codexAdapter{}
	}
	return genericAdapter{}
}

type genericAdapter struct{}

func (genericAdapter) Name() string                      { return "generic" }
func (genericAdapter) UsageAfterRun(string, int64) Usage { return Usage{} }

// claudeAdapter finds the newest transcript for the cwd modified after the
// run started and reuses the standard parser.
type claudeAdapter struct{ projectsDir string }

func (claudeAdapter) Name() string { return "claude-code" }

func (a claudeAdapter) UsageAfterRun(cwd string, startedUnix int64) Usage {
	dir := filepath.Join(a.projectsDir, encodeCwd(cwd))
	newest := newestJSONL(dir, startedUnix)
	if newest == "" {
		return Usage{}
	}
	u, err := ParseTranscript(newest)
	if err != nil {
		return Usage{}
	}
	return u
}

// codexAdapter scans ~/.codex/sessions for a session log written after the
// run started. Codex log format varies by version; the standard transcript
// parser handles the common {type, message.usage} shape — anything else
// yields zero usage.
type codexAdapter struct{}

func (codexAdapter) Name() string { return "codex" }

func (codexAdapter) UsageAfterRun(cwd string, startedUnix int64) Usage {
	home, _ := os.UserHomeDir()
	newest := newestJSONL(filepath.Join(home, ".codex", "sessions"), startedUnix)
	if newest == "" {
		return Usage{}
	}
	u, err := ParseTranscript(newest)
	if err != nil {
		return Usage{}
	}
	return u
}

// encodeCwd mirrors Claude Code's project-dir encoding (path separators → dashes).
func encodeCwd(cwd string) string {
	return strings.ReplaceAll(cwd, string(filepath.Separator), "-")
}

// newestJSONL returns the most recently modified .jsonl under dir whose
// mtime is at or after start (0 = any).
func newestJSONL(dir string, startUnix int64) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	sort.Slice(matches, func(i, j int) bool {
		fi, _ := os.Stat(matches[i])
		fj, _ := os.Stat(matches[j])
		if fi == nil || fj == nil {
			return false
		}
		return fi.ModTime().After(fj.ModTime())
	})
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err == nil && (startUnix == 0 || fi.ModTime().Unix() >= startUnix) {
			return m
		}
	}
	return ""
}
