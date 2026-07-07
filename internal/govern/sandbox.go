package govern

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sandboxAllowlist are prefixes any run may touch besides its own cwd.
var sandboxAllowlist = []string{"/tmp/", "/private/tmp/", "/var/folders/", "/dev/null"}

// homeAllowlistDirs are $HOME-relative prefixes any run may touch: the agent
// harness keeps per-project memory/state under ~/.claude/projects/. This is
// deliberately NOT all of ~/.claude — settings.json and hooks/ are the
// guardrail wiring itself; a run that can rewrite those can disarm the gates.
var homeAllowlistDirs = []string{".claude/projects/"}

// ExtractToolCall builds a ToolCall from a raw tool_input: pulls the Bash
// command and any file paths (explicit file_path fields, plus absolute-path
// tokens inside Bash commands — heuristic, documented limitation).
func ExtractToolCall(runID, agentID, project, cwd, toolName string, toolInput []byte) ToolCall {
	tc := ToolCall{RunID: runID, AgentID: agentID, Project: project, CWD: cwd, ToolName: toolName}
	var in struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	_ = json.Unmarshal(toolInput, &in)
	tc.Command = in.Command
	for _, p := range []string{in.FilePath, in.NotebookPath} {
		if p != "" {
			tc.Paths = append(tc.Paths, p)
		}
	}
	if in.Command != "" {
		tc.Paths = append(tc.Paths, pathTokens(in.Command)...)
	}
	return tc
}

// pathTokens pulls absolute/home path-looking tokens out of a shell command.
func pathTokens(command string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(command, func(r rune) bool {
		return r == ' ' || r == '\t' || r == ';' || r == '|' || r == '&' || r == '"' || r == '\'' || r == '(' || r == ')'
	}) {
		tok = strings.TrimRight(tok, ",:")
		if strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~/") {
			out = append(out, tok)
		}
	}
	return out
}

// checkSandbox denies writes/reads that escape the run's working directory
// and the shared allowlist (G2). Only Write/Edit/NotebookEdit file paths are
// enforced strictly; Bash path tokens are best-effort.
func (e *Engine) checkSandbox(tc ToolCall) (Decision, bool) {
	// G2 opt-out: a nil flag means enabled (safe default); only an explicit
	// false disables the write-scope guardrail (trusted single-dev machine).
	if e.Cfg != nil && e.Cfg.SandboxEnabled != nil && !*e.Cfg.SandboxEnabled {
		return Decision{}, false
	}
	if tc.CWD == "" {
		return Decision{}, false
	}
	home, _ := os.UserHomeDir()
	cwd := normalizePath(tc.CWD, home)
	strict := tc.ToolName == "Write" || tc.ToolName == "Edit" || tc.ToolName == "NotebookEdit"
	if !strict {
		return Decision{}, false // Bash tokens too noisy for hard deny — block rules cover the dangerous ones
	}
	for _, p := range tc.Paths {
		abs := normalizePath(p, home)
		if !filepath.IsAbs(abs) {
			continue // relative paths resolve inside cwd
		}
		if strings.HasPrefix(abs, cwd+string(filepath.Separator)) || abs == cwd || allowlisted(abs) || homeAllowlisted(abs, home) {
			continue
		}
		return Decision{Deny, fmt.Sprintf("[dandori G2] path outside run scope: %s (scope: %s)", p, tc.CWD)}, true
	}
	return Decision{}, false
}

func allowlisted(abs string) bool {
	for _, pre := range sandboxAllowlist {
		if strings.HasPrefix(abs, pre) || abs == strings.TrimSuffix(pre, "/") {
			return true
		}
	}
	return false
}

func homeAllowlisted(abs, home string) bool {
	if home == "" {
		return false
	}
	for _, dir := range homeAllowlistDirs {
		pre := filepath.Join(home, dir) + string(filepath.Separator)
		if strings.HasPrefix(abs, pre) {
			return true
		}
	}
	return false
}

func normalizePath(p, home string) string {
	if strings.HasPrefix(p, "~/") && home != "" {
		p = filepath.Join(home, p[2:])
	}
	return filepath.Clean(p)
}
