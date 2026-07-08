// Package config loads Dandori configuration: defaults → YAML file →
// environment variables → .env file in CWD (which never overrides real env).
package config

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Budget struct {
	GlobalMonthlyUSD float64 `yaml:"global_monthly_usd"`
	WarnPcts         []int   `yaml:"warn_pcts"`
}

type Integrations struct {
	JiraProject       string `yaml:"jira_project"`
	SlackChannel      string `yaml:"slack_channel"`
	GithubRepo        string `yaml:"github_repo"`
	ConfluenceSpaceID string `yaml:"confluence_space_id"`
	// Secrets — filled from env only, never serialized back to YAML.
	AtlassianSite  string `yaml:"-"`
	AtlassianEmail string `yaml:"-"`
	AtlassianToken string `yaml:"-"`
	SlackXoxc      string `yaml:"-"`
	SlackXoxd      string `yaml:"-"`
	SlackTeam      string `yaml:"-"`
}

type Config struct {
	DBPath               string   `yaml:"db_path"`
	Listen               string   `yaml:"listen"`
	UserName             string   `yaml:"user_name"`
	DryRun               bool     `yaml:"dry_run"`
	AgentWriteDisabled   bool     `yaml:"-"`
	Budget               Budget   `yaml:"budget"`
	GateWaitSeconds      int      `yaml:"gate_wait_seconds"`
	ApprovalTTLMinutes   int      `yaml:"approval_ttl_minutes"`
	Approvers            []string `yaml:"approvers"` // Slack user ids/names allowed to decide; empty = anyone
	WatchIntervalSeconds int      `yaml:"watch_interval_seconds"`
	ProjectsDir          string   `yaml:"projects_dir"`
	// SandboxEnabled gates the G2 write-scope guardrail (Write/Edit outside the
	// run's cwd). Defaults true. Set false to let runs edit sibling repos on a
	// trusted single-dev machine — loses the isolation guarantee, so keep it on
	// for multi-user / shared setups.
	SandboxEnabled *bool `yaml:"sandbox_enabled"`
	// BlockEnabled gates the G1 block guardrail (rm -rf /, reading .env, DROP
	// TABLE, force-push). Defaults true — this is the last-line safety net.
	// Set false ONLY on a trusted single-dev machine; dangerous commands then
	// run unblocked.
	BlockEnabled    *bool `yaml:"block_enabled"`
	LearnWindowDays int   `yaml:"learn_window_days"`
	// v8 notifications. PublicBaseURL is the console origin used to build deep
	// links in Slack alerts. NotifyFlagStaleDays is the age past which an open
	// flag is announced (default 3).
	PublicBaseURL       string `yaml:"public_base_url"`
	NotifyFlagStaleDays int    `yaml:"notify_flag_stale_days"`
	CalibrateWithHumans bool   `yaml:"calibrate_with_humans"`
	OpenRouterKey       string `yaml:"-"`
	OpenRouterModel     string `yaml:"-"`
	// Central mode. Client side: ServerURL+IngestToken come from the 0600
	// connect file (~/.dandori/connect.yaml) or env. Server side:
	// IngestListen is where the token-authed ingest listener binds; the
	// listener only starts when IngestToken is set.
	ServerURL    string `yaml:"-"`
	IngestToken  string `yaml:"-"`
	IngestListen string `yaml:"ingest_listen"`
	// AllowLegacyIngestToken keeps the pre-v10 shared Cfg.IngestToken accepted
	// by the ingest listener during the v10 dual-accept window (default true).
	// Runs authenticated this way attribute to the fixed principal
	// "legacy-shared@ingest" — never to a client-supplied header (H1). Set
	// false to force per-operator tokens only; the shared token then 401s.
	AllowLegacyIngestToken bool `yaml:"allow_legacy_ingest_token"`
	// CEO chatbot caps.
	ChatMaxTurns         int                `yaml:"chat_max_turns"`
	ChatDailyTokenBudget int                `yaml:"chat_daily_token_budget"`
	GateChecks           []string           `yaml:"gate_checks"`
	Pricing              map[string]Pricing `yaml:"pricing"`
	Integrations         Integrations       `yaml:"integrations"`
	// Console launch (v6). AgentBinaries maps an agent name to the ABSOLUTE
	// path of its binary — the ONLY binaries the console may launch (never
	// $PATH-resolved). v6 enables `claude` only; others are rejected by the
	// launcher's argv spec even if listed here.
	AgentBinaries         map[string]string `yaml:"agent_binaries"`
	MaxConcurrentLaunches int               `yaml:"max_concurrent_launches"`
	// DigestRecipients + ExportSpreadsheetID are the ONLY source of the
	// UG2b digest / UC8 Sheets-export destinations (C2 — config-pinned,
	// never request-supplied). Empty ExportSpreadsheetID means "create one
	// and remember it in settings"; empty DigestRecipients means "no-op".
	DigestRecipients    []string `yaml:"digest_recipients"`
	ExportSpreadsheetID string   `yaml:"export_spreadsheet_id"`
	SheetsExportEnabled bool     `yaml:"sheets_export_enabled"`
	// PostActionChecks (G6) run automatically after EVERY run finalizes,
	// against the run's cwd — AFTER the agent has modified it. This is code
	// execution by design: a check like "go test ./..." or a command with
	// "go generate" lets an untrusted agent's own committed code (a malicious
	// _test.go, a //go:generate directive, a crafted build constraint) run as
	// the Dandori host user, automatically, on every finalize. Trusted ONLY
	// because these strings come from operator-owned config.yaml — NEVER from
	// the DB, the web UI, an agent, or task text. DEFAULT EMPTY (opt-in).
	// Prefer non-executing checks: "go vet ./..." is safe; "go test ./..." or
	// anything invoking "go generate" is arbitrary code execution.
	PostActionChecks []string `yaml:"post_action_checks"`
}

func defaults() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		DBPath:                 filepath.Join(home, ".dandori", "dandori.db"),
		Listen:                 "127.0.0.1:4777",
		UserName:               currentUser(),
		DryRun:                 true,
		Budget:                 Budget{GlobalMonthlyUSD: 50, WarnPcts: []int{50, 75, 90}},
		GateWaitSeconds:        30,
		ApprovalTTLMinutes:     60,
		WatchIntervalSeconds:   60,
		ProjectsDir:            filepath.Join(home, ".claude", "projects"),
		LearnWindowDays:        30,
		CalibrateWithHumans:    true,
		IngestListen:           "0.0.0.0:4778",
		AllowLegacyIngestToken: true,
		ChatMaxTurns:           6,
		ChatDailyTokenBudget:   200_000,
		GateChecks:             []string{"go vet ./...", "go test ./..."},
		Pricing:                defaultPricing(),
		Integrations:           Integrations{JiraProject: "SCRUM", SlackChannel: ""},
		MaxConcurrentLaunches:  4,
	}
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "dandori"
}

// Load builds config: defaults → YAML at path (default ~/.dandori/config.yaml)
// → process env → .env in CWD (only for vars not already set in env).
func Load(path string) (*Config, error) {
	cfg := defaults()
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".dandori", "config.yaml")
	}
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, err
		}
	}
	loadDotenv(".env") // sets os env for keys not already present
	cfg.loadConnectFile()
	cfg.applyEnv()
	cfg.DBPath = expandHome(cfg.DBPath)
	cfg.ProjectsDir = expandHome(cfg.ProjectsDir)
	cfg.validateDigestRecipients()
	return cfg, nil
}

// validateDigestRecipients is a best-effort sanity check (not a hard
// validator) — it logs addresses that don't look like an email so a typo in
// config.yaml is visible instead of silently swallowed by a failed send.
func (c *Config) validateDigestRecipients() {
	for _, addr := range c.DigestRecipients {
		if i := strings.IndexByte(addr, '@'); i <= 0 || i == len(addr)-1 || strings.ContainsAny(addr, " \t\r\n") {
			log.Printf("config: digest_recipients entry %q does not look like an email address", addr)
		}
	}
}

// ReloadSecretsFromEnv re-reads env-sourced fields into c after the process
// env was updated (e.g. the settings UI saved a credential and os.Setenv'd it).
// It only refreshes fields that come from env; it does not re-read the YAML.
func (c *Config) ReloadSecretsFromEnv() { c.applyEnv() }

func (c *Config) applyEnv() {
	if v := os.Getenv("DANDORI_DB"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("DANDORI_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("DRY_RUN"); v != "" {
		c.DryRun = v != "false" && v != "0"
	}
	if v := os.Getenv("AGENT_WRITE_DISABLED"); v != "" {
		c.AgentWriteDisabled = v == "true" || v == "1"
	}
	if v := os.Getenv("MONTHLY_BUDGET_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Budget.GlobalMonthlyUSD = f
		}
	}
	if v := os.Getenv("DANDORI_GATE_WAIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.GateWaitSeconds = n
		}
	}
	if v := os.Getenv("PUBLIC_BASE_URL"); v != "" {
		c.PublicBaseURL = v
	}
	if v := os.Getenv("NOTIFY_FLAG_STALE_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.NotifyFlagStaleDays = n
		}
	}
	c.OpenRouterKey = os.Getenv("OPENROUTER_API_KEY")
	c.OpenRouterModel = os.Getenv("OPENROUTER_MODEL")
	if v := os.Getenv("DANDORI_SERVER_URL"); v != "" {
		c.ServerURL = v
	}
	if v := os.Getenv("DANDORI_INGEST_TOKEN"); v != "" {
		c.IngestToken = v
	}
	if v := os.Getenv("DANDORI_INGEST_LISTEN"); v != "" {
		c.IngestListen = v
	}
	if v := os.Getenv("DANDORI_ALLOW_LEGACY_INGEST_TOKEN"); v != "" {
		c.AllowLegacyIngestToken = v != "false" && v != "0"
	}
	if v := os.Getenv("SLACK_APPROVERS"); v != "" {
		c.Approvers = nil
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a != "" {
				c.Approvers = append(c.Approvers, a)
			}
		}
	}
	i := &c.Integrations
	i.AtlassianSite = os.Getenv("ATLASSIAN_SITE_NAME")
	i.AtlassianEmail = os.Getenv("ATLASSIAN_USER_EMAIL")
	i.AtlassianToken = os.Getenv("ATLASSIAN_API_TOKEN")
	i.SlackXoxc = os.Getenv("SLACK_XOXC_TOKEN")
	i.SlackXoxd = os.Getenv("SLACK_XOXD_TOKEN")
	i.SlackTeam = os.Getenv("SLACK_TEAM_DOMAIN")
	if v := os.Getenv("SLACK_CHANNEL_ID"); v != "" {
		i.SlackChannel = v
	} else if v := os.Getenv("SLACK_REPORT_CHANNEL"); v != "" {
		i.SlackChannel = v
	}
	if v := os.Getenv("JIRA_PROJECT_KEY"); v != "" {
		i.JiraProject = v
	}
	if v := os.Getenv("GITHUB_REPO"); v != "" {
		i.GithubRepo = v
	}
	if v := os.Getenv("CONFLUENCE_SPACE_ID"); v != "" {
		i.ConfluenceSpaceID = v
	}
}

// ConnectFile is where `dandori connect` stores the central-server URL and
// ingest token (mode 0600 — it holds a secret).
func ConnectFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dandori", "connect.yaml")
}

// loadConnectFile reads the client-side central-mode settings; env vars
// applied later still override. Absent file = local mode, not an error.
func (c *Config) loadConnectFile() {
	b, err := os.ReadFile(ConnectFile())
	if err != nil {
		return
	}
	var cf struct {
		ServerURL string `yaml:"server_url"`
		Token     string `yaml:"token"`
	}
	if yaml.Unmarshal(b, &cf) == nil {
		c.ServerURL = cf.ServerURL
		c.IngestToken = cf.Token
	}
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
