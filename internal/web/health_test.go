package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

func TestCollectReportsConfiguredAndCounts(t *testing.T) {
	s := testServer(t)
	// Start from a known-empty integration config; the process env may carry a
	// real OPENROUTER_API_KEY (dev machine) or one leaked from another test.
	s.Cfg.Integrations = config.Integrations{}
	s.Cfg.OpenRouterKey = ""
	h := Collect(s.Cfg, s.Store)
	if h.DB != "ok" {
		t.Fatalf("db = %q, want ok", h.DB)
	}
	if len(h.Integrations) != 6 {
		t.Fatalf("integrations = %d, want 6", len(h.Integrations))
	}
	for _, i := range h.Integrations {
		if i.Configured {
			t.Errorf("%s configured with empty config", i.Name)
		}
		if i.LastTest != nil {
			t.Errorf("%s has last_test without a cached test", i.Name)
		}
	}
	if h.HookedProjects != 0 || h.RunsCaptured != 0 {
		t.Fatalf("hooked=%d runs=%d, want 0 0", h.HookedProjects, h.RunsCaptured)
	}

	// Configure Atlassian → jira + confluence become configured.
	s.Cfg.Integrations.AtlassianSite = "acme"
	s.Cfg.Integrations.AtlassianEmail = "a@b.c"
	s.Cfg.Integrations.AtlassianToken = "tok"
	h = Collect(s.Cfg, s.Store)
	for _, i := range h.Integrations {
		want := i.Name == "jira" || i.Name == "confluence"
		if i.Configured != want {
			t.Errorf("%s configured=%v, want %v", i.Name, i.Configured, want)
		}
	}

	// Hook a project + record a run → counts reflect it.
	if err := s.Store.SetSetting("hooked:/tmp/proj", store.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO runs(id, started_at) VALUES('r1',?)`, store.Now()); err != nil {
		t.Fatal(err)
	}
	h = Collect(s.Cfg, s.Store)
	if h.HookedProjects != 1 {
		t.Errorf("hooked = %d, want 1", h.HookedProjects)
	}
	if h.RunsCaptured != 1 {
		t.Errorf("runs = %d, want 1", h.RunsCaptured)
	}
}

func TestCollectCachedTestAgeAndGhInference(t *testing.T) {
	s := testServer(t)
	// A cached github test with ok=true makes github "configured" despite no
	// config field, and age_days is derived from the stored timestamp.
	old := store.Now() // ~0 days old
	cache, _ := json.Marshal(TestInfo{OK: true, At: old, Note: "ok"})
	if err := s.Store.SetSetting("inttest:github", string(cache)); err != nil {
		t.Fatal(err)
	}
	h := Collect(s.Cfg, s.Store)
	var gh *IntegrationHealth
	for i := range h.Integrations {
		if h.Integrations[i].Name == "github" {
			gh = &h.Integrations[i]
		}
	}
	if gh == nil || !gh.Configured {
		t.Fatalf("github not inferred configured from cached ok test: %+v", gh)
	}
	if gh.LastTest == nil || !gh.LastTest.OK {
		t.Fatalf("github last_test missing: %+v", gh)
	}
	if gh.LastTest.AgeDays < 0 || gh.LastTest.AgeDays > 1 {
		t.Errorf("age_days = %v, want ~0", gh.LastTest.AgeDays)
	}
}

func TestHealthzEndpointNoSecretLeak(t *testing.T) {
	s := testServer(t)
	s.Cfg.Integrations.AtlassianToken = "SUPER-SECRET-TOKEN"
	s.Cfg.OpenRouterKey = "sk-leak-me"
	rec := get(t, s, "/healthz")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, secret := range []string{"SUPER-SECRET-TOKEN", "sk-leak-me"} {
		if strings.Contains(body, secret) {
			t.Errorf("healthz leaked secret %q in body", secret)
		}
	}
	// Sanity: it is real JSON with the expected shape.
	var h Health
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatalf("healthz body not valid Health JSON: %v", err)
	}
}
