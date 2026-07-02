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
	JiraProject  string `yaml:"jira_project"`
	SlackChannel string `yaml:"slack_channel"`
	GithubRepo   string `yaml:"github_repo"`
	// Secrets — filled from env only, never serialized back to YAML.
	AtlassianSite  string `yaml:"-"`
	AtlassianEmail string `yaml:"-"`
	AtlassianToken string `yaml:"-"`
	SlackXoxc      string `yaml:"-"`
	SlackXoxd      string `yaml:"-"`
	SlackTeam      string `yaml:"-"`
}

type Config struct {
	DBPath               string             `yaml:"db_path"`
	Listen               string             `yaml:"listen"`
	UserName             string             `yaml:"user_name"`
	DryRun               bool               `yaml:"dry_run"`
	AgentWriteDisabled   bool               `yaml:"-"`
	Budget               Budget             `yaml:"budget"`
	GateWaitSeconds      int                `yaml:"gate_wait_seconds"`
	WatchIntervalSeconds int                `yaml:"watch_interval_seconds"`
	ProjectsDir          string             `yaml:"projects_dir"`
	LearnWindowDays      int                `yaml:"learn_window_days"`
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
		WatchIntervalSeconds: 60,
		ProjectsDir:          filepath.Join(home, ".claude", "projects"),
		LearnWindowDays:      30,
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
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
