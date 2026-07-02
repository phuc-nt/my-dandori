package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallHooksMergeIdempotent(t *testing.T) {
	project := t.TempDir()
	settingsPath := filepath.Join(project, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0o755)
	// Pre-existing user hook that must survive.
	existing := `{
	  "statusLine": {"type": "command", "command": "node status.cjs"},
	  "hooks": {
	    "PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "node my-hook.cjs"}]}]
	  }
	}`
	os.WriteFile(settingsPath, []byte(existing), 0o644)

	for i := 0; i < 2; i++ { // twice → idempotent
		if err := installHooks(project, "/usr/local/bin/dandori"); err != nil {
			t.Fatal(err)
		}
	}

	b, _ := os.ReadFile(settingsPath)
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if _, ok := s["statusLine"]; !ok {
		t.Error("statusLine clobbered")
	}
	hooks := s["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	if len(pre) != 2 { // user hook + one dandori entry
		t.Errorf("PreToolUse entries: %d, want 2 (got %v)", len(pre), pre)
	}
	foundUser, foundDandori := false, false
	for _, e := range pre {
		if isDandoriEntry(e) {
			foundDandori = true
		} else {
			foundUser = true
		}
	}
	if !foundUser || !foundDandori {
		t.Errorf("merge lost entries: user=%v dandori=%v", foundUser, foundDandori)
	}
	for _, ev := range []string{"SessionStart", "PostToolUse", "Stop"} {
		if entries, _ := hooks[ev].([]any); len(entries) != 1 {
			t.Errorf("%s entries: %d, want 1", ev, len(entries))
		}
	}
}
