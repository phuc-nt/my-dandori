package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/phuc-nt/dandori/internal/store"
	"github.com/spf13/cobra"
)

var (
	flagInitProject string
	flagInitAgent   string
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Install Dandori capture/guardrail hooks into a project's .claude/settings.json",
	RunE: func(cmd *cobra.Command, args []string) error {
		project := flagInitProject
		if project == "" {
			project, _ = os.Getwd()
		}
		project, err := filepath.Abs(project)
		if err != nil {
			return err
		}
		bin, err := os.Executable()
		if err != nil {
			return err
		}
		if err := installHooks(project, bin); err != nil {
			return err
		}
		agent := flagInitAgent
		if agent == "" {
			agent = filepath.Base(project)
		}
		marker := fmt.Sprintf("agent: %s\nproject: %s\n", agent, filepath.Base(project))
		if err := os.WriteFile(filepath.Join(project, ".dandori-agent"), []byte(marker), 0o644); err != nil {
			return err
		}
		// Register the project in the central DB so the console health/wizard
		// reports it as hooked immediately, before any run is captured. Uses the
		// same store path serve resolves (config.DBPath), not a per-project DB.
		// Best-effort: a DB failure must not fail the hook install (the primary
		// job of init).
		if _, st, err := openStore(); err == nil {
			_ = st.SetSetting("hooked:"+project, store.Now())
			_ = st.Close()
		}
		fmt.Printf("hooks installed in %s (agent=%s)\n", filepath.Join(project, ".claude", "settings.json"), agent)
		return nil
	},
}

// dandoriHooks builds the hook entries pointing at this binary.
func dandoriHooks(bin string) map[string][]map[string]any {
	entry := func(matcher, event string, timeout int) map[string]any {
		h := map[string]any{"type": "command", "command": bin + " hook " + event}
		if timeout > 0 {
			h["timeout"] = timeout
		}
		m := map[string]any{"hooks": []any{h}}
		if matcher != "" {
			m["matcher"] = matcher
		}
		return m
	}
	return map[string][]map[string]any{
		"SessionStart": {entry("startup|resume", "session-start", 0)},
		"PreToolUse":   {entry("*", "pre-tool", 40)},
		"PostToolUse":  {entry("*", "post-tool", 0)},
		"Stop":         {entry("", "stop", 0)},
	}
}

// installHooks merges Dandori hook entries into settings.json, preserving
// everything else. Idempotent: existing "dandori hook" entries are replaced,
// never duplicated.
func installHooks(project, bin string) error {
	path := filepath.Join(project, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	settings := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &settings); err != nil {
			return fmt.Errorf("existing %s is not valid JSON: %w", path, err)
		}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for event, entries := range dandoriHooks(bin) {
		existing, _ := hooks[event].([]any)
		kept := existing[:0:0]
		for _, e := range existing {
			if !isDandoriEntry(e) {
				kept = append(kept, e)
			}
		}
		for _, e := range entries {
			kept = append(kept, e)
		}
		hooks[event] = kept
	}
	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// isDandoriEntry reports whether a hooks entry was installed by dandori init.
func isDandoriEntry(e any) bool {
	m, _ := e.(map[string]any)
	inner, _ := m["hooks"].([]any)
	for _, h := range inner {
		hm, _ := h.(map[string]any)
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, "dandori hook") ||
			strings.Contains(cmd, " hook pre-tool") || strings.Contains(cmd, " hook post-tool") ||
			strings.Contains(cmd, " hook session-start") || strings.Contains(cmd, " hook stop") {
			return true
		}
	}
	return false
}

func init() {
	initCmd.Flags().StringVar(&flagInitProject, "project", "", "target project directory (default CWD)")
	initCmd.Flags().StringVar(&flagInitAgent, "agent", "", "agent name for attribution (default dir name)")
	rootCmd.AddCommand(initCmd)
}
