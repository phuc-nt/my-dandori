package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(yamlPath, []byte("listen: 127.0.0.1:9999\nuser_name: yamluser\n"), 0o644)

	t.Setenv("DANDORI_LISTEN", "127.0.0.1:8888")
	t.Setenv("DANDORI_DB", filepath.Join(dir, "x.db"))

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8888" {
		t.Errorf("env should override yaml: got %s", cfg.Listen)
	}
	if cfg.UserName != "yamluser" {
		t.Errorf("yaml should override default: got %s", cfg.UserName)
	}
	if !cfg.DryRun {
		t.Error("dry_run must default to true")
	}
}

func TestDryRunEnvOverride(t *testing.T) {
	t.Setenv("DRY_RUN", "false")
	cfg, err := Load(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DryRun {
		t.Error("DRY_RUN=false must disable dry run")
	}
}

func TestDotenvDoesNotOverrideEnv(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(cwd) })

	os.WriteFile(".env", []byte(
		"# comment\nFOO_FROM_DOTENV=hello\nSLACK_TEST_COOKIE=abc%2Bdef%3D\nQUOTED=\"q v\"\nPRESET=dotenv\n"), 0o644)
	t.Setenv("PRESET", "realenv")
	os.Unsetenv("FOO_FROM_DOTENV")
	os.Unsetenv("SLACK_TEST_COOKIE")
	os.Unsetenv("QUOTED")

	loadDotenv(".env")
	if got := os.Getenv("FOO_FROM_DOTENV"); got != "hello" {
		t.Errorf("dotenv var: got %q", got)
	}
	if got := os.Getenv("SLACK_TEST_COOKIE"); got != "abc%2Bdef%3D" {
		t.Errorf("URL-encoded value must be preserved byte-exact: got %q", got)
	}
	if got := os.Getenv("QUOTED"); got != "q v" {
		t.Errorf("quotes must be stripped: got %q", got)
	}
	if got := os.Getenv("PRESET"); got != "realenv" {
		t.Errorf("real env must win over .env: got %q", got)
	}
}

func TestPricingPrefixMatch(t *testing.T) {
	cfg := defaults()
	cases := []struct {
		model  string
		wantIn float64
	}{
		{"claude-sonnet-5", 3},
		{"claude-haiku-4-5-20251001", 1},
		{"claude-fable-5[1m]", 5},
		{"gpt-something", 3}, // default
	}
	for _, c := range cases {
		if p := cfg.PriceFor(c.model); p.Input != c.wantIn {
			t.Errorf("PriceFor(%s).Input = %v, want %v", c.model, p.Input, c.wantIn)
		}
	}
	// 1M output tokens on sonnet = $15
	if got := cfg.Cost("claude-sonnet-5", 0, 1_000_000, 0, 0); got != 15 {
		t.Errorf("Cost = %v, want 15", got)
	}
}
