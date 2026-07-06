package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// chdirTemp runs the test in a fresh temp CWD so ./.env is isolated.
func chdirTemp(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

func TestSaveEnvKeysPreservesUnmanagedLines(t *testing.T) {
	chdirTemp(t)
	seed := "# my comment\nDRY_RUN=true\nSLACK_APPROVERS=alice\nOPENROUTER_API_KEY=old\n"
	if err := os.WriteFile(".env", []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveEnvKeys(map[string]string{"OPENROUTER_API_KEY": "new"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(".env")
	s := string(got)
	// Managed key updated in place.
	if !strings.Contains(s, "OPENROUTER_API_KEY=new") || strings.Contains(s, "OPENROUTER_API_KEY=old") {
		t.Errorf("managed key not updated:\n%s", s)
	}
	// Unmanaged lines untouched — critically the guardrail switches.
	for _, want := range []string{"# my comment", "DRY_RUN=true", "SLACK_APPROVERS=alice"} {
		if !strings.Contains(s, want) {
			t.Errorf("lost unmanaged line %q:\n%s", want, s)
		}
	}
}

func TestSaveEnvKeysRejectsGuardrailAndUnmanagedKeys(t *testing.T) {
	chdirTemp(t)
	for _, k := range []string{"DRY_RUN", "AGENT_WRITE_DISABLED", "SLACK_APPROVERS", "PATH"} {
		if err := SaveEnvKeys(map[string]string{k: "x"}); err == nil {
			t.Errorf("SaveEnvKeys wrote forbidden key %q", k)
		}
	}
	// And no .env was created by the rejected writes.
	if _, err := os.Stat(".env"); err == nil {
		t.Error(".env created by a rejected write")
	}
}

func TestSaveEnvKeysRejectsNewlineInjection(t *testing.T) {
	chdirTemp(t)
	// The classic attack: a value that opens a second line flipping a guardrail.
	err := SaveEnvKeys(map[string]string{
		"OPENROUTER_API_KEY": "x\nDRY_RUN=false\nAGENT_WRITE_DISABLED=false",
	})
	if err == nil {
		t.Fatal("newline-bearing value was accepted")
	}
	if _, statErr := os.Stat(".env"); statErr == nil {
		t.Error(".env written despite injection value")
	}
}

func TestSaveEnvKeysMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	chdirTemp(t)
	if err := SaveEnvKeys(map[string]string{"OPENROUTER_API_KEY": "k"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(".env")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", fi.Mode().Perm())
	}
}

func TestSaveEnvKeysConcurrentNoLostUpdate(t *testing.T) {
	chdirTemp(t)
	var wg sync.WaitGroup
	// Two integrations saved concurrently; both key sets must survive.
	saves := []map[string]string{
		{"OPENROUTER_API_KEY": "or"},
		{"SLACK_XOXC_TOKEN": "xc", "SLACK_XOXD_TOKEN": "xd"},
	}
	for _, kv := range saves {
		wg.Add(1)
		go func(m map[string]string) {
			defer wg.Done()
			if err := SaveEnvKeys(m); err != nil {
				t.Errorf("save: %v", err)
			}
		}(kv)
	}
	wg.Wait()
	got, _ := os.ReadFile(".env")
	s := string(got)
	for _, want := range []string{"OPENROUTER_API_KEY=or", "SLACK_XOXC_TOKEN=xc", "SLACK_XOXD_TOKEN=xd"} {
		if !strings.Contains(s, want) {
			t.Errorf("lost concurrent update %q:\n%s", want, s)
		}
	}
}

func TestSaveEnvKeysRoundTripThroughLoader(t *testing.T) {
	chdirTemp(t)
	if err := SaveEnvKeys(map[string]string{"OPENROUTER_API_KEY": "sk-roundtrip"}); err != nil {
		t.Fatal(err)
	}
	// The loader must read back exactly what we wrote (fresh key, not in env).
	os.Unsetenv("OPENROUTER_API_KEY")
	loadDotenv(filepath.Join(".", ".env"))
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "sk-roundtrip" {
		t.Errorf("loader read %q, want sk-roundtrip", v)
	}
	os.Unsetenv("OPENROUTER_API_KEY")
}
