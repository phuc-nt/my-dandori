package web

import (
	"encoding/json"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

// Health is the machine-readable status the /healthz endpoint returns and the
// settings/wizard pages read. It never contains secret values — only booleans
// and cached last-test outcomes. Collect lives in package web because every
// consumer (healthz handler, wizard, settings render) is here; promote to its
// own package only if a non-web caller (e.g. `dandori doctor`) appears.
type Health struct {
	DB             string              `json:"db"` // "ok" or an error string
	Integrations   []IntegrationHealth `json:"integrations"`
	HookedProjects int                 `json:"hooked_projects"`
	RunsCaptured   int                 `json:"runs_captured"`
}

// IntegrationHealth reports whether an integration is configured and the
// outcome of the last connection test (from the inttest:<name> settings cache,
// written by the settings Test/Save handlers). LastTest is nil when never
// tested.
type IntegrationHealth struct {
	Name       string    `json:"name"`
	Configured bool      `json:"configured"`
	LastTest   *TestInfo `json:"last_test,omitempty"`
}

// TestInfo is the cached result of a connection test. AgeDays lets consumers
// grey out a stale "ok" (tokens die externally; a week-old green is not proof
// of a live connection).
type TestInfo struct {
	OK      bool    `json:"ok"`
	At      string  `json:"at"`
	Note    string  `json:"note,omitempty"`
	AgeDays float64 `json:"age_days"`
}

// integrationNames is the fixed set the console reports on, in display order.
var integrationNames = []string{"jira", "confluence", "slack", "github", "gws", "openrouter"}

// Collect assembles the current health snapshot. It performs no outbound calls:
// configured is derived from config fields, last_test from the settings cache.
func Collect(cfg *config.Config, st *store.Store) Health {
	h := Health{DB: "ok"}
	// A trivial query proves the DB is reachable.
	if err := st.DB.Ping(); err != nil {
		h.DB = err.Error()
	}
	for _, name := range integrationNames {
		lt := lastTest(st, name)
		configured := integrationConfigured(cfg, name)
		// github/gws have no config field (keyring-CLI); treat a cached
		// successful test as the signal they are configured.
		if !configured && lt != nil && lt.OK {
			configured = true
		}
		h.Integrations = append(h.Integrations, IntegrationHealth{
			Name:       name,
			Configured: configured,
			LastTest:   lt,
		})
	}
	h.HookedProjects = st.CountSettingsPrefix("hooked:")
	h.RunsCaptured = st.CountRuns()
	return h
}

// integrationConfigured reports whether the credentials an integration needs
// are present in config. gh/github and gws are keyring-CLI based (no config
// field), so "configured" for them means a successful test was cached.
func integrationConfigured(cfg *config.Config, name string) bool {
	i := cfg.Integrations
	switch name {
	case "jira", "confluence":
		return i.AtlassianSite != "" && i.AtlassianEmail != "" && i.AtlassianToken != ""
	case "slack":
		return i.SlackXoxc != "" && i.SlackXoxd != ""
	case "openrouter":
		return cfg.OpenRouterKey != ""
	default: // github, gws: no config field, infer from a cached ok test
		return false
	}
}

// lastTest reads the cached inttest:<name> result, or nil when never tested.
func lastTest(st *store.Store, name string) *TestInfo {
	raw := st.Setting("inttest:" + name)
	if raw == "" {
		return nil
	}
	var ti TestInfo
	if err := json.Unmarshal([]byte(raw), &ti); err != nil {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, ti.At); err == nil {
		ti.AgeDays = time.Since(t).Hours() / 24
	}
	return &ti
}
