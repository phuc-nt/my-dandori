// Package config loads Dandori configuration: defaults → YAML file →
// environment variables → .env file in CWD (which never overrides real env).
package config

import (
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
	LearnWindowDays      int      `yaml:"learn_window_days"`
	CalibrateWithHumans  bool     `yaml:"calibrate_with_humans"`
	OpenRouterKey        string   `yaml:"-"`
	OpenRouterModel      string   `yaml:"-"`
	// Central mode. Client side: ServerURL+IngestToken come from the 0600
	// connect file (~/.dandori/connect.yaml) or env. Server side:
	// IngestListen is where the token-authed ingest listener binds; the
	// listener only starts when IngestToken is set.
	ServerURL    string `yaml:"-"`
	IngestToken  string `yaml:"-"`
	IngestListen string `yaml:"ingest_listen"`
	// CEO chatbot caps.
	ChatMaxTurns         int                `yaml:"chat_max_turns"`
	ChatDailyTokenBudget int                `yaml:"chat_daily_token_budget"`
	GateChecks           []string           `yaml:"gate_checks"`
	Pricing              map[string]Pricing `yaml:"pricing"`
	Integrations         Integrations       `yaml:"integrations"`
}

func defaults() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		DBPath:               filepath.Join(home, ".dandori", "dandori.db"),
		Listen:               "127.0.0.1:4777",
		UserName:             currentUser(),
		DryRun:               true,
		Budget:               Budget{GlobalMonthlyUSD: 50, WarnPcts: []int{50, 75, 90}},
		GateWaitSeconds:      30,
		ApprovalTTLMinutes:   60,
		WatchIntervalSeconds: 60,
		ProjectsDir:          filepath.Join(home, ".claude", "projects"),
		LearnWindowDays:      30,
		CalibrateWithHumans:  true,
		IngestListen:         "0.0.0.0:4778",
		ChatMaxTurns:         6,
		ChatDailyTokenBudget: 200_000,
		GateChecks:           []string{"go vet ./...", "go test ./..."},
		Pricing:              defaultPricing(),
		Integrations:         Integrations{JiraProject: "SCRUM", SlackChannel: ""},
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
	return cfg, nil
}

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
