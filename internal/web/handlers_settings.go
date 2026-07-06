package web

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/integrations/probe"
	"github.com/phuc-nt/dandori/internal/store"
)

// Settings/integrations UI: paste credentials, test a connection, save to
// ./.env. Saving requires a restart to take effect for background workers
// (they bind config at boot) — the UI says so. Secrets are never echoed back
// (masked), never logged, never put in the audit detail.

// handleSettings renders the integration cards with current status.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "settings_integrations", map[string]any{
		"Page":   "settings",
		"Health": Collect(s.Cfg, s.Store),
		"Masked": s.maskedValues(),
	})
}

// handleSettingsTest runs a read-only probe for one integration and caches the
// result, without saving anything.
func (s *Server) handleSettingsTest(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if config.ManagedKeys(name) == nil && name != "github" && name != "gws" {
		http.Error(w, "unknown integration", http.StatusNotFound)
		return
	}
	s.runTest(w, r, name)
}

// handleSettingsSave writes the pasted keys to ./.env, applies them in-process
// (so the immediate test works), caches the test result, and tells the user a
// restart is needed for workers.
func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	keys := config.ManagedKeys(name)
	if keys == nil {
		http.Error(w, "unknown integration", http.StatusNotFound)
		return
	}
	kv := map[string]string{}
	for _, k := range keys {
		if v := r.FormValue(k); v != "" {
			kv[k] = v
		}
	}
	if len(kv) == 0 {
		s.renderFragment(w, r, "settings_integrations", "inttest_result",
			testView(name, probe.ProbeResult{OK: false, Note: "chưa nhập giá trị nào"}, true))
		return
	}
	if err := config.SaveEnvKeys(kv); err != nil {
		// err may name a key but never the value; still keep it terse.
		s.renderFragment(w, r, "settings_integrations", "inttest_result",
			testView(name, probe.ProbeResult{OK: false, Note: "không lưu được cấu hình"}, true))
		return
	}
	// Apply in-process so the immediate probe reflects the new values. This
	// does NOT reach background workers — they need a restart.
	for k, v := range kv {
		_ = os.Setenv(k, v)
	}
	s.Cfg.ReloadSecretsFromEnv()
	// Audit the change WITHOUT any secret value.
	a := &govern.Audit{St: s.Store, Actor: s.actor(r)}
	_, _ = a.Append("credential_updated", name, "cập nhật qua UI")
	s.runTest(w, r, name)
}

// runTest probes an integration, caches the outcome, and renders the result
// fragment (with the restart notice).
func (s *Server) runTest(w http.ResponseWriter, r *http.Request, name string) {
	res := probe.Probe(name, s.Cfg)
	cache, _ := json.Marshal(TestInfo{OK: res.OK, At: store.Now(), Note: res.Note})
	_ = s.Store.SetSetting("inttest:"+name, string(cache))
	s.renderFragment(w, r, "settings_integrations", "inttest_result", testView(name, res, true))
}

// testView is the fragment data for a probe result.
func testView(name string, res probe.ProbeResult, restart bool) map[string]any {
	return map[string]any{
		"Name": name, "OK": res.OK, "Note": res.Note, "Restart": restart,
	}
}

// maskedValues returns a display-safe view of currently-set secrets: last 4
// chars only, never the full value.
func (s *Server) maskedValues() map[string]string {
	i := s.Cfg.Integrations
	out := map[string]string{}
	set := func(name, v string) {
		if v != "" {
			out[name] = mask(v)
		}
	}
	set("ATLASSIAN_API_TOKEN", i.AtlassianToken)
	set("ATLASSIAN_SITE_NAME", i.AtlassianSite)
	set("ATLASSIAN_USER_EMAIL", i.AtlassianEmail)
	set("SLACK_XOXC_TOKEN", i.SlackXoxc)
	set("SLACK_XOXD_TOKEN", i.SlackXoxd)
	set("OPENROUTER_API_KEY", s.Cfg.OpenRouterKey)
	return out
}

func mask(v string) string {
	if len(v) <= 4 {
		return "••••"
	}
	return "••••" + v[len(v)-4:]
}
