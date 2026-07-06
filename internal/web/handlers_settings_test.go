package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// settingsServer runs the console in a temp CWD so ./.env writes are isolated.
// It also clears any managed env keys the save handler may os.Setenv, so a
// save in one test never leaks into config.Load of the next.
func settingsServer(t *testing.T) *Server {
	t.Helper()
	orig, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chdir(orig)
		for _, k := range []string{"OPENROUTER_API_KEY", "ATLASSIAN_SITE_NAME",
			"ATLASSIAN_USER_EMAIL", "ATLASSIAN_API_TOKEN", "CONFLUENCE_SPACE_ID",
			"SLACK_XOXC_TOKEN", "SLACK_XOXD_TOKEN", "SLACK_TEAM_DOMAIN"} {
			os.Unsetenv(k)
		}
	})
	return testServer(t)
}

func TestSettingsSaveUnknownIntegration404(t *testing.T) {
	s := settingsServer(t)
	rec := postForm(t, s, "/settings/integrations/bogus", url.Values{"X": {"y"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSettingsSaveWritesEnvAndCachesTest(t *testing.T) {
	s := settingsServer(t)
	// Save OpenRouter key (probe will fail against the real network, but the
	// save + cache path is what we assert; note is credential-free).
	form := url.Values{"OPENROUTER_API_KEY": {"sk-test-value-1234"}}
	rec := postForm(t, s, "/settings/integrations/openrouter", form)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// The secret must never appear in the response.
	if strings.Contains(rec.Body.String(), "sk-test-value-1234") {
		t.Error("response leaked the saved secret")
	}
	// It was written to ./.env.
	env, _ := os.ReadFile(".env")
	if !strings.Contains(string(env), "OPENROUTER_API_KEY=sk-test-value-1234") {
		t.Errorf(".env missing saved key:\n%s", env)
	}
	// In-process config reflects it (so the immediate probe used the new value).
	if s.Cfg.OpenRouterKey != "sk-test-value-1234" {
		t.Errorf("config not reloaded: %q", s.Cfg.OpenRouterKey)
	}
	// A test result was cached.
	if s.Store.Setting("inttest:openrouter") == "" {
		t.Error("no cached inttest result after save")
	}
}

func TestSettingsRejectsOriginNullOnPost(t *testing.T) {
	s := settingsServer(t)
	req := httptest.NewRequest("POST", "/settings/integrations/openrouter",
		strings.NewReader("OPENROUTER_API_KEY=x"))
	req.Host = s.Cfg.Listen
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	req.Header.Set("Sec-Fetch-Site", "cross-site") // sandboxed iframe fingerprint
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (Origin: null cross-site must be rejected)", rec.Code)
	}
}

func TestSettingsPageRendersMaskedNotRaw(t *testing.T) {
	s := settingsServer(t)
	s.Cfg.OpenRouterKey = "sk-secret-abcd"
	rec := get(t, s, "/settings/integrations")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "sk-secret-abcd") {
		t.Error("settings page leaked raw secret")
	}
	if !strings.Contains(body, "••••abcd") {
		t.Error("settings page did not show masked secret")
	}
}
